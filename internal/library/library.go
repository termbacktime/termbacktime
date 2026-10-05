// Package library indexes private local recordings without moving imported files
package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/termbacktime/termbacktime/internal/config"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/sharing"
)

var ErrMissing = errors.New("recording file is missing")
var ErrAmbiguous = errors.New("ambiguous recording ID")

type Library struct{ Root string }
type Entry struct {
	Checked     bool   `json:"-"`
	Enriched    bool   `json:"-"`
	Pinned      bool   `json:"-"`
	UploadCount int    `json:"-"`
	Events      int    `json:"events,omitempty"`
	Version     int    `json:"version,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Identity    string `json:"file_identity,omitempty"`
	Modified    int64  `json:"modified_ns,omitempty"`
	Changed     int64  `json:"changed_ns,omitempty"`
	ID          string `json:"id"`
	Path        string `json:"path"`
	Title       string `json:"title"`
	Started     int64  `json:"started"`
	Duration    int64  `json:"duration_ms"`
	Bytes       int64  `json:"bytes"`
	Status      string `json:"status"`
}
type Receipt struct {
	Remote      *sharing.Reference `json:"remote,omitempty"`
	RecordingID string             `json:"recording_id"`
	Link        string             `json:"link"`
	Created     int64              `json:"created"`
	Public      bool               `json:"public"`
	Encrypted   bool               `json:"encrypted"`
	Metadata    bool               `json:"metadata"`
}

func (l Library) Init() error {
	for _, p := range []string{"", "recordings", "library", "shares"} {
		if err := config.EnsurePrivateDir(filepath.Join(l.Root, p)); err != nil {
			return err
		}
	}
	return nil
}

func (l Library) Output(path string) (string, error) {
	if path == "" {
		path = filepath.Join(l.Root, "recordings", "termbacktime-"+time.Now().Format("20060102-150405.000")+"-"+recording.NewID()[:8]+".tbt")
	}
	return config.ExpandPath(path)
}

func pathID(path string) string {
	sum := sha256.Sum256([]byte(strings.TrimSuffix(path, ".partial")))
	return hex.EncodeToString(sum[:16])
}

func (l Library) Register(path string) (*Entry, error) {
	path, err := config.ExpandPath(path)
	if err != nil {
		return nil, err
	}
	old, _ := l.readEntry(pathID(path))
	return l.register(path, old)
}

func (l Library) register(path string, old *Entry) (*Entry, error) {
	return l.registerContext(context.Background(), path, old)
}

func (l Library) registerContext(ctx context.Context, path string, old *Entry) (*Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("invalid recording file")
	}
	if old != nil && old.Path == strings.TrimSuffix(path, ".partial") {
		if strings.HasSuffix(path, ".partial") {
			f, err := os.Open(path)
			if err == nil {
				lockErr := unix.Flock(int(f.Fd()), unix.LOCK_SH|unix.LOCK_NB)
				locked := errors.Is(lockErr, unix.EWOULDBLOCK) || errors.Is(lockErr, unix.EAGAIN)
				f.Close()
				if locked {
					old.Status = "recording"
					old.Bytes = before.Size()
					return old, nil
				}
			}
			if old.Version == 2 && old.Status == "partial" && old.Fingerprint != "" && old.Fingerprint == recording.Format+":"+fileFingerprint(before) {
				return old, nil
			}
		} else if old.Version == 2 && old.Status == "ready" && old.Fingerprint != "" && old.Fingerprint == recording.Format+":"+fileFingerprint(before) {
			return old, nil
		}
	}
	s, err := recording.InspectContext(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := l.Init(); err != nil {
		return nil, err
	}
	e := &Entry{Events: s.Events, ID: pathID(path), Path: strings.TrimSuffix(path, ".partial"), Title: s.Title, Started: s.Started, Duration: s.Duration, Bytes: s.Bytes, Status: s.Status}
	unlock, err := l.waitMutationLock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	after, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if s.Status != "recording" && fileFingerprint(before) != fileFingerprint(after) {
		return nil, fmt.Errorf("recording changed during inspection")
	}
	e.Version, e.Fingerprint = 2, recording.Format+":"+fileFingerprint(after)
	e.Modified = after.ModTime().UnixNano()
	e.Identity, e.Changed = fileIdentity(after)
	if err := l.save(e); err != nil {
		return nil, err
	}
	return e, nil
}

func fileIdentity(info os.FileInfo) (string, int64) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", 0
	}
	value := reflect.ValueOf(stat).Elem()
	changed := value.FieldByName("Ctim")
	if !changed.IsValid() {
		changed = value.FieldByName("Ctimespec")
	}
	if !changed.IsValid() {
		return "", 0
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), changed.FieldByName("Sec").Int()*1e9 + changed.FieldByName("Nsec").Int()
}
func fileFingerprint(info os.FileInfo) string {
	identity, changed := fileIdentity(info)
	if identity == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d:%d:%d", identity, info.Size(), info.ModTime().UnixNano(), changed)
}

func (l Library) readEntry(id string) (*Entry, error) {
	if !recording.ValidID(id) {
		return nil, fmt.Errorf("invalid library ID")
	}
	f, err := os.Open(filepath.Join(l.Root, "library", id+".json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := recording.ReadBounded(f, 1<<20)
	if err != nil {
		return nil, err
	}
	var e Entry
	if json.Unmarshal(data, &e) != nil || e.ID != id || !filepath.IsAbs(e.Path) || pathID(e.Path) != id {
		return nil, fmt.Errorf("invalid library entry %s", id)
	}
	return &e, nil
}

func (l Library) save(e *Entry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Join(l.Root, "library"), ".index-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(l.Root, "library", e.ID+".json"))
}

func (l Library) List() ([]Entry, error) { return l.ListContext(context.Background()) }

func (l Library) ListContext(ctx context.Context) ([]Entry, error) {
	result, _, err := l.ListWithWarnings(ctx)
	return result, err
}

func (l Library) ListWithWarnings(ctx context.Context) ([]Entry, []string, error) {
	entries := map[string]Entry{}
	warnings := []string{}
	err := l.StreamList(ctx, func(update ListUpdate) error {
		warnings = append(warnings, update.Warnings...)
		for _, e := range update.Entries {
			if e.Checked {
				entries[e.ID] = e
			}
		}
		return nil
	})
	result := make([]Entry, 0, len(entries))
	for _, e := range entries {
		result = append(result, e)
	}
	Sort(result, "date", "")
	return result, warnings, err
}

func (l Library) Resolve(value string) (string, error) {
	path, err := config.ExpandPath(value)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if strings.ContainsAny(value, "/\\") || strings.Contains(value, ".") {
		return "", fmt.Errorf("recording not found: %s", value)
	}
	var found *Entry
	if recording.ValidID(value) {
		found, err = l.readEntry(value)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	} else {
		files, readErr := os.ReadDir(filepath.Join(l.Root, "library"))
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return "", readErr
		}
		for _, f := range files {
			id := strings.TrimSuffix(f.Name(), ".json")
			if !f.Type().IsRegular() || !strings.HasSuffix(f.Name(), ".json") || !recording.ValidID(id) || !strings.HasPrefix(id, value) {
				continue
			}
			if found != nil {
				return "", fmt.Errorf("%w: %s", ErrAmbiguous, value)
			}
			found, err = l.readEntry(id)
			if err != nil {
				return "", err
			}
		}
	}
	if found == nil {
		return "", fmt.Errorf("recording not found: %s", value)
	}
	for _, candidate := range []string{found.Path, found.Path + ".partial"} {
		if st, err := os.Stat(candidate); err == nil {
			if !st.Mode().IsRegular() {
				return "", fmt.Errorf("invalid recording file")
			}
			return candidate, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("%w: %s", ErrMissing, found.Path)
}

func (l Library) SaveReceipt(path, link string, details ...Receipt) error {
	e, err := l.Register(path)
	if err != nil {
		return err
	}
	return l.saveReceipt(e.ID, link, details...)
}

// SaveQueuedReceipt associates a completed immutable upload with its original path,
// even when that original recording has since been moved or removed.
func (l Library) SaveQueuedReceipt(path, link string, details ...Receipt) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return l.saveReceipt(pathID(path), link, details...)
}

func (l Library) saveReceipt(id, link string, details ...Receipt) error {
	r := Receipt{}
	if len(details) > 0 {
		r = details[0]
	}
	if ref, e := sharing.Parse(link); e == nil {
		r.Remote = &ref
	}
	r.RecordingID, r.Link, r.Created = id, link, time.Now().UnixNano()
	b, _ := json.Marshal(r)
	return recording.Publish(filepath.Join(l.Root, "shares", recording.NewID()+".json"), func(w io.Writer) error { _, err := w.Write(b); return err })
}

func (l Library) ShareLink(path string) (string, error) {
	files, err := os.ReadDir(filepath.Join(l.Root, "shares"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var latest Receipt
	for _, f := range files {
		if !f.Type().IsRegular() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(l.Root, "shares", f.Name()))
		if err != nil {
			return "", err
		}
		var r Receipt
		if len(b) > 16384 || json.Unmarshal(b, &r) != nil {
			continue
		}
		if r.RecordingID == pathID(path) && r.Created > latest.Created {
			latest = r
		}
	}
	return latest.Link, nil
}
