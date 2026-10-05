package manage

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagerStorageConfirmationFiltersPinsAndResize(t *testing.T) {
	lib := library.Library{Root: t.TempDir()}
	path, _ := lib.Output("")
	if err := recording.Save(path, &recording.Recording{Sizes: []int{80, 24}}); err != nil {
		t.Fatal(err)
	}
	entry, err := lib.Register(path)
	if err != nil {
		t.Fatal(err)
	}
	backend := &Store{Library: lib}
	m := newModel(t.Context(), backend, false, nil)
	m.update(m.Init()())
	for m.loading {
		m.update(m.listing.next()())
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	command := press(m, "P")
	if command == nil {
		t.Fatal("pin action missing")
	}
	m.Update(command())
	if p, _ := lib.Pinned(entry.ID); !p {
		t.Fatal("pin not saved")
	}
	export := filepath.Join(lib.Root, "exports", "session.html")
	os.MkdirAll(filepath.Dir(export), 0700)
	os.WriteFile(export, []byte("export"), 0600)
	command = press(m, "S")
	if command == nil {
		t.Fatal("storage action missing")
	}
	m.Update(command())
	if m.storage == nil || len(m.storage.items) != 1 {
		t.Fatal("pinned item eligible")
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.width != 80 || m.height != 24 {
		t.Fatal("storage swallowed resize")
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "Storage management") {
		t.Fatal("missing screen")
	}
	press(m, "A")
	press(m, "enter")
	if m.storage.editing != "delete" {
		t.Fatal("missing typed confirmation")
	}
	if cmd := press(m, "enter"); cmd != nil {
		t.Fatal("empty confirmation applied")
	}
	press(m, "esc")
	if _, err := os.Stat(export); err != nil {
		t.Fatal("cancel deleted")
	}
	press(m, "a")
	m.input.SetValue("5d")
	press(m, "enter")
	if len(m.storage.items) != 0 {
		t.Fatal("age filter ignored")
	}
	press(m, "a")
	m.input.SetValue("0s")
	press(m, "enter")
	press(m, "A")
	press(m, "enter")
	m.input.SetValue("delete")
	m.Update(press(m, "enter")())
	if _, err := os.Stat(export); !os.IsNotExist(err) {
		t.Fatal("confirmed cleanup failed", m.storage.status)
	}
	old := m.storage
	press(m, "esc")
	m.Update(storageLoaded{owner: old})
	if m.storage != nil {
		t.Fatal("stale storage message restored screen")
	}
}

func TestStorageScreenDoesNotSwallowLibraryRefresh(t *testing.T) {
	s, _, _ := testStore(t)
	m := newModel(t.Context(), s, false, nil)
	list := m.Init()
	storage := press(m, "S")
	m.update(storage())
	m.update(list())
	for m.loading {
		m.update(m.listing.next()())
	}
	if len(m.items) != 1 || m.storage == nil {
		t.Fatal("library refresh was lost behind storage screen")
	}
	m.update(m.selectItem(false)())
	if strings.Contains(m.details, "Loading recording metadata") {
		t.Fatal("metadata result was lost behind storage screen")
	}
}
