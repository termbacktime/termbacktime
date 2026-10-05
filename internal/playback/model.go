// Package playback renders recordings in a virtual terminal. Recorded terminal
// modes and resize requests never control the user's terminal.
package playback

import (
	"context"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/termbacktime/termbacktime/internal/history"
	"github.com/termbacktime/termbacktime/internal/live"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/termui"
)

type DoneMsg struct {
	Model *Model
	Err   error
}
type tickMsg struct {
	model *Model
	now   time.Time
}

type Model struct {
	historyStore                                *history.Store
	historySaved                                time.Time
	historyNotice                               string
	historyReady, interactiveInit, resumeChoice bool
	initialStart, resumePosition                float64
	restoration                                 *restoration
	restoreProgress                             func(int)
	Managed                                     bool
	initialErr                                  error
	ctx                                         context.Context
	recording                                   *recording.Recording
	screen                                      *live.Screen
	original, presented                         []int64
	position, speed, loopStart, loopEnd         float64
	index, width, height                        int
	playing, loop, closed                       bool
	previous                                    time.Time
}

func New(ctx context.Context, r *recording.Recording, speed float64, idle time.Duration, start float64) *Model {
	a, b := recording.Times(r, idle.Milliseconds())
	m := &Model{ctx: ctx, recording: r, original: a, presented: b, speed: speed, loopEnd: float64(a[len(a)-1]), playing: true, width: 80, height: 24}
	m.initialErr = m.seek(start, r.Count())
	return m
}
func (m *Model) Close() {
	if !m.closed {
		m.saveHistory(true)
		if m.restoration != nil {
			m.restoration.cancel()
			<-m.restoration.done
			if m.restoration.screen != nil {
				m.restoration.screen.Close()
			}
			m.restoration = nil
		}
		m.closed = true
		if m.screen != nil {
			m.screen.Close()
		}
		_ = m.recording.Close()
	}
}
func (m *Model) tick() tea.Cmd {
	return tea.Tick(time.Second/30, func(t time.Time) tea.Msg { return tickMsg{m, t} })
}
func (m *Model) Init() tea.Cmd {
	if m.interactiveInit {
		m.interactiveInit = false
		if m.resumeChoice {
			return nil
		}
		return m.restore(m.initialStart)
	}
	if m.initialErr != nil {
		return m.finish(m.initialErr)
	}
	m.previous = time.Now()
	return m.tick()
}
func (m *Model) finish(err error) tea.Cmd {
	m.Close()
	return func() tea.Msg { return DoneMsg{m, err} }
}
func (m *Model) apply() error {
	for m.index < m.recording.Count() && float64(m.original[m.index+1]) <= m.position {
		event, err := m.recording.Event(m.index)
		if err != nil {
			return err
		}
		if err := m.applyEvent(event); err != nil {
			return err
		}
		m.index++
	}
	return nil
}
func (m *Model) applyEvent(e recording.Event) error {
	if err := m.ctx.Err(); err != nil {
		return err
	}
	if e.Command == "s" {
		m.screen.Resize(e.Sizes[0], e.Sizes[1])
		return nil
	}
	return m.screen.Write(strings.Join(e.Lines, ""))
}
func (m *Model) seek(target float64, limit int) error {
	m.position = math.Max(0, math.Min(m.loopEndDuration(), target))
	if m.screen != nil {
		m.screen.Close()
	}
	m.screen = live.NewScreen(m.recording.Sizes[0], m.recording.Sizes[1])
	m.index = 0
	for m.index < limit && float64(m.original[m.index+1]) <= m.position {
		event, err := m.recording.Event(m.index)
		if err != nil {
			return err
		}
		if err := m.applyEvent(event); err != nil {
			return err
		}
		m.index++
		if m.restoreProgress != nil {
			m.restoreProgress(m.index)
		}
	}
	return nil
}
func (m *Model) loopEndDuration() float64 { return float64(m.original[len(m.original)-1]) }
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.closed {
		return m, nil
	}
	if handled, cmd := m.historyUpdate(msg); handled {
		return m, cmd
	}
	var err error
	beforePosition, beforePlaying := m.position, m.playing
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
		return m, tea.ClearScreen
	case tickMsg:
		if msg.model != m {
			return m, nil
		}
		delta := float64(msg.now.Sub(m.previous)) / float64(time.Millisecond)
		m.previous = msg.now
		if m.playing {
			p := recording.MapTime(m.position, m.original, m.presented) + delta*m.speed
			m.position = recording.MapTime(p, m.presented, m.original)
			if m.loop {
				m.position = math.Min(m.position, m.loopEnd)
			}
			err = m.apply()
			if err == nil && m.loop && m.position >= m.loopEnd {
				err = m.seek(m.loopStart, m.recording.Count())
			} else if err == nil && m.position >= m.loopEndDuration() {
				return m, m.finish(nil)
			}
		}
		if err != nil {
			return m, m.finish(err)
		}
		m.saveHistory(false)
		return m, m.tick()
	case tea.KeyPressMsg:
		m.previous = time.Now()
		switch strings.ToLower(msg.String()) {
		case "q", "esc", "ctrl+c":
			return m, m.finish(nil)
		case "space":
			m.playing = !m.playing
		case "left":
			err = m.seek(m.position-5000, m.recording.Count())
		case "right":
			err = m.seek(m.position+5000, m.recording.Count())
		case ",", ".":
			m.playing = false
			next := max(0, m.index-1)
			if msg.String() == "." {
				next = min(m.recording.Count(), m.index+1)
			}
			err = m.seek(float64(m.original[next]), next)
		case "+", "=":
			m.speed = math.Min(100, m.speed*2)
		case "-":
			m.speed = math.Max(0.25, m.speed/2)
		case "i":
			m.loopStart = m.position
		case "o":
			m.loopEnd = m.position
		case "l":
			if m.loopEnd > m.loopStart {
				m.loop = !m.loop
			}
		}
	}
	if err != nil {
		return m, m.finish(err)
	}
	m.saveHistory(beforePosition != m.position || beforePlaying && !m.playing)
	return m, nil
}
func (m *Model) View() tea.View {
	if view, ok := m.historyView(); ok {
		return view
	}
	frame, _ := m.screen.Frame(true)
	state := "Playing"
	if !m.playing {
		state = "Paused"
	}
	loop := ""
	if m.loop {
		loop = fmt.Sprintf(" · Loop %.1f–%.1fs", m.loopStart/1000, m.loopEnd/1000)
	}
	title := strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, ansi.Strip(m.recording.Title))
	header := fmt.Sprintf(" %s · %s · %.1f / %.1fs · %gx%s", title, state, m.position/1000, m.loopEndDuration()/1000, m.speed, loop+" "+m.historyNotice)
	body := termui.Fit(frame.Screen, m.width, max(1, m.height-4))
	exit := "Quit"
	if m.Managed {
		exit = "Back"
	}
	footer := " Space Pause · ←/→ 5s · ,/. Step · +/- Speed · I/O Bounds · L Loop · Q " + exit
	view := tea.NewView(termui.Fit(header+"\n"+strings.Repeat("─", m.width)+"\n"+body+"\n"+strings.Repeat("─", m.width)+"\n"+footer, m.width, m.height))
	view.AltScreen = true
	if frame.Visible && frame.Cursor[0] < m.width && frame.Cursor[1] < m.height-4 {
		view.Cursor = tea.NewCursor(frame.Cursor[0], frame.Cursor[1]+2)
	}
	return view
}

type standalone struct {
	*Model
	err error
}

func (s *standalone) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if done, ok := msg.(DoneMsg); ok && done.Model == s.Model {
		s.err = done.Err
		return s, tea.Quit
	}
	_, cmd := s.Model.Update(msg)
	return s, cmd
}
func Run(ctx context.Context, input io.Reader, output io.Writer, r *recording.Recording, speed float64, idle time.Duration, start float64) error {
	return RunWithHistory(ctx, input, output, r, speed, idle, start, HistoryOptions{})
}
func RunWithHistory(ctx context.Context, input io.Reader, output io.Writer, r *recording.Recording, speed float64, idle time.Duration, start float64, options HistoryOptions) error {
	m := &standalone{Model: NewInteractive(ctx, r, speed, idle, start, options)}
	defer m.Close()
	_, err := termui.Run(ctx, tea.NewProgram(m, tea.WithInput(input), tea.WithOutput(output), tea.WithoutSignalHandler()))
	if err != nil {
		return err
	}
	return m.err
}
