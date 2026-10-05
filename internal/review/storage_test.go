package review

import (
	tea "charm.land/bubbletea/v2"
	"github.com/termbacktime/termbacktime/internal/recording"
	"testing"
)

func TestStorageChoiceRememberedOnlyOnConfirmationAndRespectsOverride(t *testing.T) {
	for _, locked := range []bool{false, true} {
		m := New(&recording.Recording{Title: "Title", Metadata: &recording.Metadata{Version: 1}}, recording.AllMetadataFields(), false, false)
		m.Storage = "repo"
		m.StorageLocked = locked
		m.Managed = true
		m.focus = 7
		m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
		want := "gist"
		if locked {
			want = "repo"
		}
		if m.Storage != want {
			t.Fatal(m.Storage)
		}
		m.focus = 11 // Add to queue, with the destination row present.
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("missing confirmation")
		}
		done := cmd().(DoneMsg)
		if done.Result.Storage != want || done.Result.RememberStorage == locked || !done.Result.Queue || (locked && !done.Result.Public) {
			t.Fatal(done)
		}
		_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		canceled := cmd().(DoneMsg)
		if canceled.Err != ErrCanceled || canceled.Result.RememberStorage {
			t.Fatal(canceled)
		}
	}
}
