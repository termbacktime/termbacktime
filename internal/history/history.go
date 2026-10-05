// Package history stores only opaque recording revisions and playback positions.
package history

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/termbacktime/termbacktime/internal/config"
	"github.com/termbacktime/termbacktime/internal/recording"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Position struct {
	Position int64 `json:"position_ms"`
	Updated  int64 `json:"updated_ms"`
}
type State struct {
	Version int                 `json:"version"`
	Enabled bool                `json:"enabled"`
	Entries map[string]Position `json:"entries"`
}
type Store struct{ Root string }

func validID(id string) bool {
	if len(id) != 64 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
func (s Store) dir() string { return filepath.Join(s.Root, "history") }
func (s Store) State() (State, error) {
	state := State{Version: 1, Entries: map[string]Position{}}
	dir, err := unix.Open(s.dir(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, "state.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	file := os.NewFile(uintptr(fd), "history")
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return state, err
	}
	if !info.Mode().IsRegular() || info.Size() > 65536 {
		return state, fmt.Errorf("invalid playback history")
	}
	data, err := recording.ReadBounded(file, 65536)
	if err != nil {
		return state, err
	}
	if json.Unmarshal(data, &state) != nil || state.Version != 1 || len(state.Entries) > 100 {
		return State{}, fmt.Errorf("invalid playback history")
	}
	if state.Entries == nil {
		state.Entries = map[string]Position{}
	}
	for id, p := range state.Entries {
		if !validID(id) || p.Position < 0 || p.Position > 604800000 || p.Updated < 0 {
			return State{}, fmt.Errorf("invalid playback history")
		}
	}
	return state, nil
}
func (s Store) change(clear bool, edit func(*State)) error {
	if err := config.EnsurePrivateDir(s.dir()); err != nil {
		return err
	}
	fd, err := unix.Open(filepath.Join(s.dir(), "lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return err
	}
	lock := os.NewFile(uintptr(fd), "history lock")
	defer lock.Close()
	st, err := lock.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("invalid history lock")
	}
	if err = unix.Flock(fd, unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	state, err := s.State()
	if err != nil {
		if !clear {
			return err
		}
		state = State{Version: 1, Entries: map[string]Position{}}
	}
	edit(&state)
	ids := make([]string, 0, len(state.Entries))
	for id := range state.Entries {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := state.Entries[ids[i]], state.Entries[ids[j]]
		if a.Updated == b.Updated {
			return ids[i] < ids[j]
		}
		return a.Updated > b.Updated
	})
	for _, id := range ids[min(100, len(ids)):] {
		delete(state.Entries, id)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(s.dir(), ".history-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err = file.Write(data); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(s.dir(), "state.json"))
}
func (s Store) Enable(enabled bool) error {
	return s.change(false, func(state *State) { state.Enabled = enabled })
}
func (s Store) Clear() error {
	return s.change(true, func(state *State) { state.Entries = map[string]Position{} })
}
func (s Store) Save(id string, position, duration int64) error {
	if !validID(id) {
		return nil
	}
	state, err := s.State()
	if err != nil {
		return err
	}
	if !state.Enabled {
		return nil
	}
	return s.change(false, func(state *State) {
		if !state.Enabled {
			return
		}
		if position <= 0 || position >= duration {
			delete(state.Entries, id)
		} else {
			state.Entries[id] = Position{position, time.Now().UnixMilli()}
		}
	})
}
