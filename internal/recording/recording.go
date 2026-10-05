// Package recording reads legacy recordings and writes recoverable local journals
package recording

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const MaxBytes = 64 << 20
const MaxEvents = 1_000_000

// A PTY chunk remains below this limit even when every byte needs JSON escaping
const MaxJournalLineBytes = 256 << 10

type Info struct {
	Arch      string `json:"a"`
	OS        string `json:"o"`
	Go        string `json:"v"`
	CLI       string `json:"c,omitempty"`
	UploadCLI string `json:"u,omitempty"`
}
type Event struct {
	Time      int64         `json:"t,omitempty"`
	Command   string        `json:"c,omitempty"`
	Lines     []string      `json:"l,omitempty"`
	Sizes     []int         `json:"s,omitempty"`
	Lifecycle *CommandEvent `json:"e,omitempty"`
}
type Recording struct {
	HistoryID string    `json:"-"`
	Source    *Source   `json:"-"`
	Metadata  *Metadata `json:"m,omitempty"`
	ID        string    `json:"u,omitempty"`
	Markers   []Marker  `json:"a,omitempty"`
	Callouts  []Callout `json:"c,omitempty"`
	Info      Info      `json:"i"`
	Started   int64     `json:"d"`
	Title     string    `json:"t,omitempty"`
	Sizes     []int     `json:"s,omitempty"`
	Lines     []Event   `json:"r,omitempty"`
	Pack      string    `json:"p,omitempty"`
}

func ReadBounded(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("input exceeds %d byte limit", max)
	}
	return b, nil
}

func validSize(s []int) bool {
	return len(s) == 2 && s[0] > 0 && s[0] <= 500 && s[1] > 0 && s[1] <= 200
}

func (r *Recording) Validate() error {
	if len(r.Sizes) == 0 {
		r.Sizes = []int{80, 24}
	}
	if !validSize(r.Sizes) {
		return fmt.Errorf("invalid terminal size")
	}
	if len(r.Lines) > MaxEvents {
		return fmt.Errorf("too many recording events")
	}
	var duration int64
	for _, e := range r.Lines {
		if err := validateEvent(e, &duration); err != nil {
			return err
		}
	}
	return validateMetadata(r, duration)
}

func validateEvent(e Event, duration *int64) error {
	if e.Time < 0 || e.Time > 86_400_000 {
		return fmt.Errorf("invalid event delay")
	}
	*duration += e.Time
	if *duration > 604_800_000 {
		return fmt.Errorf("recording exceeds seven days")
	}
	if e.Command != "" && e.Command != "s" {
		return fmt.Errorf("unknown recording command")
	}
	if e.Command == "s" && !validSize(e.Sizes) {
		return fmt.Errorf("invalid resize")
	}
	return e.Lifecycle.Validate()
}

// Decode is the bounded adapter used by sharing, scanning, and exports.
func Decode(rd io.Reader) (*Recording, error) {
	b, err := ReadBounded(rd, MaxBytes)
	if err != nil {
		return nil, err
	}
	return decodeArchive(b)
}

func Load(path string) (*Recording, error) {
	if strings.HasSuffix(path, ".partial") {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if st.Size() > MaxBytes {
			return nil, fmt.Errorf("this operation is limited to 64 MiB")
		}
		return Recover(f)
	}
	source, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	return source.Materialize(MaxBytes)
}

// Journals are append-only and synced for recovery after a crash. The first line
// contains metadata; incomplete final event lines are ignored during recovery
type journalHeader struct {
	Format    string    `json:"f"`
	Recording Recording `json:"r"`
}
type Writer struct {
	mu          sync.Mutex
	file        *os.File
	path        string
	bytes       int
	closed      bool
	interval    time.Duration
	stop        chan struct{}
	done        chan struct{}
	stopOnce    sync.Once
	syncErr     error
	asyncErrors chan error
}

func NewWriter(path string, r Recording) (*Writer, error) {
	return NewWriterWithInterval(path, r, 0)
}

func NewWriterWithInterval(path string, r Recording, interval time.Duration) (*Writer, error) {
	if interval < 0 {
		return nil, fmt.Errorf("sync interval must not be negative")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err == nil {
		return nil, fmt.Errorf("destination already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(path+".partial", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	w := &Writer{file: f, path: path, interval: interval, stop: make(chan struct{}), done: make(chan struct{}), asyncErrors: make(chan error, 1)}
	initial := r.Lines
	r.Lines = nil
	b, err := json.Marshal(journalHeader{JournalFormat, r})
	if err == nil {
		err = w.write(b)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	for _, event := range initial {
		data, err := json.Marshal(event)
		if err == nil {
			err = w.write(data)
		}
		if err != nil {
			f.Close()
			return nil, err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	go func() {
		defer close(w.done)
		if interval == 0 {
			<-w.stop
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-w.stop:
				return
			case <-ticker.C:
				w.mu.Lock()
				if w.syncErr == nil {
					w.syncErr = w.file.Sync()
					if w.syncErr != nil {
						w.asyncErrors <- w.syncErr
					}
				}
				w.mu.Unlock()
			}
		}
	}()
	return w, nil
}

// SyncErrors reports a deferred disk failure even while the terminal is idle
func (w *Writer) SyncErrors() <-chan error { return w.asyncErrors }

func (w *Writer) write(b []byte) error {
	if len(b) > MaxJournalLineBytes {
		return fmt.Errorf("journal entry exceeds %d byte limit", MaxJournalLineBytes)
	}
	if w.bytes+len(b)+1 > MaxExpandedBytes {
		return fmt.Errorf("recording reached 1 GiB limit; journal preserved")
	}
	n, err := w.file.Write(append(b, '\n'))
	w.bytes += n
	if err != nil {
		return err
	}
	if w.interval == 0 {
		return w.file.Sync()
	}
	return nil
}

func (w *Writer) Append(e Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return os.ErrClosed
	}
	if w.syncErr != nil {
		return w.syncErr
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return w.write(b)
}

func (w *Writer) Close() error {
	w.stopOnce.Do(func() { close(w.stop) })
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return w.syncErr
	}
	w.closed = true
	w.syncErr = errors.Join(w.syncErr, w.file.Sync(), w.file.Close())
	return w.syncErr
}

func (w *Writer) Finish() error {
	if err := w.Close(); err != nil {
		return err
	}
	journal, err := os.Open(w.path + ".partial")
	if err != nil {
		return err
	}
	defer journal.Close()
	f, err := os.CreateTemp(filepath.Dir(w.path), ".tbt-recording-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = finalizeJournal(f, journal); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// Link gives atomic no-clobber publication, including when another process
	// created the destination while the recording was running
	if err = os.Link(f.Name(), w.path); err != nil {
		return err
	}
	return os.Remove(w.path + ".partial")
}

func Recover(rd io.Reader) (*Recording, error) {
	reader, err := newJournalReader(rd)
	if err != nil {
		return nil, err
	}
	r := reader.header
	for {
		e, err := reader.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if reader.bytes > MaxBytes {
			return nil, fmt.Errorf("materialized recovery is limited to 64 MiB; use RecoverFile for large journals")
		}
		r.Lines = append(r.Lines, e)
	}
	return &r, r.Validate()
}
