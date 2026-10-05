// Package review presents the optional publication metadata before a publication exists.
package review

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/termui"
)

var ErrCanceled = errors.New("upload canceled; nothing was published")

type Result struct {
	Storage                  string
	RememberStorage          bool
	Title, Description       string
	Fields                   recording.MetadataFields
	RememberTemplate         bool
	Public, Encrypted, Queue bool
}
type DoneMsg struct {
	Result Result
	Err    error
}
type Model struct {
	Storage           string
	StorageLocked     bool
	Managed           bool
	Findings          int
	width, height     int
	title             textinput.Model
	description       textarea.Model
	result            Result
	focus             int
	public, encrypted bool
	specs             string
}

func New(r *recording.Recording, fields recording.MetadataFields, public, encrypted bool) *Model {
	m := &Model{title: textinput.New(), description: textarea.New(), result: Result{Fields: fields}, public: public, encrypted: encrypted, width: 80, height: 30}
	m.title.CharLimit = 1024
	m.title.SetValue(r.Title)
	m.title.Focus()
	m.description.CharLimit = 16384
	m.description.SetWidth(70)
	m.description.SetHeight(3)
	m.description.ShowLineNumbers = false
	m.description.SetValue(r.Metadata.Description)
	for _, g := range []struct {
		name string
		s    *recording.SystemInfo
	}{{"Capture computer", r.Metadata.CaptureSystem}, {"Upload computer", r.Metadata.UploadSystem}} {
		if g.s != nil {
			s := g.s
			value := func(s string) string {
				if s == "" {
					return "unavailable"
				}
				return s
			}
			capacity := func(n uint64) string {
				if n == 0 {
					return "unavailable"
				}
				return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
			}
			cores := func(n int) string {
				if n == 0 {
					return "unavailable"
				}
				return fmt.Sprint(n)
			}
			m.specs += fmt.Sprintf("%s: %s · %s\nCPU: %s · %s physical / %s logical cores · RAM: %s · Disk: %s\n", g.name, value(strings.TrimSpace(s.OS+" "+s.OSVersion)), value(s.Arch), value(s.CPUModel), cores(s.PhysicalCores), cores(s.LogicalCores), capacity(s.RAMBytes), capacity(s.DiskTotalBytes))
		}
	}
	return m
}

type standalone struct {
	*Model
	result Result
	err    error
}

