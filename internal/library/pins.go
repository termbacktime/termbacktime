package library

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/termbacktime/termbacktime/internal/config"
	"github.com/termbacktime/termbacktime/internal/recording"
	"golang.org/x/sys/unix"
)

// MutationLock serializes pin changes with deletion. It never locks capture/playback.
func (l Library) MutationLock() (func(), error) {
	if err := config.EnsurePrivateDir(l.Root); err != nil {
		return nil, err
	}
	fd, err := unix.Open(filepath.Join(l.Root, "library.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "library.lock")
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("invalid library lock")
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("library mutation is already in progress")
	}
	return func() { unix.Flock(fd, unix.LOCK_UN); f.Close() }, nil
}
func (l Library) Pinned(id string) (bool, error) {
	if !recording.ValidID(id) {
		return false, fmt.Errorf("invalid recording ID")
	}
	dir := filepath.Join(l.Root, "pins")
	st, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("invalid pins directory")
	}
	_, err = os.Lstat(filepath.Join(dir, id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
func (l Library) SetPinned(id string, pin bool) error {
	if !recording.ValidID(id) {
		return fmt.Errorf("invalid recording ID")
	}
	unlock, err := l.MutationLock()
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := l.readEntry(id); err != nil {
		return err
	}
	existing, err := l.Pinned(id)
	if err != nil {
		return err
	}
	if existing == pin {
		return nil
	}
	dir := filepath.Join(l.Root, "pins")
	if err := config.EnsurePrivateDir(dir); err != nil {
		return err
	}
	path := filepath.Join(dir, id+".json")
	if !pin {
		return os.Remove(path)
	}
	return recording.Publish(path, func(w io.Writer) error { _, err := io.WriteString(w, "{\"pinned\":true}\n"); return err })
}
