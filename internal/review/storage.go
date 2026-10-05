package review

import (
	"context"
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/termbacktime/termbacktime/internal/termui"
)

type storageChoice struct {
	value     string
	confirmed bool
}

func (m *storageChoice) Init() tea.Cmd { return nil }
func (m *storageChoice) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "up", "down", "j", "k", "tab", "space":
			if m.value == "repo" {
				m.value = "gist"
			} else {
				m.value = "repo"
			}
		case "enter":
			m.confirmed = true
			return m, tea.Quit
		case "esc", "q", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}
func (m *storageChoice) View() tea.View {
	repo, gist := "  ", "  "
	if m.value == "repo" {
		repo = "> "
	} else {
		gist = "> "
	}
	return tea.NewView("Where should this recording be uploaded?\n\n" + repo + "GitHub repository - TBT-Recordings (public)\n" + gist + "GitHub Gist\n\n↑↓ Choose · Enter Continue · Esc Cancel\n")
}
func ChooseStorage(ctx context.Context, input io.Reader, output io.Writer, initial string) (string, error) {
	m := &storageChoice{value: initial}
	_, err := termui.Run(ctx, tea.NewProgram(m, tea.WithInput(input), tea.WithOutput(output), tea.WithoutSignalHandler()))
	if err != nil {
		return "", err
	}
	if !m.confirmed {
		return "", ErrCanceled
	}
	return m.value, nil
}
