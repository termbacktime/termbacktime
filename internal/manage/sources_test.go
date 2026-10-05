package manage

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"testing"
)

type sourceTestStore struct {
	fakeStore
	selected []Source
}

func (s *sourceTestStore) ListSource(ctx context.Context, source Source, page int) ([]Item, int, error) {
	s.selected = append(s.selected, source)
	return nil, 0, nil
}
func TestSourceChooserDefersRemoteWorkAndReturnsToCurrentSource(t *testing.T) {
	backend := &sourceTestStore{}
	m := newModel(t.Context(), backend, false, nil)
	m.mode = "source-choice"
	m.sourceChosen = false
	if cmd := m.Init(); cmd != nil || len(backend.selected) != 0 {
		t.Fatal("loaded before selection")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: '3', Text: "3"})
	if cmd == nil {
		t.Fatal("no selection command")
	}
	// sourceUpdate gives the underlying command before Update wraps screen redraws.
	m.mode = "source-choice"
	_, cmd = m.sourceUpdate(tea.KeyPressMsg{Code: '3', Text: "3"})
	msg := cmd()
	m.Update(msg)
	if len(backend.selected) != 1 || backend.selected[0] != RepoSource {
		t.Fatal(backend.selected)
	}
	m.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	if m.mode != "source-choice" || m.sourceCursor != RepoSource {
		t.Fatal(m.mode)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.mode != "" || m.source != RepoSource {
		t.Fatal("escape lost source")
	}
}
