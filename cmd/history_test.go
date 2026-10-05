package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/termbacktime/termbacktime/internal/history"
)

func TestHistoryCommandsPreservePositionsUntilExplicitClear(t *testing.T) {
	h := newCommandHarness(t)
	store := history.Store{Root: h.data}
	id := strings.Repeat("a", 64)
	for _, action := range []string{"enable", "disable", "enable", "clear"} {
		out, diagnostic, err := h.run(t.Context(), "history", action)
		if err != nil || out != "Playback history: "+action+"\n" || diagnostic != "" {
			t.Fatal(out, diagnostic, err)
		}
		state, err := store.State()
		if err != nil {
			t.Fatal(err)
		}
		if action == "disable" && (state.Enabled || state.Entries[id].Position != 1200) {
			t.Fatal(state)
		}
		if action == "enable" {
			if !state.Enabled {
				t.Fatal(state)
			}
			if err := store.Save(id, 1200, 10000); err != nil {
				t.Fatal(err)
			}
		}
		if action == "clear" && (!state.Enabled || len(state.Entries) != 0) {
			t.Fatal(state)
		}
	}
}

func TestHistoryCommandFailuresDoNotPrintSuccessOrReplaceCorruption(t *testing.T) {
	h := newCommandHarness(t)
	path := filepath.Join(h.data, "history", "state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("broken history"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"enable", "disable"} {
		out, _, err := h.run(t.Context(), "history", action)
		if err == nil || out != "" {
			t.Fatal(out, err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "broken history" {
			t.Fatal(string(data), err)
		}
	}
	if _, _, err := h.run(t.Context(), "history", "clear"); err != nil {
		t.Fatal(err)
	}
	state, err := (history.Store{Root: h.data}).State()
	if err != nil || len(state.Entries) != 0 {
		t.Fatal(state, err)
	}
}
