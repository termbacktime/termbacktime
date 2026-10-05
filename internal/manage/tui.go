package manage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/termbacktime/termbacktime/internal/playback"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/termui"
)

type Backend interface {
	List(context.Context, bool, int) ([]Item, int, error)
	Inspect(context.Context, Item) (string, error)
	Delete(context.Context, Item) error
	Action(context.Context, Item, string) (string, error)
	Import(string) (string, error)
}
type Playback func(context.Context, Item) (*recording.Recording, error)
type playbackLoaded struct {
	epoch     int
	recording *recording.Recording
	err       error
}

func Run(ctx context.Context, input io.Reader, output io.Writer, backend Backend, remote bool, play Playback) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := newModel(ctx, backend, remote, play)
	m.mode = "source-choice"
	m.sourceChosen = false
	m.sourceCursor = LocalSource
	defer func() {
		m.closeQueueRunner()
		if m.verification != nil {
			m.verification.cancel()
		}
		if m.associations != nil {
			m.associations.cancel()
		}
		if m.storage != nil {
			m.storage.cancel()
		}
		if m.upload.cancel != nil {
			m.upload.cancel()
		}
		if m.cancelPlayback != nil {
			m.cancelPlayback()
		}
		if m.player != nil {
			m.player.Close()
		}
		if m.cancelDetail != nil {
			m.cancelDetail()
		}
		if m.cancelAction != nil {
			m.cancelAction()
		}
		if m.cancelList != nil {
			m.cancelList()
		}
	}()
	_, err := termui.Run(ctx, tea.NewProgram(m, tea.WithInput(input), tea.WithOutput(output), tea.WithoutSignalHandler()))
	return err
}

type listMsg struct {
	epoch  int
	items  []Item
	next   int
	append bool
	err    error
}
type detailMsg struct {
	epoch int
	text  string
	err   error
}
type actionMsg struct {
	epoch   int
	text    string
	err     error
	deleted *Item
	details bool
}
type model struct {
	currentBadges                        *badgesLoaded
	historySettings                      *historyView
	queue                                *queueView
	associations                         *associationView
	badgeEpoch                           int
	listing                              *listingSession
	listWarnings                         []string
	listProgress                         string
	filters                              [3]viewFilter
	filterCursor                         int
	verification                         *verification
	actionDone                           chan struct{}
	storage                              *storageView
	ctx                                  context.Context
	backend                              Backend
	play                                 Playback
	player                               *playback.Model
	playbackEpoch                        int
	cancelPlayback                       context.CancelFunc
	upload                               uploadState
	source, sourceCursor                 Source
	sourceChosen                         bool
	items                                []Item
	visible                              []int
	selected, next, width, height        int
	query                                string
	input                                textinput.Model
	mode                                 string
	actionEpoch                          int
	actionTitle, actionTarget            string
	cancelAction                         context.CancelFunc
	confirm                              *Item
	loading, busy, detailFocus           bool
	status, details                      string
	detailOffset, detailEpoch, listEpoch int
	promptOffset                         int
	cancelDetail, cancelList             context.CancelFunc
	detailGate                           chan struct{}
}

