package manage

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/termbacktime/termbacktime/internal/storage"
	"github.com/termbacktime/termbacktime/internal/termui"
)

type StorageBackend interface {
	StorageStore() storage.Store
	PinItem(context.Context, Item) (bool, error)
}

func (s *Store) StorageStore() storage.Store { return storage.Store{Root: s.Library.Root} }
func (s *Store) PinItem(ctx context.Context, item Item) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if item.Local == nil {
		return false, fmt.Errorf("only local recordings can be pinned")
	}
	pinned, err := s.Library.Pinned(item.Local.ID)
	if err != nil {
		return false, err
	}
	err = s.Library.SetPinned(item.Local.ID, !pinned)
	return !pinned, err
}

type storageView struct {
	report    storage.Report
	filter    storage.Filter
	age, size string
	items     []storage.Item
	selected  map[string]bool
	cursor    int
	editing   string
	busy      bool
	status    string
	cancel    context.CancelFunc
}
type storageLoaded struct {
	owner  *storageView
	report storage.Report
	err    error
}
type storageApplied struct {
	owner   *storageView
	results []storage.Result
	err     error
}

func (m *model) startStorage() tea.Cmd {
	if m.storage != nil {
		m.storage.cancel()
	}
	backend, ok := m.backend.(StorageBackend)
	if !ok {
		m.status = "Storage management unavailable"
		return nil
	}
	v := &storageView{filter: storage.Filter{Kind: "all"}, age: "0s", size: "0", selected: map[string]bool{}, busy: true, status: "Loading storage…"}
	m.storage = v
	ctx, cancel := context.WithCancel(m.ctx)
	v.cancel = cancel
	return func() tea.Msg { r, e := backend.StorageStore().Snapshot(ctx); return storageLoaded{v, r, e} }
}
func (v *storageView) selectItems() {
	v.items = v.report.Select(v.filter, time.Now())
	v.cursor = min(v.cursor, max(0, len(v.items)-1))
	v.selected = map[string]bool{}
}
func (m *model) storageUpdate(msg tea.Msg) (bool, tea.Cmd) {
	switch value := msg.(type) {
	case listMsg, detailMsg, actionMsg:
		return false, nil
	case storageLoaded:
		if m.storage != value.owner {
			return true, nil
		}
		v := m.storage
		v.busy = false
		if value.err != nil {
			v.status = value.err.Error()
		} else {
			v.report = value.report
			v.selectItems()
			v.status = "Preview only. Select items before deleting."
		}
		return true, nil
	case storageApplied:
		if m.storage != value.owner {
			return true, nil
		}
		v := m.storage
		v.busy = false
		v.editing = ""
		v.selected = map[string]bool{}
		removed := map[string]bool{}
		var failures []string
		for _, r := range value.results {
			if r.Deleted {
				removed[r.Path] = true
			} else {
				failures = append(failures, plain(r.Error))
			}
		}
		if value.err != nil {
			failures = append(failures, value.err.Error())
		}
		v.status = fmt.Sprintf("Deleted %d items", len(removed))
		if len(failures) > 0 {
			v.status += " · " + strings.Join(failures, "; ")
		}
		kept := v.report.Items[:0]
		for _, i := range v.report.Items {
			if !removed[i.Path] {
				kept = append(kept, i)
			} else {
				t := v.report.Totals[i.Kind]
				t.Files -= i.Files
				t.Bytes -= i.Bytes
				v.report.Totals[i.Kind] = t
			}
		}
		v.report.Items = kept
		v.selectItems()
		return true, nil
	}
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		return false, nil
	}
	v := m.storage
	if v == nil {
		return false, nil
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return true, nil
	}
	k := key.String()
	if v.busy {
		if k == "esc" || k == "ctrl+c" {
			v.cancel()
			v.status = "Canceling…"
		}
		return true, nil
	}
	if v.editing != "" {
		if k == "esc" {
			v.editing = ""
			m.input.Blur()
			return true, nil
		}
		if k == "enter" {
			text := strings.TrimSpace(m.input.Value())
			switch v.editing {
			case "age":
				d, e := storage.ParseAge(text)
				if e != nil {
					v.status = e.Error()
					return true, nil
				}
				v.filter.OlderThan = d
				v.age = text
				v.selectItems()
			case "size":
				n, e := storage.ParseSize(text)
				if e != nil {
					v.status = e.Error()
					return true, nil
				}
				v.filter.MinSize = n
				v.size = text
				v.selectItems()
			case "delete":
				if text != "delete" {
					v.status = "Type delete to confirm, or Esc to cancel"
					return true, nil
				}
				selected := []storage.Item{}
				for _, item := range v.items {
					if v.selected[item.Path] {
						selected = append(selected, item)
					}
				}
				backend := m.backend.(StorageBackend)
				ctx, cancel := context.WithCancel(m.ctx)
				v.cancel = cancel
				v.busy = true
				v.status = "Applying reviewed cleanup…"
				return true, func() tea.Msg { r, e := backend.StorageStore().Apply(ctx, selected); return storageApplied{v, r, e} }
			}
			v.editing = ""
			m.input.Blur()
			return true, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return true, cmd
	}
	switch k {
	case "q", "esc", "ctrl+c":
		v.cancel()
		m.storage = nil
		return true, m.refresh(false)
	case "g":
		return true, m.startStorage()
	case "tab":
		kinds := []string{"all", "recordings", "exports", "queue"}
		for i, k := range kinds {
			if k == v.filter.Kind {
				v.filter.Kind = kinds[(i+1)%len(kinds)]
				break
			}
		}
		v.selectItems()
	case "a", "m":
		v.editing = "age"
		value := v.age
		if k == "m" {
			v.editing = "size"
			value = v.size
		}
		m.input.SetValue(value)
		return true, m.input.Focus()
	case "up", "k":
		v.cursor = max(0, v.cursor-1)
	case "down", "j":
		v.cursor = min(max(0, len(v.items)-1), v.cursor+1)
	case "space":
		if len(v.items) > 0 {
			p := v.items[v.cursor].Path
			v.selected[p] = !v.selected[p]
		}
	case "A":
		all := true
		for _, item := range v.items {
			all = all && v.selected[item.Path]
		}
		for _, item := range v.items {
			v.selected[item.Path] = !all
		}
	case "enter":
		n := 0
		for _, item := range v.items {
			if v.selected[item.Path] {
				n++
			}
		}
		if n == 0 {
			v.status = "Select at least one item with Space"
		} else {
			v.editing = "delete"
			m.input.SetValue("")
			return true, m.input.Focus()
		}
	}
	return true, nil
}
func (m *model) storageView() tea.View {
	v := m.storage
	var lines []string
	lines = append(lines, "Storage management · manual cleanup")
	for _, kind := range []string{"recordings", "exports", "queue", "protected", "external"} {
		t := v.report.Totals[kind]
		lines = append(lines, fmt.Sprintf("%s: %d files · %s", kind, t.Files, bytes(t.Bytes)))
	}
	lines = append(lines, fmt.Sprintf("Kind: %s · Older than: %s · Minimum: %s", v.filter.Kind, v.age, v.size))
	selected, total := 0, int64(0)
	for _, item := range v.items {
		if v.selected[item.Path] {
			selected++
			total += item.Bytes
		}
	}
	lines = append(lines, fmt.Sprintf("Selected %d items · %s reclaimable", selected, bytes(total)))
	room := max(1, m.height-len(lines)-5)
	start := max(0, v.cursor-room+1)
	for i := start; i < len(v.items) && i < start+room; i++ {
		item := v.items[i]
		mark := "[ ]"
		if v.selected[item.Path] {
			mark = "[x]"
		}
		cursor := " "
		if i == v.cursor {
			cursor = ">"
		}
		lines = append(lines, fmt.Sprintf("%s %s %s %s %s", cursor, mark, item.Kind, bytes(item.Bytes), plain(item.Path)))
	}
	if len(v.items) == 0 {
		lines = append(lines, "No eligible items match. Protected files are retained.")
	}
	lines = append(lines, plain(v.status))
	if v.editing != "" {
		lines = append(lines, "Enter "+v.editing+": "+m.input.View(), "Enter confirms · Esc cancels")
	} else {
		lines = append(lines, "↑↓ Navigate · Space Select · A Select all · Enter Preview/delete", "Tab Kind · a Age · m Minimum size · g Refresh · Esc Back")
	}
	view := tea.NewView(termui.Fit(strings.Join(lines, "\n"), m.width, m.height))
	view.AltScreen = true
	return view
}
