package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMalformedHistoryCanBeClearedButCannotBeExtended(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, test := range []struct {
		name, data string
	}{
		{"malformed", "{"},
		{"version", `{"version":2}`},
		{"invalid id", `{"version":1,"entries":{"private-filename":{"position_ms":1}}}`},
		{"negative time", `{"version":1,"entries":{"` + id + `":{"position_ms":-1}}}`},
		{"over duration bound", `{"version":1,"entries":{"` + id + `":{"position_ms":604800001}}}`},
		{"negative update", `{"version":1,"entries":{"` + id + `":{"updated_ms":-1}}}`},
		{"oversized", strings.Repeat(" ", 65537)},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := Store{Root: t.TempDir()}
			if err := os.Mkdir(store.dir(), 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.dir(), "state.json")
			if err := os.WriteFile(path, []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.State(); err == nil {
				t.Fatal("accepted invalid history")
			}
			if err := store.Enable(true); err == nil {
				t.Fatal("silently replaced corrupt history")
			}
			data, _ := os.ReadFile(path)
			if string(data) != test.data {
				t.Fatal("failed edit changed history")
			}
			if err := store.Clear(); err != nil {
				t.Fatal(err)
			}
			state, err := store.State()
			if err != nil || len(state.Entries) != 0 || state.Enabled {
				t.Fatal(state, err)
			}
		})
	}
}

func TestHistoryEntryBoundAndInvalidRevisions(t *testing.T) {
	store := Store{Root: t.TempDir()}
	if err := store.Enable(true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", strings.Repeat("x", 64), strings.Repeat("a", 63)} {
		if err := store.Save(id, 1, 100); err != nil {
			t.Fatal(err)
		}
	}
	state, _ := store.State()
	if len(state.Entries) != 0 {
		t.Fatal("saved invalid revision", state)
	}
	// Read-time limits reject an externally edited envelope, rather than
	// silently selecting which user's playback positions to retain.
	for n := range 101 {
		id := fmt.Sprintf("%064x", n)
		state.Entries[id] = Position{Position: 1}
	}
	data, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(store.dir(), "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.State(); err == nil {
		t.Fatal("accepted over 100 entries")
	}
}
