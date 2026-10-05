// Package dashboard keeps presentation, links and controls outside the child PTY.
package dashboard

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/termbacktime/termbacktime/internal/live"
	"github.com/termbacktime/termbacktime/internal/terminal"
	"github.com/termbacktime/termbacktime/internal/termui"
	"golang.org/x/term"
)

type Mode uint8

const (
	RecordingMode Mode = iota
	LiveMode
)

func (m Mode) chromeRows() int {
	if m == LiveMode {
		return 9
	}
	return 5
}

func Check(in, out *os.File) error {
	if !term.IsTerminal(int(in.Fd())) || !term.IsTerminal(int(out.Fd())) {
		return fmt.Errorf("--dashboard requires interactive terminal input and output")
	}
	return nil
}

type State struct {
	Path, Status, Viewer, Host, Room string
	Diagnostics                      live.Diagnostics
}
type Dashboard struct {
	mu         sync.Mutex
	state      State
	mode       Mode
	program    *tea.Program
	ready      chan struct{}
	done       chan struct{}
	once       sync.Once
	cols, rows int
	open       func(string) error
}

func New(out *os.File, path string, mode Mode, open func(string) error) *Dashboard {
	c, r := terminal.Size(out)
	return &Dashboard{state: State{Path: path, Status: "Starting"}, mode: mode, ready: make(chan struct{}), done: make(chan struct{}), cols: c, rows: max(1, r-mode.chromeRows()), open: open}
}
func (d *Dashboard) Geometry() (int, int)    { return d.cols, d.rows }
func (d *Dashboard) Set(update func(*State)) { d.mu.Lock(); defer d.mu.Unlock(); update(&d.state) }
func (d *Dashboard) snapshot() State         { d.mu.Lock(); defer d.mu.Unlock(); return d.state }

type outputMsg string
type finishedMsg struct{}
type tickMsg time.Time
type actionMsg string

