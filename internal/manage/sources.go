package manage

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/termbacktime/termbacktime/internal/sharing"
)

type Source int

const (
	LocalSource Source = iota
	GistSource
	RepoSource
)

type SourceBackend interface {
	ListSource(context.Context, Source, int) ([]Item, int, error)
}

func (s *Store) ListSource(ctx context.Context, source Source, page int) ([]Item, int, error) {
	if source != RepoSource {
		return s.List(ctx, source == GistSource, page)
	}
	if s.GitHub == nil {
		return nil, 0, fmt.Errorf("repository authentication required; run termbacktime auth --storage repo")
	}
	client, err := s.GitHub.Backend(ctx, sharing.Repo)
	if err != nil {
		return nil, 0, err
	}
	entries, next, err := client.ListRepository(ctx, page)
	if err != nil {
		return nil, 0, err
	}
	items := []Item{}
	for _, entry := range entries {
		items = append(items, Item{Gist: &entry})
	}
	return items, next, nil
}
func (m *model) changeSource(source Source) tea.Cmd {
	m.source = source
	m.sourceChosen = true
	m.mode = ""
	m.items = nil
	m.visible = nil
	m.selected = 0
	m.query = ""
	m.next = 0
	m.detailFocus = false
	m.selectItem(false)
	return m.refresh(false)
}
func (m *model) sourceUpdate(msg tea.Msg) (bool, tea.Cmd) {
	if m.mode != "source-choice" {
		return false, nil
	}
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = max(1, size.Width)
		m.height = max(1, size.Height)
		return true, nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "up", "k":
			m.sourceCursor = (m.sourceCursor + 2) % 3
		case "down", "j", "tab":
			m.sourceCursor = (m.sourceCursor + 1) % 3
		case "1", "2", "3":
			return true, m.changeSource(Source(key.String()[0] - '1'))
		case "enter":
			return true, m.changeSource(m.sourceCursor)
		case "esc":
			if m.sourceChosen {
				m.mode = ""
				return true, nil
			}
			return true, tea.Quit
		case "q", "ctrl+c":
			return true, tea.Quit
		}
		return true, nil
	}
	return false, nil
}
func (m *model) sourceView() tea.View {
	rows := []string{"TERMBACKTIME / Recording manager", "", "Which recordings would you like to manage?", ""}
	for i, name := range []string{"Local recordings - this computer", "GitHub Gists", "GitHub repository - TBT-Recordings"} {
		mark := "  "
		if Source(i) == m.sourceCursor {
			mark = "> "
		}
		rows = append(rows, fmt.Sprintf("%s%d  %s", mark, i+1, name))
	}
	rows = append(rows, "", "↑↓ Choose · Enter Open · 1/2/3 Select · Esc Back · q Quit")
	v := tea.NewView(strings.Join(rows, "\n"))
	v.AltScreen = true
	return v
}
