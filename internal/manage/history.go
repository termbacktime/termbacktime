package manage

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"github.com/termbacktime/termbacktime/internal/history"
	"github.com/termbacktime/termbacktime/internal/termui"
)

type HistoryBackend interface{ HistoryStore() history.Store }

func (s *Store) HistoryStore() history.Store { return history.Store{Root: s.Library.Root} }

type historyView struct {
	state  history.State
	status string
	busy   bool
}
type historyLoaded struct {
	owner *historyView
	state history.State
	err   error
	text  string
}

func (m *model) openHistory() tea.Cmd {
	backend, ok := m.backend.(HistoryBackend)
	if !ok {
		m.status = "History settings unavailable"
		return nil
	}
	view := &historyView{busy: true, status: "Loading history settings…"}
	m.historySettings = view
	m.mode = "history"
	return func() tea.Msg {
		state, err := backend.HistoryStore().State()
		return historyLoaded{view, state, err, "History settings"}
	}
}
func (m *model) historyUpdate(msg tea.Msg) (bool, tea.Cmd) {
	if loaded, ok := msg.(historyLoaded); ok {
		if loaded.owner != m.historySettings {
			return true, nil
		}
		v := m.historySettings
		v.busy = false
		v.state = loaded.state
		v.status = loaded.text
		if loaded.err != nil {
			v.status = "History unavailable: " + loaded.err.Error()
		}
		return true, nil
	}
	if m.mode != "history" {
		return false, nil
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}
	v := m.historySettings
	switch key.String() {
	case "esc", "q", "H":
		m.mode = ""
		m.historySettings = nil
	case "ctrl+c":
		return true, tea.Quit
	case "e", "d", "c", "space":
		if v.busy {
			return true, nil
		}
		v.busy = true
		store := m.backend.(HistoryBackend).HistoryStore()
		enabled := !v.state.Enabled
		if key.String() == "e" {
			enabled = true
		}
		if key.String() == "d" {
			enabled = false
		}
		return true, func() tea.Msg {
			var err error
			text := "History settings saved"
			if key.String() == "c" {
				err = store.Clear()
				text = "Playback history cleared"
			} else {
				err = store.Enable(enabled)
			}
			state, readErr := store.State()
			if err == nil {
				err = readErr
			}
			return historyLoaded{v, state, err, text}
		}
	}
	return true, nil
}
func (m *model) historyScreen() tea.View {
	v := m.historySettings
	state := "Disabled"
	if v.state.Enabled {
		state = "Enabled"
	}
	content := fmt.Sprintf("Playback history: %s\n\n%d saved positions (maximum 100)\nOnly recording revision IDs, positions, and timestamps are stored.\nNo recording contents, filenames, URLs, or keys.\n\n%s\n\ne Enable · d Disable · c Clear history · Esc Back", state, len(v.state.Entries), v.status)
	view := tea.NewView(termui.Fit(pane("Playback history", wrapped(content, max(1, m.width-2)), m.width, max(3, m.height), true), m.width, m.height))
	view.AltScreen = true
	return view
}