func (s *standalone) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if done, ok := msg.(DoneMsg); ok {
		s.result, s.err = done.Result, done.Err
		return s, tea.Quit
	}
	_, cmd := s.Model.Update(msg)
	return s, cmd
}
func Run(ctx context.Context, input io.Reader, output io.Writer, r *recording.Recording, fields recording.MetadataFields, public, encrypted bool, destination ...StorageOptions) (Result, error) {
	m := &standalone{Model: New(r, fields, public, encrypted), err: ErrCanceled}
	if len(destination) > 0 {
		m.Storage = destination[0].Storage
		m.StorageLocked = destination[0].Locked
	}
	_, err := termui.Run(ctx, tea.NewProgram(m, tea.WithInput(input), tea.WithOutput(output), tea.WithoutSignalHandler()))
	if err != nil {
		return Result{}, err
	}
	return m.result, m.err
}
func (m *Model) Init() tea.Cmd { return nil }
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	count, action := 8, 7
	if m.Managed {
		count, action = 11, 9
	}
	offset := 0
	if m.Storage != "" {
		offset = 1
		count++
		action++
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
		m.description.SetWidth(max(20, min(80, msg.Width-4)))
		m.title.SetWidth(max(20, msg.Width-4))
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, func() tea.Msg { return DoneMsg{Err: ErrCanceled} }
		case "tab", "shift+tab":
			m.title.Blur()
			m.description.Blur()
			d := 1
			if msg.String() == "shift+tab" {
				d = -1
			}
			m.focus = (m.focus + d + count) % count
			if m.focus == 0 {
				return m, m.title.Focus()
			}
			if m.focus == 1 {
				return m, m.description.Focus()
			}
			return m, nil
		case "ctrl+enter":
			m.focus = action
		case "enter":
			if m.focus == 0 {
				m.focus = 1
				m.title.Blur()
				return m, m.description.Focus()
			}
		}
		if (msg.String() == "space" || msg.String() == "enter" || msg.String() == "ctrl+enter") && m.focus >= 2 {
			switch m.focus {
			case 2:
				m.result.Fields.OS = !m.result.Fields.OS
			case 3:
				m.result.Fields.CPU = !m.result.Fields.CPU
			case 4:
				m.result.Fields.RAM = !m.result.Fields.RAM
			case 5:
				m.result.Fields.Disk = !m.result.Fields.Disk
			case 6:
				m.result.RememberTemplate = !m.result.RememberTemplate
			default:
				if offset == 1 && m.focus == 7 {
					if !m.StorageLocked {
						if m.Storage == "repo" {
							m.Storage = "gist"
						} else {
							m.Storage = "repo"
						}
					}
					return m, nil
				}
				if m.Managed && m.focus == 7+offset {
					if m.Storage == "repo" {
						return m, nil
					}
					m.public = !m.public
					return m, nil
				}
				if m.Managed && m.focus == 8+offset {
					m.encrypted = !m.encrypted
					return m, nil
				}
				m.result.Title = m.title.Value()
				m.result.Description = m.description.Value()
				m.result.Public, m.result.Encrypted = m.public, m.encrypted
				m.result.Queue = m.Managed && m.focus == 10+offset
				m.result.Storage = m.Storage
				m.result.RememberStorage = m.Storage != "" && !m.StorageLocked
				if m.Storage == "repo" {
					m.result.Public = true
				}
				result := m.result
				return m, func() tea.Msg { return DoneMsg{Result: result} }
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	if m.focus == 0 {
		m.title, cmd = m.title.Update(msg)
	} else if m.focus == 1 {
		m.description, cmd = m.description.Update(msg)
	}
	return m, cmd
}
func (m *Model) View() tea.View {
	visibility, encryption := "Secret", "unencrypted"
	if m.public || m.Storage == "repo" {
		visibility = "Public"
	}
	if m.encrypted {
		encryption = "encrypted"
	}
	header := fmt.Sprintf("Review upload · %s Gist · %s", visibility, encryption)
	if m.Storage == "repo" {
		header = fmt.Sprintf("Review upload · Public TBT-Recordings repository · %s", encryption)
	}
	rows := []string{"README.md is readable on GitHub, including with encryption.", "Only checked computer categories will be uploaded."}
	if m.Findings > 0 {
		rows = append(rows, fmt.Sprintf("Secret scan: %d finding(s). Esc to cancel; use scan/redact to review.", m.Findings))
	}
	focusLine := 0
	add := func(focus int, label, value string) {
		prefix := "  "
		if m.focus == focus {
			prefix = "> "
			focusLine = len(rows)
		}
		rows = append(rows, strings.Split(ansi.Hardwrap(prefix+label+value, max(1, m.width-2), true), "\n")...)
	}
	add(0, "Title\n", m.title.View())
	add(1, "Description\n", m.description.View())
	rows = append(rows, strings.Split(ansi.Hardwrap(ansi.Strip(m.specs), max(1, m.width-2), true), "\n")...)
	for i, row := range []struct {
		label string
		on    bool
	}{
		{"OS, version and architecture", m.result.Fields.OS}, {"CPU model and core counts", m.result.Fields.CPU}, {"Total RAM", m.result.Fields.RAM}, {"Recording filesystem capacity", m.result.Fields.Disk}, {"Reuse title and description as a template", m.result.RememberTemplate},
	} {
		mark := " "
		if row.on {
			mark = "x"
		}
		add(i+2, "["+mark+"] ", row.label)
	}
	offset := 0
	if m.Storage != "" {
		offset = 1
		label := "GitHub Gist"
		if m.Storage == "repo" {
			label = "GitHub repository - TBT-Recordings"
		}
		if m.StorageLocked {
			label += " (fixed for this upload)"
		} else {
			label += " (Space to change; remembered on confirmation)"
		}
		add(7, "Upload storage: ", label)
	}
	if m.Managed {
		add(7+offset, "Visibility: ", visibility+" (Space to change)")
		add(8+offset, "Encryption: ", encryption+" (Space to change)")
		add(9+offset, "[ Upload now ]", "")
		add(10+offset, "[ Add to queue ]", "")
	} else {
		add(7+offset, "[ Upload ]", "")
	}
	footer := "Tab: next · Shift+Tab: previous · Space: select · Esc: cancel"
	if !m.Managed {
		return tea.NewView("\n" + header + "\n\n" + strings.Join(rows, "\n") + "\n\n" + footer + "\n")
	}
	height := max(1, m.height-4)
	scrollOffset := min(max(0, focusLine-height+4), max(0, len(rows)-height))
	content := header + "\n" + strings.Repeat("─", m.width) + "\n" + termui.Fit(strings.Join(rows[scrollOffset:], "\n"), m.width, height) + "\n" + strings.Repeat("─", m.width) + "\n" + footer
	v := tea.NewView(termui.Fit(content, m.width, m.height))
	v.AltScreen = true
	return v
}

type StorageOptions struct {
	Storage string
	Locked  bool
}
