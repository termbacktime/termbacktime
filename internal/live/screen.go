package live

import (
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/x/vt"
	"github.com/termbacktime/termbacktime/internal/recording"
)

type SessionMetadata struct {
	Title   string         `json:"title"`
	Started int64          `json:"started"`
	Info    recording.Info `json:"info"`
}

type ScreenFrame struct {
	Type      string           `json:"type"`
	Cols      int              `json:"cols"`
	Rows      int              `json:"rows"`
	Screen    string           `json:"screen"`
	Cursor    [2]int           `json:"cursor"`
	Visible   bool             `json:"visible"`
	Alternate bool             `json:"alternate"`
	Title     string           `json:"title"`
	Progress  [2]int           `json:"progress"`
	Session   *SessionMetadata `json:"session,omitempty"`
}

// Screen owns the pinned emulator and exposes only normalized current-screen state
type Screen struct {
	mu        sync.Mutex
	emulator  *vt.Emulator
	drained   chan struct{}
	stopRead  chan struct{}
	closeOnce sync.Once
	dirty     bool
	visible   bool
	title     string
	progress  [2]int
	session   *SessionMetadata
	rendered  string
}

// Session metadata travels only inside the authenticated encrypted screen frame.
func (s *Screen) SetSession(title string, started int64, info recording.Info) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session = &SessionMetadata{Title: string([]rune(title)[:min(len([]rune(title)), 256)]), Started: started, Info: info}
	s.dirty = true
}

func NewScreen(cols, rows int) *Screen {
	s := &Screen{
		emulator: vt.NewEmulator(cols, rows),
		drained:  make(chan struct{}),
		stopRead: make(chan struct{}),
		dirty:    true,
		visible:  true,
	}
	s.emulator.SetScrollbackSize(0)
	s.emulator.SetCallbacks(vt.Callbacks{
		CursorVisibility: func(visible bool) { s.visible = visible },
		Title: func(title string) {
			s.title = string([]rune(title)[:min(len([]rune(title)), 256)])
		},
	})
	s.emulator.RegisterOscHandler(9, s.setProgress)
	// Query replies belong to the host terminal and must not stall this observer
	go func() {
		defer close(s.drained)
		buffer := make([]byte, 4096)
		for {
			if _, err := s.emulator.Read(buffer); err != nil {
				return
			}
			select {
			case <-s.stopRead:
				return
			default:
			}
		}
	}()
	return s
}

// OSC data includes its command number before the progress subcommand
func (s *Screen) setProgress(data []byte) bool {
	fields := strings.Split(string(data), ";")
	if len(fields) < 3 || fields[0] != "9" || fields[1] != "4" {
		return false
	}
	state, err := strconv.Atoi(fields[2])
	if err != nil || state < 0 || state > 4 {
		return false
	}
	value := 0
	if len(fields) > 3 {
		value, err = strconv.Atoi(fields[3])
		if err != nil {
			return false
		}
	}
	s.progress = [2]int{state, max(0, min(value, 100))}
	return true
}

func (s *Screen) Write(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.emulator.WriteString(text)
	s.dirty = true
	return err
}

func (s *Screen) Resize(cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emulator.Resize(cols, rows)
	s.dirty = true
}

// Every update is replaceable so backpressure never requires replaying old output
func (s *Screen) Frame(force bool) (ScreenFrame, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty && !force {
		return ScreenFrame{}, false
	}
	frameType := "update"
	if force {
		frameType = "snapshot"
	}
	pos := s.emulator.CursorPosition()
	if s.dirty {
		s.rendered = strings.ReplaceAll(s.emulator.Render(), "\n", "\r\n")
	}
	s.dirty = false
	return ScreenFrame{
		Type:      frameType,
		Cols:      s.emulator.Width(),
		Rows:      s.emulator.Height(),
		Screen:    s.rendered,
		Cursor:    [2]int{pos.X, pos.Y},
		Visible:   s.visible,
		Alternate: s.emulator.IsAltScreen(),
		Title:     s.title,
		Progress:  s.progress,
		Session:   s.session,
	}, true
}

func (s *Screen) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		// Wake and join the reader before touching the emulator's unsynchronized close flag
		close(s.stopRead)
		_, _ = s.emulator.WriteString("\x1b[5n")
		<-s.drained
		_ = s.emulator.Close()
	})
}