func tick() tea.Cmd { return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }) }
func (d *Dashboard) Write(b []byte) (int, error) {
	select {
	case <-d.ready:
	case <-d.done:
		return 0, io.ErrClosedPipe
	}
	select {
	case <-d.done:
		return 0, io.ErrClosedPipe
	default:
	}
	d.program.Send(outputMsg(string(b)))
	return len(b), nil
}
func (d *Dashboard) Close() error {
	select {
	case <-d.ready:
		d.program.Send(finishedMsg{})
	case <-d.done:
	}
	return nil
}
func (d *Dashboard) Run(ctx context.Context, in, out *os.File, write func([]byte) error, resize func(int, int) error) error {
	defer d.once.Do(func() { close(d.done) })
	em := vt.NewEmulator(d.cols, d.rows)
	em.SetScrollbackSize(0)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		b := make([]byte, 4096)
		for {
			n, err := em.Read(b)
			if n > 0 {
				_ = write(b[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() { _ = em.InputPipe().(io.Closer).Close(); <-drained; _ = em.Close() }()
	m := &model{owner: d, screen: em, resize: resize, started: time.Now(), cols: d.cols, rows: d.rows, cursorVisible: true}
	em.SetCallbacks(vt.Callbacks{CursorVisibility: func(visible bool) { m.cursorVisible = visible }})
	d.program = tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out), tea.WithFPS(30), tea.WithoutSignalHandler())
	close(d.ready)
	_, err := termui.Run(ctx, d.program)
	if err != nil {
		return err
	}
	return m.failure
}

type model struct {
	owner         *Dashboard
	screen        *vt.Emulator
	resize        func(int, int) error
	started       time.Time
	cols, rows    int
	width, height int
	controls      bool
	cursorVisible bool
	notice        string
	failure       error
}

func (m *model) Init() tea.Cmd { return tick() }
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case finishedMsg:
		return m, tea.Quit
	case tickMsg:
		return m, tick()
	case actionMsg:
		m.notice = string(msg)
	case outputMsg:
		_, _ = m.screen.WriteString(string(msg))
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
		c, r := min(max(1, msg.Width), 500), max(1, min(msg.Height, 200)-m.owner.mode.chromeRows())
		if c != m.cols || r != m.rows {
			if err := m.resize(c, r); err != nil {
				m.failure = err
				return m, tea.Quit
			}
			m.cols, m.rows = c, r
			m.screen.Resize(c, r)
		}
		return m, tea.ClearScreen
	case tea.PasteMsg:
		if !m.controls {
			m.screen.Paste(msg.Content)
		}
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+]" {
			m.controls = !m.controls
			return m, nil
		}
		if m.controls {
			switch key {
			case "esc", "enter":
				m.controls = false
			case "]":
				m.screen.SendText("\x1d")
				m.controls = false
			case "v", "h", "c", "a":
				if m.owner.mode != LiveMode {
					return m, nil
				}
				s := m.owner.snapshot()
				link := s.Viewer
				if key == "h" || key == "a" {
					link = s.Host
				}
				if link == "" {
					m.notice = "Link unavailable"
					return m, nil
				}
				return m, func() tea.Msg {
					var err error
					if key == "c" || key == "a" {
						err = clipboard.WriteAll(link)
					} else if m.owner.open != nil {
						err = m.owner.open(link)
					}
					if err != nil {
						return actionMsg("Link action failed")
					}
					return actionMsg("Link action complete")
				}
			}
			return m, nil
		}
		m.screen.SendKey(uv.KeyPressEvent(msg))
	case tea.MouseMsg:
		if m.controls {
			return m, nil
		}
		mouse := msg.Mouse()
		mouse.Y -= 2
		if mouse.X < 0 || mouse.X >= m.cols || mouse.Y < 0 || mouse.Y >= m.rows {
			return m, nil
		}
		switch msg.(type) {
		case tea.MouseClickMsg:
			m.screen.SendMouse(uv.MouseClickEvent(mouse))
		case tea.MouseReleaseMsg:
			m.screen.SendMouse(uv.MouseReleaseEvent(mouse))
		case tea.MouseMotionMsg:
			m.screen.SendMouse(uv.MouseMotionEvent(mouse))
		case tea.MouseWheelMsg:
			m.screen.SendMouse(uv.MouseWheelEvent(mouse))
		}
	}
	return m, nil
}
func (m *model) View() tea.View {
	s := m.owner.snapshot()
	width, height := m.width, m.height
	if width == 0 {
		width = m.cols
	}
	if height == 0 {
		height = m.rows + m.owner.mode.chromeRows()
	}
	line := func(v string) string {
		return ansi.Truncate(strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return ' '
			}
			return r
		}, ansi.Strip(v)), width, "")
	}
	location := "Not recording"
	if s.Path != "" {
		size := int64(0)
		if info, e := os.Stat(s.Path + ".partial"); e == nil {
			size = info.Size()
		}
		location = fmt.Sprintf("Recording · %.1f KiB · %s", float64(size)/1024, s.Path)
	}
	controls := "Ctrl+]: dashboard controls · normal keys go to the shell"
	if m.controls {
		controls = "Esc: shell · ]: send Ctrl+]"
		if m.owner.mode == LiveMode {
			controls += " · v/h: open viewer/host · c/a: copy viewer/host"
		}
	}
	lines := []string{line("TermBackTime · " + time.Since(m.started).Truncate(time.Second).String() + " · " + s.Status), strings.Repeat("─", width), termui.Fit(m.screen.Render(), width, max(1, height-m.owner.mode.chromeRows())), strings.Repeat("─", width), line(location)}
	if m.owner.mode == LiveMode {
		lines = append(lines, line(s.Room), line(diagnosticsText(s.Diagnostics, time.Now())), line("Viewer: "+s.Viewer), line("Private host: "+s.Host))
	}
	lines = append(lines, line(controls+" "+m.notice))
	view := tea.NewView(termui.Fit(strings.Join(lines, "\n"), width, height))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	if !m.controls && m.cursorVisible {
		p := m.screen.CursorPosition()
		if p.X < width && p.Y < height-m.owner.mode.chromeRows() {
			view.Cursor = tea.NewCursor(p.X, p.Y+2)
		}
	}
	return view
}

func diagnosticsText(d live.Diagnostics, now time.Time) string {
	rtt, rate, buffer := "unavailable", "unavailable", "unavailable"
	if d.RTTAvailable {
		rtt = fmt.Sprintf("%d ms", d.RTT.Milliseconds())
	}
	if d.RateAvailable {
		rate = fmt.Sprintf("%.1f KiB/s", d.BytesPerSecond/1024)
	}
	if d.BufferAvailable {
		buffer = fmt.Sprintf("%.1f KiB", float64(d.BufferedBytes)/1024)
	}
	return fmt.Sprintf("Connection %s · RTT to SFU %s · Up %s · Buffer %s · Reconnects %d", d.Quality(now), rtt, rate, buffer, d.ReconnectAttempts)
}
