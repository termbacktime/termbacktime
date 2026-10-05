package history

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestHistoryOptInConcurrentWritesBoundsAndCompletion(t *testing.T) {
	store := Store{Root: t.TempDir()}
	id := fmt.Sprintf("%064x", 1)
	if err := store.Save(id, 100, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.dir()); !os.IsNotExist(err) {
		t.Fatal("disabled history wrote files")
	}
	if err := store.Enable(true); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for n := 0; n < 120; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.Save(fmt.Sprintf("%064x", n), int64(n+1), 1000); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	state, err := store.State()
	if err != nil || len(state.Entries) != 100 {
		t.Fatal(len(state.Entries), err)
	}
	if err := store.Save(id, 500, 1000); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(id, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	state, _ = store.State()
	if _, ok := state.Entries[id]; ok {
		t.Fatal("completed recording retained position")
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	state, _ = store.State()
	if !state.Enabled || len(state.Entries) != 0 {
		t.Fatal(state)
	}
	if err := store.Enable(false); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(id, 50, 1000); err != nil {
		t.Fatal(err)
	}
	state, _ = store.State()
	if len(state.Entries) != 0 {
		t.Fatal("disabled history saved position")
	}
	info, err := os.Stat(filepath.Join(store.dir(), "state.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
}

func TestHistoryRefusesSymlinkDirectoryAndFile(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "state.json"), []byte(`{"version":1,"enabled":true,"entries":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	store := Store{Root: t.TempDir()}
	if err := os.Symlink(outside, store.dir()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.State(); err == nil {
		t.Fatal("followed history directory symlink")
	}
	if err := store.Enable(true); err == nil {
		t.Fatal("wrote through history directory symlink")
	}
	if err := os.Remove(store.dir()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(store.dir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "state.json"), filepath.Join(store.dir(), "state.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.State(); err == nil {
		t.Fatal("followed history file symlink")
	}
}
