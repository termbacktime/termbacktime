package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/termbacktime/termbacktime/internal/sharing"
	"os"
	"path/filepath"
	"strings"

	"github.com/termbacktime/termbacktime/internal/recording"
	"golang.org/x/sys/unix"
)

// Delete removes exactly the indexed file the user selected. Shares are retained:
// they can be the only remaining copy of a remote recording's decryption key.
func (l Library) Delete(e Entry, expected os.FileInfo) error {
	unlock, err := l.MutationLock()
	if err != nil {
		return err
	}
	defer unlock()
	return l.DeleteLocked(e, expected)
}

// DeleteLocked requires MutationLock, including for cleanup previews.
func (l Library) DeleteLocked(e Entry, expected os.FileInfo) error {
	if pinned, err := l.Pinned(e.ID); err != nil {
		return err
	} else if pinned {
		return fmt.Errorf("recording is pinned; unpin it before deleting")
	}
	if !recording.ValidID(e.ID) || !filepath.IsAbs(e.Path) || pathID(e.Path) != e.ID {
		return fmt.Errorf("invalid library entry")
	}
	index := filepath.Join(l.Root, "library", e.ID+".json")
	b, err := os.ReadFile(index)
	if err != nil {
		return err
	}
	var current Entry
	if len(b) > 1<<20 || json.Unmarshal(b, &current) != nil || current.ID != e.ID || current.Path != e.Path {
		return fmt.Errorf("library entry changed; refresh before deleting")
	}
	path := e.Path
	if e.Status == "recording" {
		return fmt.Errorf("cannot delete an active recording")
	}
	if e.Status == "partial" {
		path += ".partial"
	}
	if e.Status == "invalid" || e.Status == "unsupported" {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			path += ".partial"
		}
	}
	other := e.Path + ".partial"
	if path == other {
		other = e.Path
	}
	if _, err := os.Lstat(other); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("recording state changed or another journal exists; refresh before deleting")
	}
	if e.Status == "missing" {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("recording reappeared; refresh before deleting")
		}
		return os.Remove(index)
	}
	// O_NOFOLLOW avoids deleting through a replaced symlink; the advisory lock
	// prevents deleting a journal while capture or recovery has it open.
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("recording is in use; try again after capture or recovery ends")
	}
	st, err := f.Stat()
	if err != nil {
		return err
	}
	actual, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || !os.SameFile(st, actual) {
		return fmt.Errorf("recording changed; refresh before deleting")
	}
	if expected == nil || !os.SameFile(st, expected) || st.Size() != expected.Size() || !st.ModTime().Equal(expected.ModTime()) {
		return fmt.Errorf("recording changed since listing; refresh before deleting")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if err := os.Remove(index); err != nil {
		return fmt.Errorf("file deleted, but could not remove its library entry: %w", err)
	}
	return nil
}

// GistShareLink finds a saved capability only for an explicit recording action.
// Callers must never include this value in metadata, errors, or list rendering.
func (l Library) GistShareLink(id string) (string, error) {
	target, err := sharing.Parse(id)
	if err != nil {
		return "", err
	}
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
		info, err := f.Info()
		if err != nil || info.Size() > 16384 {
			continue
		}
		b, err := os.ReadFile(filepath.Join(l.Root, "shares", f.Name()))
		if err != nil {
			return "", err
		}
		var r Receipt
		if json.Unmarshal(b, &r) != nil {
			continue
		}
		ref, err := sharing.Parse(r.Link)
		if err == nil && ref.String() == target.String() && r.Created > latest.Created {
			latest = r
		}
	}
	return latest.Link, nil
}
