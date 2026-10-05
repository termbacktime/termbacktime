package recording

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/termbacktime/termbacktime/internal/buildinfo"
	"golang.org/x/sys/unix"
)

type Marker struct {
	ID    string `json:"i"`
	At    int64  `json:"a"`
	Label string `json:"l"`
	Note  string `json:"n,omitempty"`
	Kind  string `json:"k"`
}

// CurrentInfo identifies the CLI that captured a new recording, not a later editor.
func CurrentInfo() Info {
	return Info{Arch: runtime.GOARCH, OS: runtime.GOOS, Go: runtime.Version(), CLI: buildinfo.Tag()}
}

var idPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func ValidID(id string) bool { return idPattern.MatchString(id) }

func validateMetadata(r *Recording, duration int64) error {
	if err := validateCallouts(r, duration); err != nil {
		return err
	}
	if err := r.Metadata.Validate(); err != nil {
		return err
	}
	if len(r.Info.CLI) > 128 || len(r.Info.UploadCLI) > 128 {
		return fmt.Errorf("CLI version metadata is too large")
	}
	if r.ID != "" && !ValidID(r.ID) {
		return fmt.Errorf("invalid recording ID")
	}
	if len(r.Title) > MaxJournalLineBytes || len(r.Markers) > 10000 {
		return fmt.Errorf("recording metadata is too large")
	}
	seen := map[string]bool{}
	for _, m := range r.Markers {
		if !ValidID(m.ID) || seen[m.ID] || m.At < 0 || m.At > duration || len(m.Label) > 1024 || len(m.Note) > 16384 || (m.Kind != "bookmark" && m.Kind != "chapter") {
			return fmt.Errorf("invalid recording marker")
		}
		seen[m.ID] = true
	}
	return nil
}

type Summary struct {
	Recording
	Duration     int64  `json:"duration_ms"`
	Events       int    `json:"events"`
	Bytes        int64  `json:"bytes"`
	Status       string `json:"status"`
	Verification string `json:"verification,omitempty"`
}

func Inspect(path string) (*Summary, error) {
	return InspectContext(context.Background(), path)
}

// InspectContext releases its file and journal lock when a superseded manager
// selection is canceled, instead of continuing to scan the entire recording.
func InspectContext(ctx context.Context, path string) (*Summary, error) {
	return inspect(ctx, path, false, nil)
}

// Preview validates the archive structure and first chunk, never all event chunks.
func Preview(ctx context.Context, path string) (*Summary, error) {
	return inspect(ctx, path, true, nil)
}

func Verify(ctx context.Context, path string, progress func(int, int)) (*Summary, error) {
	return inspect(ctx, path, false, progress)
}

func inspect(ctx context.Context, path string, quick bool, progress func(int, int)) (*Summary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxArchiveBytes {
		return nil, fmt.Errorf("invalid or oversized recording")
	}
	s := &Summary{Bytes: info.Size(), Status: "ready"}
	if strings.HasSuffix(path, ".partial") {
		s.Status = "partial"
		if err := unix.Flock(int(f.Fd()), unix.LOCK_SH|unix.LOCK_NB); err != nil {
			s.Status = "recording"
		}
		r, err := newJournalReader(f)
		if err != nil {
			return nil, err
		}
		s.Recording = r.header
		if quick {
			s.Verification = "preview; journal totals unavailable"
			return s, ctx.Err()
		}
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			_, err := r.next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
			if progress != nil {
				progress(r.events, 0)
			}
		}
		s.Duration, s.Events = r.duration, r.events
		s.Lines = nil
		return s, nil
	}
	source, err := OpenReader(f, info.Size())
	if err != nil {
		return nil, err
	}
	defer source.Close()
	s.Recording, s.Duration, s.Events = source.Manifest.Recording, source.Manifest.Duration, source.Manifest.Count
	if quick {
		s.Verification = "preview; remaining chunks not verified"
		return s, ctx.Err()
	}
	// Inspection checks every chunk with bounded memory; unchanged indexes skip this.
	checked := 0
	if err := source.Walk(ctx, func(Event) error {
		checked++
		if progress != nil {
			progress(checked, s.Events)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return s, nil
}

func Duration(r *Recording) int64 {
	if r.Source != nil {
		return r.Source.Manifest.Duration
	}
	var n int64
	for _, e := range r.Lines {
		n += e.Time
	}
	return n
}

// Save publishes a new recording without replacing a file that appeared concurrently
func Save(path string, r *Recording) error {
	return Publish(path, func(w io.Writer) error {
		return Encode(w, r)
	})
}

func Publish(path string, write func(io.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tbt-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := write(f); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Link(f.Name(), path)
}

func RecoverFile(source, destination string) error {
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("recording is still active")
	}
	return Publish(destination, func(w io.Writer) error { return finalizeJournal(w, f) })
}