func newModel(ctx context.Context, backend Backend, remote bool, play Playback) *model {
	input := textinput.New()
	input.CharLimit = 4096
	source := LocalSource
	if remote {
		source = GistSource
	}
	return &model{ctx: ctx, backend: backend, source: source, sourceChosen: true, play: play, input: input, width: 100, height: 30, status: "Loading recordings…", detailGate: make(chan struct{}, 1)}
}
func (m *model) Init() tea.Cmd {
	if m.mode == "source-choice" {
		return nil
	}
	return m.refresh(false)
}
func (m *model) refresh(appendPage bool) tea.Cmd {
	m.currentBadges = nil
	if m.verification != nil {
		m.verification.cancel()
		m.verification = nil
	}
	if m.cancelList != nil {
		m.cancelList()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancelList = cancel
	m.listEpoch++
	epoch := m.listEpoch
	page := 1
	if appendPage {
		page = m.next
	}
	source := m.source
	remote := source != LocalSource
	m.loading = true
	m.status = "Loading recordings…"
	if backend, ok := m.backend.(ListingBackend); ok && !remote {
		return m.startListing(ctx, backend)
	}
	m.listing = nil
	return func() tea.Msg {
		var items []Item
		var next int
		var err error
		if backend, ok := m.backend.(SourceBackend); ok {
			items, next, err = backend.ListSource(ctx, source, page)
		} else {
			items, next, err = m.backend.List(ctx, remote, page)
		}
		return listMsg{epoch, items, next, appendPage, err}
	}
}
func (m *model) current() *Item {
	if m.selected < 0 || m.selected >= len(m.visible) {
		return nil
	}
	index := m.visible[m.selected]
	if index < 0 || index >= len(m.items) {
		return nil
	}
	return &m.items[index]
}
func (m *model) filter() { m.sortAndFilter() }

func (m *model) selectItem(loadRemote bool) tea.Cmd {
	if m.cancelDetail != nil {
		m.cancelDetail()
	}
	m.detailEpoch++
	epoch := m.detailEpoch
	m.details = ""
	m.detailOffset = 0
	i := m.current()
	if i == nil {
		return nil
	}
	m.details = i.Details()
	if i.pending || i.Local != nil && i.Local.Status == "missing" {
		return nil
	}
	if i.Gist != nil && !loadRemote {
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancelDetail = cancel
	item := *i
	m.details += "\nLoading recording metadata…"
	return func() tea.Msg {
		if err := m.acquireDetail(ctx); err != nil {
			return detailMsg{epoch: epoch, err: err}
		}
		defer func() { <-m.detailGate }()
		text, err := m.backend.Inspect(ctx, item)
		return detailMsg{epoch, text, err}
	}
}

// Serialize inspections so rapid selection changes cannot leave multiple scans
// running. Actions also wait here for canceled inspections to release file locks.
func (m *model) acquireDetail(ctx context.Context) error {
	select {
	case m.detailGate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-m.detailGate
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *model) prompt(mode string) tea.Cmd {
	m.mode = mode
	m.promptOffset = 0
	m.input.SetValue("")
	m.input.SetWidth(max(10, m.width-8))
	return m.input.Focus()
}

// Every blocking action has its own cancellable operation and result identity.
// A late result must not release another screen's busy state or steal its focus.
func (m *model) beginAction(title, target string, run func(context.Context) actionMsg) tea.Cmd {
	if m.cancelAction != nil {
		m.cancelAction()
	}
	if m.cancelDetail != nil {
		m.cancelDetail()
	}
	m.detailEpoch++
	if m.listing == nil {
		if m.cancelList != nil {
			m.cancelList()
		}
		m.listEpoch++
		m.loading = false
	}
	if m.actionDone != nil {
		close(m.actionDone)
	}
	m.actionDone = make(chan struct{})
	m.actionEpoch++
	epoch := m.actionEpoch
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancelAction = cancel
	m.mode, m.busy = "action", true
	m.actionTitle, m.actionTarget = title, target
	m.status = title + "…"
	return func() tea.Msg {
		if err := m.acquireDetail(ctx); err != nil {
			return actionMsg{epoch: epoch, err: err}
		}
		defer func() { <-m.detailGate }()
		result := run(ctx)
		result.epoch = epoch
		return result
	}
}
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	before := fmt.Sprintf("%s/%t/%t/%d/%d/%p/%p", m.mode, (m.source != LocalSource), m.detailFocus, m.width, m.height, m.player, m.storage)
	_, cmd := m.update(msg)
	after := fmt.Sprintf("%s/%t/%t/%d/%d/%p/%p", m.mode, (m.source != LocalSource), m.detailFocus, m.width, m.height, m.player, m.storage)
	if before != after {
		cmd = tea.Sequence(tea.ClearScreen, cmd)
	}
	return m, cmd
}
func (m *model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if handled, cmd := m.sourceUpdate(msg); handled {
		return m, cmd
	}
	if handled, cmd := m.historyUpdate(msg); handled {
		return m, cmd
	}
	if handled, cmd := m.queueUpdate(msg); handled {
		return m, cmd
	}
	if handled, cmd := m.associationUpdate(msg); handled {
		return m, cmd
	}
	if handled, cmd := m.listingUpdate(msg); handled {
		return m, cmd
	}
	if handled, cmd := m.verificationUpdate(msg); handled {
		return m, cmd
	}
	if handled, cmd := m.filterUpdate(msg); handled {
		return m, cmd
	}
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = max(1, size.Width)
		m.height = max(1, size.Height)
	}
	if handled, cmd := m.storageUpdate(msg); handled {
		return m, cmd
	}
	if handled, cmd := m.uploadUpdate(msg); handled {
		return m, cmd
	}
	if done, ok := msg.(playback.DoneMsg); ok {
		if done.Model == m.player {
			m.player.Close()
			m.player = nil
			m.busy = false
			m.status = "Playback finished"
			if done.Err != nil {
				m.status = "Playback failed: " + done.Err.Error()
			}
		}
		return m, nil
	}
	background := false
	switch msg.(type) {
	case detailMsg, listMsg, actionMsg:
		background = true
	}
	if m.player != nil && !background {
		if size, ok := msg.(tea.WindowSizeMsg); ok {
			m.width = max(1, size.Width)
			m.height = max(1, size.Height)
		}
		_, cmd := m.player.Update(msg)
		return m, cmd
	}
	if loaded, ok := msg.(playbackLoaded); ok {
		if loaded.epoch != m.playbackEpoch {
			if loaded.recording != nil {
				_ = loaded.recording.Close()
			}
			return m, nil
		}
		m.cancelPlayback()
		m.cancelPlayback = nil
		m.busy = false
		m.mode = ""
		if loaded.err != nil {
			m.status = "Playback failed: " + loaded.err.Error()
			return m, nil
		}
		options := playback.HistoryOptions{}
		if backend, ok := m.backend.(HistoryBackend); ok {
			store := backend.HistoryStore()
			options.Store = &store
		}
		m.player = playback.NewInteractive(m.ctx, loaded.recording, 1, 0, 0, options)
		m.player.Managed = true
		m.player.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		return m, m.player.Init()
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(1, msg.Width)
		m.height = max(1, msg.Height)
		m.input.SetWidth(max(1, m.width-8))
	case listMsg:
		if msg.epoch != m.listEpoch {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.status = "Could not load: " + msg.err.Error()
			return m, nil
		}
		id := ""
		if i := m.current(); i != nil {
			id = i.ID()
		}
		if !msg.append {
			m.items = nil
		}
		seen := map[string]bool{}
		for _, i := range m.items {
			seen[i.ID()] = true
		}
		for _, i := range msg.items {
			if !seen[i.ID()] {
				m.items = append(m.items, i)
				seen[i.ID()] = true
			}
		}
		m.next = msg.next
		m.filter()
		for j, k := range m.visible {
			if m.items[k].ID() == id {
				m.selected = j
				break
			}
		}
		m.status = fmt.Sprintf("%d recording(s) loaded", len(m.items))
		if m.next > 0 {
			m.status += " · n loads the next GitHub page (including pages with no recordings)"
		}
		return m, m.selectItem(false)
	case detailMsg:
		if msg.epoch != m.detailEpoch {
			return m, nil
		}
		if i := m.current(); i != nil {
			m.details = i.Details() + "\n"
			if msg.err != nil {
				m.details += "Metadata unavailable: " + msg.err.Error()
			} else {
				m.details = msg.text + "\n\nSource\n" + i.Details()
			}
		}
	case actionMsg:
		if msg.epoch != m.actionEpoch || m.cancelAction == nil {
			return m, nil
		}
		m.cancelAction()
		m.cancelAction = nil
		if m.actionDone != nil {
			close(m.actionDone)
			m.actionDone = nil
		}
		m.busy = false
		m.mode = ""
		m.detailFocus = false
		if msg.err != nil {
			m.status = "Action failed: " + msg.err.Error()
			if errors.Is(msg.err, context.Canceled) {
				m.status = "Action canceled"
			}
			if msg.deleted != nil {
				m.status += " · Deletion not confirmed; refresh before retrying"
			}
			m.details = m.status + "\n\n" + m.details
			m.detailOffset = 0
			return m, nil
		}
		if msg.deleted != nil {
			kept := make([]Item, 0, len(m.items))
			for _, item := range m.items {
				if item.ID() != msg.deleted.ID() || (item.Local == nil) != (msg.deleted.Local == nil) {
					kept = append(kept, item)
				}
			}
			m.items = kept
			m.filter()
			m.details = ""
			if item := m.current(); item != nil {
				m.details = item.Details()
			}
			refresh := m.refresh(false)
			m.status = "Deleted " + msg.deleted.Title() + " · Refreshing recordings…"
			return m, refresh
		}
		m.status = msg.text
		m.details = msg.text + "\n\n" + m.details
		m.detailOffset = 0
		m.detailFocus = msg.details
		return m, m.refreshBadges()
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" && m.mode != "playback-loading" {
			return m, tea.Quit
		}
		if m.busy {
			if m.cancelAction != nil && (key == "esc" || key == "q") {
				m.cancelAction()
				m.status = "Canceling…"
				return m, nil
			}
			if m.mode == "playback-loading" && (key == "q" || key == "esc" || key == "ctrl+c") {
				m.cancelPlayback()
				m.cancelPlayback = nil
				m.playbackEpoch++
				m.busy = false
				m.mode = ""
				m.status = "Playback canceled"
			}
			return m, nil
		}
		if m.mode != "" {
			if m.mode == "help" {
				if key == "esc" || key == "enter" || key == "?" || key == "q" {
					m.mode = ""
				}
				if key == "down" || key == "pgdown" {
					m.promptOffset++
				}
				if key == "up" || key == "pgup" {
					m.promptOffset = max(0, m.promptOffset-1)
				}
				return m, nil
			}
			if m.mode == "delete" && (key == "pgup" || key == "pgdown") {
				step := max(1, m.height-12)
				if key == "pgup" {
					step = -step
				}
				m.promptOffset = max(0, m.promptOffset+step)
				return m, nil
			}
			if key == "esc" {
				m.mode = ""
				m.confirm = nil
				m.input.Blur()
				return m, nil
			}
			if key == "enter" {
				value := m.input.Value()
				switch m.mode {
				case "search":
					m.query = value
					m.mode = ""
					m.input.Blur()
					m.selected = 0
					m.filter()
					return m, m.selectItem(false)
				case "delete":
					if value != "delete" {
						return m, nil
					}
					item := *m.confirm
					m.confirm = nil
					m.mode = ""
					m.input.Blur()
					return m, m.beginAction("Deleting recording", item.Title()+"\n\n"+item.Deletion(), func(ctx context.Context) actionMsg {
						return actionMsg{err: m.backend.Delete(ctx, item), deleted: &item}
					})
				case "import":
					if strings.TrimSpace(value) == "" {
						return m, nil
					}
					m.mode = ""
					m.input.Blur()
					return m, m.beginAction("Importing recording", value, func(ctx context.Context) actionMsg {
						if err := ctx.Err(); err != nil {
							return actionMsg{err: err}
						}
						text, err := m.backend.Import(value)
						return actionMsg{text: text, err: err}
					})
				}
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		switch key {
		case "q":
			return m, tea.Quit
		case "?":
			m.mode = "help"
			m.promptOffset = 0
			return m, nil
		case "b":
			m.sourceCursor = m.source
			m.mode = "source-choice"
			return m, nil
		case "1", "2", "3":
			return m, m.changeSource(Source(key[0] - '1'))
		case "tab", "shift+tab":
			m.detailFocus = !m.detailFocus
		case "/":
			return m, m.prompt("search")
		case "esc":
			if m.verification != nil && m.verification.running {
				m.verification.cancel()
				m.verification.status = "Canceling verification…"
				return m, nil
			}
			if m.verification != nil {
				m.verification = nil
			}
			if m.detailFocus {
				m.detailFocus = false
				return m, nil
			}
			m.query = ""
			m.filter()
			return m, m.selectItem(false)
		case "H":
			return m, m.openHistory()
		case "a":
			return m, m.openAssociations()
		case "f":
			m.mode = "filters"
			m.filterCursor = 0
			return m, nil
		case "v":
			return m, m.verifySelected()
		case "S":
			return m, m.startStorage()
		case "P":
			backend, ok := m.backend.(StorageBackend)
			if item := m.current(); ok && item != nil && item.Local != nil && m.actionReady() {
				copy := *item
				return m, m.beginAction("Updating recording pin", copy.Title(), func(ctx context.Context) actionMsg {
					pin, err := backend.PinItem(ctx, copy)
					label := "Recording unpinned"
					if pin {
						label = "Recording pinned"
					}
					return actionMsg{text: label, err: err}
				})
			}
			return m, nil
		case "i":
			return m, m.prompt("import")
		case "g":
			return m, m.refresh(false)
		case "n":
			if (m.source != LocalSource) && m.next > 0 && !m.loading {
				return m, m.refresh(true)
			}
		case "up", "k", "down", "j", "pgup", "pgdown", "home", "end":
			if m.listing != nil {
				m.listing.selection = ""
			}
			step := 1
			if key == "up" || key == "k" {
				step = -1
			}
			if key == "pgup" {
				step = -max(1, m.height-10)
			}
			if key == "pgdown" {
				step = max(1, m.height-10)
			}
			if m.detailFocus {
				width := m.width
				if width >= 80 {
					width -= max(28, min(48, width*2/5)) + 1
				}
				limit := max(0, len(wrapped(m.details, width-2))-(m.height-8))
				m.detailOffset = max(0, min(limit, min(limit, m.detailOffset)+step))
				if key == "home" {
					m.detailOffset = 0
				}
				if key == "end" {
					m.detailOffset = limit
				}
				return m, nil
			}
			m.selected = max(0, min(len(m.visible)-1, m.selected+step))
			if key == "home" {
				m.selected = 0
			}
			if key == "end" {
				m.selected = max(0, len(m.visible)-1)
			}
			return m, m.selectItem(false)
		case "enter":
			return m, m.selectItem(true)
		case "d", "delete":
			if !m.actionReady() {
				return m, nil
			}
			if i := m.current(); i != nil {
				if i.Local != nil && i.Local.Status == "recording" {
					m.status = "Active recordings cannot be deleted"
					return m, nil
				}
				item := *i
				m.confirm = &item
				return m, m.prompt("delete")
			}
		case "J", "U":
			return m, m.openQueue(key == "U")

		case "u":
			if m.actionReady() {
				if i := m.current(); i != nil {
					return m, m.startUpload(*i)
				}
			}
		case "s", "r":
			if !m.actionReady() {
				return m, nil
			}
			m.detailEpoch++
			if m.cancelDetail != nil {
				m.cancelDetail()
			}
			if i := m.current(); i != nil {
				item := *i
				action := map[string]string{"s": "scan", "r": "recover"}[key]
				return m, m.beginAction("Running "+action, item.Title(), func(ctx context.Context) actionMsg {
					text, err := m.backend.Action(ctx, item, action)
					return actionMsg{text: text, err: err, details: true}
				})
			}
		case "p":
			if !m.actionReady() {
				return m, nil
			}
			if i := m.current(); i != nil && m.play != nil {
				m.busy = true
				m.mode = "playback-loading"
				m.playbackEpoch++
				epoch := m.playbackEpoch
				ctx, cancel := context.WithCancel(m.ctx)
				m.cancelPlayback = cancel
				item := *i
				m.status = "Loading playback…"
				return m, func() tea.Msg { r, err := m.play(ctx, item); return playbackLoaded{epoch, r, err} }
			}
		}
	}
	if m.mode != "" && m.mode != "help" && !m.busy {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

var accent = lipgloss.NewStyle().Foreground(lipgloss.Color("#67e8c1"))
var muted = lipgloss.NewStyle().Foreground(lipgloss.Color("#8b9aad"))
var selected = lipgloss.NewStyle().Foreground(lipgloss.Color("#111827")).Background(lipgloss.Color("#67e8c1")).Bold(true)

func line(s string, w int) string {
	s = ansi.Truncate(s, max(0, w), "…")
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}
func wrapped(s string, w int) []string {
	return strings.Split(ansi.Hardwrap(plain(s), max(1, w), true), "\n")
}
func pane(title string, content []string, w, h int, focus bool) string {
	style := muted
	if focus {
		style = accent
	}
	rows := []string{style.Render("┌" + line(" "+title+" ", w-2) + "┐")}
	for j := 0; j < h-2; j++ {
		value := ""
		if j < len(content) {
			value = content[j]
		}
		rows = append(rows, style.Render("│")+line(value, w-2)+style.Render("│"))
	}
	rows = append(rows, style.Render("└"+strings.Repeat("─", max(0, w-2))+"┘"))
	return strings.Join(rows, "\n")
}
func (m *model) View() tea.View {
	if m.mode == "source-choice" {
		return m.sourceView()
	}
	if m.mode == "history" {
		return m.historyScreen()
	}
	if m.mode == "queue" {
		return m.queueScreen()
	}
	if m.mode == "associations" {
		return m.associationScreen()
	}
	if m.storage != nil {
		return m.storageView()
	}
	if m.player != nil {
		return m.player.View()
	}
	w, h := m.width, m.height
	view := tea.NewView("")
	view.AltScreen = true
	if w < 40 || h < 16 {
		view.Content = termui.Fit("Resize to at least 40 × 16. q quits.", w, h)
		return view
	}
	tabs := "[1 Local]   2 Gists   3 Repository"
	if m.source == GistSource {
		tabs = "1 Local   [2 Gists]   3 Repository"
	}
	if m.source == RepoSource {
		tabs = "1 Local   2 Gists   [3 Repository]"
	}
	query := "/ Filter recordings"
	if m.query != "" {
		query = "Filter: " + plain(m.query) + " · Esc clears"
	}
	rows := []string{accent.Render(line(" TERMBACKTIME  /  Recording manager", w)), line(" "+tabs+"    Tab: pane · ?: help", w), muted.Render(line(" "+query, w))}
	bodyHeight := h - 6
	if m.mode == "upload-review" {
		return m.upload.review.View()
	} else if m.mode == "action" {
		content := m.status + "\n\n" + m.actionTarget + "\n\nEsc cancels · Ctrl+C quits"
		rows = append(rows, pane(m.actionTitle, wrapped(content, w-2), w, bodyHeight, true))
	} else if m.mode == "playback-loading" {
		rows = append(rows, pane("Loading playback", []string{"Esc cancels"}, w, bodyHeight, true))
	} else if strings.HasPrefix(m.mode, "upload-") {
		title := "Upload recording"
		content := m.status + "\n\n" + m.upload.notice + "\n\nEsc cancels"
		if m.mode == "upload-result" {
			title = "Recording uploaded"
			content = m.status + "\n\n" + m.upload.link + "\n\nc Copy link · o Open link · Enter Return"
		}
		rows = append(rows, pane(title, wrapped(content, w-2), w, bodyHeight, true))
	} else if m.mode == "filters" {
		rows = append(rows, pane("Sort and filter", m.filterRows(), w, bodyHeight, true))
	} else if m.mode == "help" {
		help := wrapped("1 / 2 / 3 Local / Gists / Repository\nb        Choose recording storage\n↑↓ j/k   Navigate or scroll\nTab      Switch list / metadata\n/        Filter loaded titles, IDs, paths\nEnter    Preview recording metadata\nv        Verify all local recording events\nf        Sort and filter recordings\np        Play; Q returns to manager\ns        Scan for likely secrets\nu        Upload a local recording; review options or add to queue\nJ / U    Open / run upload queue\na        Saved uploads for local recording\nH        Playback history settings\nr        Recover an inactive journal\ni        Import a local file\nd        Confirm deletion\nS        Storage usage and cleanup\nP        Pin/unpin selected local recording\nn        Load next page of remote recordings\ng        Refresh current source\nEsc      Clear filter / cancel\nq        Quit manager\n\nEsc / Enter closes help", w-2)
		offset := min(m.promptOffset, max(0, len(help)-(bodyHeight-2)))
		rows = append(rows, pane("Shortcuts · ↑↓ scroll · Esc close", help[offset:], w, bodyHeight, true))
	} else if m.mode != "" {
		title, help := "Filter loaded recordings", "Enter applies · Esc cancels"
		prompt := []string{}
		if m.mode == "delete" {
			title = "Confirm deletion"
			help = "Type delete · Enter confirms · Esc cancels"
			prompt = wrapped(m.confirm.Title()+"\n\n"+m.confirm.Deletion(), w-6)
		}
		if m.mode == "import" {
			title = "Import a local recording"
			prompt = wrapped("Enter an existing .tbt or .partial file path. The file stays in its current location.", w-6)
		}

		// The complete target can be reviewed without hiding the confirmation input.
		limit := max(1, bodyHeight-6)
		if len(prompt) > limit {
			offset := min(m.promptOffset, len(prompt)-limit)
			prompt = prompt[offset : offset+limit]
			title += " · PgUp/PgDn to scroll"
		}
		prompt = append(prompt, "", m.input.View(), "", help)
		rows = append(rows, pane(title, prompt, w, bodyHeight, true))
	} else {
		leftW := max(28, min(48, w*2/5))
		rightW := w - leftW - 1
		narrow := w < 80
		if narrow {
			leftW = w
			rightW = w
		}
		list := []string{}
		capacity := max(1, (bodyHeight-2)/2)
		start := max(0, m.selected-capacity+1)
		for j := start; j < len(m.visible) && j < start+capacity; j++ {
			i := m.items[m.visible[j]]
			name := line("  "+strings.ReplaceAll(plain(i.Title()), "\n", " "), leftW-2)
			if j == m.selected {
				name = selected.Render(line("› "+strings.ReplaceAll(plain(i.Title()), "\n", " "), leftW-2))
			}
			list = append(list, name, muted.Render(line("  "+plain(i.Subtitle()), leftW-2)))
		}
		if len(list) == 0 {
			message := "No recordings found. i imports a file."
			if m.source != LocalSource {
				message = "No recordings on loaded pages."
				if m.next > 0 {
					message += " Press n for more."
				}
			}
			if m.loading {
				message = "Loading…"
			}
			list = wrapped(message, leftW-2)
		}
		text := m.details
		if text == "" {
			text = "Choose a recording to view its metadata.\n\n1 Local: your recording library, including imported files.\n2 Gists: public and secret recording Gists. Run termbacktime auth --storage gist to connect.\n3 Repository: your public TBT-Recordings. Run termbacktime auth --storage repo to connect.\n\nNo terminal output is rendered here."
		}
		details := wrapped(text, rightW-2)
		offset := min(m.detailOffset, max(0, len(details)-(bodyHeight-2)))
		left := pane(fmt.Sprintf("Recordings %d/%d", min(m.selected+1, len(m.visible)), len(m.visible)), list, leftW, bodyHeight, !m.detailFocus)
		right := pane(fmt.Sprintf("Metadata %d/%d", offset+1, len(details)), details[offset:], rightW, bodyHeight, m.detailFocus)
		if narrow {
			if m.detailFocus {
				rows = append(rows, right)
			} else {
				rows = append(rows, left)
			}
		} else {
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right))
		}
	}
	rows = append(rows, line(" "+strings.ReplaceAll(plain(m.listingStatus()+m.queueIndicator()), "\n", " · "), w))
	help := " ↑↓ Navigate · Enter Inspect · p Play · s Scan"
	tail := " d Delete · i Import · / Filter · n More recordings · g Refresh · q Quit"
	canUpload := false
	if item := m.current(); item != nil {
		canUpload = item.Local != nil && item.Local.Status == "ready"
	}
	if canUpload {
		help = " ↑↓ Navigate · p Play · u Upload · s Scan"
	}
	if w < 80 {
		help = " p Play · s Scan"
		if canUpload {
			help = " p Play · u Upload · s Scan"
		}
		tail = " b Storage · 1/2/3 Source · Tab Details · / Filter · q Quit"
	}
	if m.mode == "action" {
		help, tail = " Esc Cancel operation · Ctrl+C Quit manager", ""
	}
	rows = append(rows, muted.Render(line(help, w)), muted.Render(line(tail, w)))
	view.Content = termui.Fit(strings.Join(rows, "\n"), w, h)
	return view
}
