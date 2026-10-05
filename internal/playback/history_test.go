package playback

import (
	tea "charm.land/bubbletea/v2"
	"github.com/termbacktime/termbacktime/internal/history"
	"github.com/termbacktime/termbacktime/internal/recording"
	"strings"
	"testing"
)

func TestResumeChoiceExplicitZeroAndPromptCancellation(t *testing.T) {
	store := history.Store{Root: t.TempDir()}
	if err := store.Enable(true); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 64)
	if err := store.Save(id, 500, 1000); err != nil {
		t.Fatal(err)
	}
	r := &recording.Recording{HistoryID: id, Sizes: []int{80, 24}, Lines: []recording.Event{{Time: 1000, Lines: []string{"end"}}}}
	m := NewInteractive(t.Context(), r, 1, 0, 0, HistoryOptions{Store: &store})
	if !m.resumeChoice {
		t.Fatal("saved position did not prompt")
	}
	m.Close()
	state, _ := store.State()
	if state.Entries[id].Position != 500 {
		t.Fatal("closing prompt discarded previous progress")
	}
	m = NewInteractive(t.Context(), r, 1, 0, 0, HistoryOptions{Store: &store, ExplicitStart: true})
	if m.resumeChoice {
		t.Fatal("explicit zero did not take precedence")
	}
	m.Close()
	m = NewInteractive(t.Context(), r, 1, 0, 0, HistoryOptions{Store: &store})
	defer m.Close()
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's'})
	commands := cmd().(tea.BatchMsg)
	result := commands[0]()
	m.Update(result)
	if m.position != 0 || m.resumeChoice || m.restoration != nil {
		t.Fatal("start over failed")
	}
	state, _ = store.State()
	if _, ok := state.Entries[id]; ok {
		t.Fatal("start over did not clear progress")
	}
}
