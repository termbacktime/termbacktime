package manage

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"github.com/termbacktime/termbacktime/internal/library"
	"strings"
)

type viewFilter struct {
	library.Filter
	Visibility, Encryption string
}

func (m *model) selectedFilters() *viewFilter {
	return &m.filters[m.source]
}
func (m *model) sortAndFilter() {
	f := m.selectedFilters()
	id := ""
	if item := m.current(); item != nil {
		id = item.ID()
	}
	entries := make([]library.Entry, 0, len(m.items))
	byID := map[string]Item{}
	for _, item := range m.items {
		byID[item.ID()] = item
		if item.Local != nil {
			entries = append(entries, *item.Local)
		} else {
			g := item.Gist
			started := g.Updated.Unix()
			if f.SortBy == "created" {
				started = g.Created.Unix()
			}
			size := int64(0)
			for _, file := range g.Files {
				size += file.Size
			}
			entries = append(entries, library.Entry{ID: g.ID, Title: item.Title(), Started: started, Bytes: size})
		}
	}
	by := f.SortBy
	if by == "created" || by == "updated" {
		by = "date"
	}
	library.Sort(entries, by, f.Order)
	m.items = nil
	m.visible = nil
	for _, e := range entries {
		item := byID[e.ID]
		index := len(m.items)
		m.items = append(m.items, item)
		if !strings.Contains(strings.ToLower(plain(item.Title()+" "+item.ID()+" "+item.Path())), strings.ToLower(m.query)) {
			continue
		}
		if item.Local != nil {
			if !f.Match(*item.Local) {
				continue
			}
		} else {
			if f.Visibility == "public" && !item.Gist.Public || f.Visibility == "secret" && item.Gist.Public {
				continue
			}
			if f.Encryption == "encrypted" && !item.Gist.Encrypted() || f.Encryption == "plaintext" && item.Gist.Encrypted() {
				continue
			}
		}
		m.visible = append(m.visible, index)
	}
	m.selected = min(m.selected, max(0, len(m.visible)-1))
	for n, index := range m.visible {
		if m.items[index].ID() == id {
			m.selected = n
			break
		}
	}
}
func cycle(value string, choices []string) string {
	for n, v := range choices {
		if value == v {
			return choices[(n+1)%len(choices)]
		}
	}
	return choices[0]
}
func toggleFilter(value **bool) {
	if *value == nil {
		b := true
		*value = &b
	} else if **value {
		b := false
		*value = &b
	} else {
		*value = nil
	}
}
func boolFilter(value *bool) string {
	if value == nil {
		return "any"
	}
	if *value {
		return "yes"
	}
	return "no"
}

var statuses = []string{"ready", "recording", "partial", "missing", "unsupported", "invalid"}

func (m *model) filterRows() []string {
	f := m.selectedFilters()
	by := f.SortBy
	if by == "" {
		by = "date"
		if m.source != LocalSource {
			by = "updated"
		}
	}
	if (m.source != LocalSource) && by == "size" {
		by = "total publication file size"
	}
	order := f.Order
	if order == "" {
		order = "default"
	}
	rows := []string{"Sort: " + by, "Order: " + order}
	if m.source != LocalSource {
		rows = append(rows, "Visibility: "+fallback(f.Visibility, "any"), "Encryption: "+fallback(f.Encryption, "any"))
	} else {
		rows = append(rows, "Pinned: "+boolFilter(f.Pinned), "Has saved uploads: "+boolFilter(f.Uploaded))
		for _, status := range statuses {
			selected := false
			for _, s := range f.Statuses {
				selected = selected || s == status
			}
			rows = append(rows, fmt.Sprintf("[%s] %s", map[bool]string{true: "x", false: " "}[selected], status))
		}
	}
	for n := range rows {
		if n == m.filterCursor {
			rows[n] = "› " + rows[n]
		} else {
			rows[n] = "  " + rows[n]
		}
	}
	window := max(1, m.height-13)
	start := min(max(0, m.filterCursor-window+1), max(0, len(rows)-window))
	rows = rows[start:min(len(rows), start+window)]
	return append(rows, "", "↑↓ selects · Space changes · x clears · Esc returns", "No selected statuses means all statuses. GitHub filters use loaded pages.")
}
func (m *model) filterUpdate(msg tea.Msg) (bool, tea.Cmd) {
	if m.mode != "filters" {
		return false, nil
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}
	f := m.selectedFilters()
	limit := 9
	if m.source != LocalSource {
		limit = 3
	}
	switch key.String() {
	case "esc", "q", "f":
		m.mode = ""
		return true, m.selectItem(false)
	case "ctrl+c":
		return true, tea.Quit
	case "up", "k":
		m.filterCursor = max(0, m.filterCursor-1)
	case "down", "j":
		m.filterCursor = min(limit, m.filterCursor+1)
	case "x":
		*f = viewFilter{}
	case "space", "enter", "right", "left":
		switch m.filterCursor {
		case 0:
			choices := []string{"date", "title", "duration", "size"}
			if m.source != LocalSource {
				choices = []string{"updated", "created", "title", "size"}
			}
			if f.SortBy == "" {
				f.SortBy = choices[0]
			}
			f.SortBy = cycle(f.SortBy, choices)
		case 1:
			f.Order = cycle(f.Order, []string{"", "asc", "desc"})
		case 2:
			if m.source != LocalSource {
				f.Visibility = cycle(f.Visibility, []string{"", "public", "secret"})
			} else {
				toggleFilter(&f.Pinned)
			}
		case 3:
			if m.source != LocalSource {
				f.Encryption = cycle(f.Encryption, []string{"", "encrypted", "plaintext"})
			} else {
				toggleFilter(&f.Uploaded)
			}
		default:
			s := statuses[m.filterCursor-4]
			found := -1
			for n, v := range f.Statuses {
				if s == v {
					found = n
				}
			}
			if found < 0 {
				f.Statuses = append(f.Statuses, s)
			} else {
				f.Statuses = append(f.Statuses[:found], f.Statuses[found+1:]...)
			}
		}
	}
	m.filter()
	return true, nil
}
