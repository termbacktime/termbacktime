package playback

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/termbacktime/termbacktime/internal/history"
	"github.com/termbacktime/termbacktime/internal/live"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/termui"
	"sync/atomic"
	"time"
)

type HistoryOptions struct {
	Store         *history.Store
	ExplicitStart bool
}
type restoration struct {
	progress atomic.Int64
	target   int
	cancel   context.CancelFunc
	done     chan struct{}
	screen   *live.Screen
	position float64
	index    int
	err      error
}
type restorationTick struct {
	owner *Model
	task  *restoration
}

func (m *Model) restorationTick(task *restoration) tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return restorationTick{m, task} })
}

type restored struct {
	owner *Model
	task  *restoration
}

func NewInteractive(ctx context.Context, r *recording.Recording, speed float64, idle time.Duration, start float64, options HistoryOptions) *Model {
	a, b := recording.Times(r, idle.Milliseconds())
	m := &Model{ctx: ctx, recording: r, original: a, presented: b, speed: speed, loopEnd: float64(a[len(a)-1]), playing: true, width: 80, height: 24, interactiveInit: true, initialStart: start}
	m.screen = live.NewScreen(r.Sizes[0], r.Sizes[1])
	if options.Store != nil && r.HistoryID != "" {
		state, err := options.Store.State()
		if err != nil {
			m.historyNotice = "History unavailable: " + err.Error()
		} else if state.Enabled {
			m.historyStore = options.Store
			if p, ok := state.Entries[r.HistoryID]; ok && p.Position > 0 && p.Position < a[len(a)-1] && !options.ExplicitStart {
				m.resumeChoice = true
				m.resumePosition = float64(p.Position)
			}
		}
	}
	return m
}
func (m *Model) restore(start float64) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	task := &restoration{cancel: cancel, done: make(chan struct{})}
	for task.target < m.recording.Count() && float64(m.original[task.target+1]) <= start {
		task.target++
	}
	m.restoration = task
	m.resumeChoice = false
	go func() {
		defer close(task.done)
		scratch := &Model{ctx: ctx, recording: m.recording, original: m.original, presented: m.presented, loopEnd: m.loopEnd, restoreProgress: func(n int) { task.progress.Store(int64(n)) }}
		task.err = scratch.seek(start, m.recording.Count())
		task.screen = scratch.screen
		task.position = scratch.position
		task.index = scratch.index
	}()
	return tea.Batch(func() tea.Msg { <-task.done; return restored{m, task} }, m.restorationTick(task))
}
func (m *Model) saveHistory(force bool) {
	if m.historyStore == nil || !m.historyReady {
		return
	}
	if !force && time.Since(m.historySaved) < 5*time.Second {
		return
	}
	m.historySaved = time.Now()
	if err := m.historyStore.Save(m.recording.HistoryID, int64(m.position), int64(m.loopEndDuration())); err != nil {
		m.historyNotice = "History unavailable: " + err.Error()
		m.historyStore = nil
	}
}
func (m *Model) historyUpdate(msg tea.Msg) (bool, tea.Cmd) {
	if tick, ok := msg.(restorationTick); ok {
		if tick.owner == m && tick.task == m.restoration {
			return true, m.restorationTick(tick.task)
		}
		return true, nil
	}
	if result, ok := msg.(restored); ok {
		if result.owner != m || result.task != m.restoration {
			return true, nil
		}
		task := m.restoration
		m.restoration = nil
		task.cancel()
		m.screen.Close()
		m.screen = task.screen
		if task.err != nil {
			return true, m.finish(task.err)
		}
		m.position, m.index = task.position, task.index
		m.historyReady = true
		m.saveHistory(true)
		m.previous = time.Now()
		return true, m.tick()
	}
	if !m.resumeChoice && m.restoration == nil {
		return false, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "q", "ctrl+c":
			return true, m.finish(nil)
		case "enter", "r":
			if m.resumeChoice {
				return true, m.restore(m.resumePosition)
			}
		case "s":
			if m.resumeChoice {
				return true, m.restore(0)
			}
		}
	}
	return true, nil
}
func (m *Model) historyView() (tea.View, bool) {
	if !m.resumeChoice && m.restoration == nil {
		return tea.View{}, false
	}
	text := "Restoring terminal state…\n\nEsc cancels"
	if m.restoration != nil {
		text = fmt.Sprintf("Restoring terminal state… %d / %d events\n\nEsc cancels", m.restoration.progress.Load(), m.restoration.target)
	}
	if m.resumeChoice {
		text = fmt.Sprintf("Resume playback at %.1f seconds?\n\nEnter / r Resume · s Start over · Esc Back", m.resumePosition/1000)
	}
	view := tea.NewView(termui.Fit(text, m.width, m.height))
	view.AltScreen = true
	return view, true
}
