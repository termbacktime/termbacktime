package manage

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/atotto/clipboard"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/termui"
	"strings"
	"time"
)

type AssociationBackend interface {
	SavedUploads(context.Context, string) ([]library.ReceiptSummary, []string, error)
	SavedLink(string, string) (string, error)
	CheckUpload(context.Context, string) (string, error)
	Badges(context.Context) (library.Enrichment, []string, error)
}

func (s *Store) SavedUploads(ctx context.Context, id string) ([]library.ReceiptSummary, []string, error) {
	return s.Library.Receipts(ctx, id)
}
func (s *Store) SavedLink(id, recordingID string) (string, error) {
	return s.Library.ReceiptLink(id, recordingID)
}
func (s *Store) CheckUpload(ctx context.Context, id string) (string, error) {
	if s.GitHub == nil {
		return "", fmt.Errorf("GitHub is unavailable")
	}
	return s.GitHub.CheckAvailability(ctx, id)
}
func (s *Store) Badges(ctx context.Context) (library.Enrichment, []string, error) {
	return s.Library.Enrichment(ctx)
}

type associationView struct {
	item     Item
	receipts []library.ReceiptSummary
	cursor   int
	status   string
	checks   map[string]string
	cancel   context.CancelFunc
	ctx      context.Context
	epoch    int
}
type associationsLoaded struct {
	owner    *associationView
	receipts []library.ReceiptSummary
	warnings []string
	err      error
}
type associationResult struct {
	owner    *associationView
	epoch    int
	id, text string
	err      error
}
type badgesLoaded struct {
	listEpoch int
	epoch     int
	value     library.Enrichment
	warnings  []string
	err       error
}

func (m *model) refreshBadges() tea.Cmd {
	backend, ok := m.backend.(AssociationBackend)
	if !ok {
		return nil
	}
	m.badgeEpoch++
	epoch, listing := m.badgeEpoch, m.listEpoch
	return func() tea.Msg {
		value, warnings, err := backend.Badges(m.ctx)
		return badgesLoaded{listEpoch: listing, epoch: epoch, value: value, warnings: warnings, err: err}
	}
}
func (m *model) openAssociations() tea.Cmd {
	backend, ok := m.backend.(AssociationBackend)
	item := m.current()
	if !ok || item == nil || item.Local == nil || !m.actionReady() {
		return nil
	}
	if m.associations != nil {
		m.associations.cancel()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	view := &associationView{item: *item, status: "Loading saved uploads…", checks: map[string]string{}, cancel: cancel, ctx: ctx}
	m.associations = view
	m.mode = "associations"
	return func() tea.Msg {
		receipts, warnings, err := backend.SavedUploads(ctx, item.ID())
		return associationsLoaded{view, receipts, warnings, err}
	}
}
func (m *model) associationUpdate(msg tea.Msg) (bool, tea.Cmd) {
	switch value := msg.(type) {
	case badgesLoaded:
		if value.epoch != m.badgeEpoch || value.listEpoch != m.listEpoch {
			return true, nil
		}
		m.listWarnings = append(m.listWarnings, value.warnings...)
		if value.err != nil {
			m.listWarnings = append(m.listWarnings, "Could not refresh pins and receipts")
		}
		if value.err == nil {
			m.currentBadges = &value
			for n := range m.items {
				item := &m.items[n]
				if item.Local != nil {
					e := *item.Local
					e.Pinned = value.value.Pins[e.ID]
					e.UploadCount = value.value.Uploads[e.ID]
					e.Enriched = value.value.Known
					item.Local = &e
				}
			}
			m.filter()
		}
		return true, nil
	case associationsLoaded:
		if value.owner != m.associations {
			return true, nil
		}
		v := m.associations
		v.receipts = value.receipts
		v.status = fmt.Sprintf("%d saved uploads for this location; current contents may differ", len(v.receipts))
		if len(value.warnings) > 0 {
			v.status += " · " + strings.Join(value.warnings, "; ")
		}
		if value.err != nil {
			v.status = value.err.Error()
		}
		return true, nil
	case associationResult:
		if value.owner != m.associations || value.epoch != m.associations.epoch {
			return true, nil
		}
		v := m.associations
		v.status = value.text
		if value.err != nil {
			v.status = value.err.Error()
		}
		if value.id != "" {
			v.checks[value.id] = v.status
		}
		return true, nil
	}
	if m.mode != "associations" {
		return false, nil
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}
	v := m.associations
	switch key.String() {
	case "esc", "q", "a":
		v.cancel()
		m.associations = nil
		m.mode = ""
		return true, nil
	case "ctrl+c":
		return true, tea.Quit
	case "up", "k":
		v.cursor = max(0, v.cursor-1)
	case "down", "j":
		v.cursor = min(max(0, len(v.receipts)-1), v.cursor+1)
	case "c", "o", "v":
		if len(v.receipts) == 0 {
			return true, nil
		}
		receipt := v.receipts[v.cursor]
		backend := m.backend.(AssociationBackend)
		v.epoch++
		epoch := v.epoch
		v.status = "Working…"
		return true, func() tea.Msg {
			result := associationResult{owner: v, epoch: epoch}
			if key.String() == "v" {
				result.id = receipt.ID
				result.text, result.err = backend.CheckUpload(v.ctx, receipt.Target())
			} else {
				link, err := backend.SavedLink(receipt.ID, v.item.ID())
				result.err = err
				if err == nil {
					if key.String() == "c" {
						result.err = clipboard.WriteAll(link)
						result.text = "Playback link copied"
					} else if opener, ok := m.backend.(interface{ OpenLink(string) error }); ok {
						result.err = opener.OpenLink(link)
						result.text = "Playback opened"
					} else {
						result.err = fmt.Errorf("opening links is unavailable")
					}
				}
			}
			return result
		}
	}
	return true, nil
}
func (m *model) associationScreen() tea.View {
	v := m.associations
	rows := []string{v.item.Title(), "", v.status, ""}
	start := max(0, v.cursor-2)
	for n := start; n < len(v.receipts) && n < start+5; n++ {
		r := v.receipts[n]
		mark := "  "
		if n == v.cursor {
			mark = "› "
		}
		visibility := "secret"
		if r.Public {
			visibility = "public"
		}
		encryption := "plaintext"
		if r.Encrypted {
			encryption = "encrypted"
		}
		rows = append(rows, mark+r.Target(), fmt.Sprintf("  %s · %s · %s · %s", time.Unix(0, r.Created).Format(time.RFC3339), visibility, encryption, fallback(v.checks[r.ID], "Not checked")))
	}
	content := pane("Saved uploads", wrapped(strings.Join(rows, "\n"), max(1, m.width-2)), m.width, max(3, m.height-2), true) + "\nc Copy playback link · o Open playback · v Check availability · Esc Back"
	view := tea.NewView(termui.Fit(content, m.width, m.height))
	view.AltScreen = true
	return view
}
