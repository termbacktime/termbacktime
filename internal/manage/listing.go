package manage

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
	"os"
	"strings"
)

type ListingBackend interface {
	StreamList(context.Context, func(library.ListUpdate) error) error
}

func (s *Store) StreamList(ctx context.Context, emit func(library.ListUpdate) error) error {
	return s.Library.StreamList(ctx, emit)
}

type listingSession struct {
	selection string
	epoch     int
	ctx       context.Context
	updates   chan listingMsg
}
type listingMsg struct {
	owner    *listingSession
	update   library.ListUpdate
	err      error
	finished bool
}

func (s *listingSession) next() tea.Cmd {
	return func() tea.Msg {
		select {
		case result := <-s.updates:
			return result
		case <-s.ctx.Done():
			return nil
		}
	}
}
func (m *model) startListing(ctx context.Context, backend ListingBackend) tea.Cmd {
	session := &listingSession{epoch: m.listEpoch, ctx: ctx, updates: make(chan listingMsg, 2)}
	if item := m.current(); item != nil {
		session.selection = item.ID()
	}
	m.listing = session
	m.listWarnings = nil
	for n := range m.items {
		m.items[n].pending = true
	}
	return func() tea.Msg {
		go func() {
			err := backend.StreamList(ctx, func(update library.ListUpdate) error {
				select {
				case session.updates <- listingMsg{owner: session, update: update}:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			select {
			case session.updates <- listingMsg{owner: session, err: err, finished: true}:
			case <-ctx.Done():
			}
		}()
		return session.next()()
	}
}
func (m *model) listingUpdate(msg tea.Msg) (bool, tea.Cmd) {
	value, ok := msg.(listingMsg)
	if !ok {
		return false, nil
	}
	if value.owner != m.listing || value.owner.epoch != m.listEpoch {
		return true, nil
	}
	if value.finished {
		m.loading = false
		m.listing = nil
		if value.err != nil {
			m.status = "Could not finish refresh: " + value.err.Error()
		} else if (!m.busy && m.mode == "") || m.status == "Loading recordings…" {
			m.status = fmt.Sprintf("%d recordings loaded", len(m.items))
			if len(m.listWarnings) > 0 {
				m.status += fmt.Sprintf(" · %d warnings: %s", len(m.listWarnings), m.listWarnings[0])
			}
		}
		return true, nil
	}
	update := value.update
	if update.Begin {
		m.items = nil
		m.visible = nil
		m.details = ""
	}
	oldID := ""
	oldPending := false
	if item := m.current(); item != nil {
		oldID = item.ID()
		oldPending = item.pending
	}
	if value.owner.selection != "" {
		oldID = value.owner.selection
	}
	positions := make(map[string]int, len(m.items))
	for n, item := range m.items {
		positions[item.ID()] = n
	}
	for _, e := range update.Entries {
		if badges := m.currentBadges; badges != nil {
			e.Pinned = badges.value.Pins[e.ID]
			e.UploadCount = badges.value.Uploads[e.ID]
			e.Enriched = badges.value.Known
		}
		item := Item{Local: &e, pending: !e.Checked}
		if e.Checked {
			item.identity, _ = os.Lstat(item.Path())
		}
		if n, ok := positions[e.ID]; ok {
			m.items[n] = item
		} else {
			positions[e.ID] = len(m.items)
			m.items = append(m.items, item)
		}
	}
	m.listWarnings = append(m.listWarnings, update.Warnings...)
	m.listProgress = fmt.Sprintf("Checking recordings %d/%d", update.Checked, update.Total)
	m.filter()
	for n, index := range m.visible {
		if m.items[index].ID() == oldID {
			m.selected = n
			value.owner.selection = ""
			break
		}
	}
	var details tea.Cmd
	if current := m.current(); current != nil && !m.busy && m.mode == "" && (oldID != current.ID() || oldPending && !current.pending) {
		details = m.selectItem(false)
	}
	return true, tea.Batch(value.owner.next(), details)
}
func (m *model) actionReady() bool {
	item := m.current()
	if item == nil {
		return false
	}
	if item.pending || (m.source != LocalSource) && m.loading {
		m.status = "Wait until this recording has been checked"
		return false
	}
	return true
}

type VerificationBackend interface {
	Verify(context.Context, Item, func(int, int)) (string, error)
}

func (s *Store) Verify(ctx context.Context, item Item, progress func(int, int)) (string, error) {
	if item.Local == nil {
		return "", fmt.Errorf("full verification is available for local recordings")
	}
	summary, err := recording.Verify(ctx, item.Path(), progress)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Verified %d events · %s", summary.Events, item.Title()), nil
}

func (m *model) listingStatus() string {
	if m.verification != nil {
		return m.verification.status
	}
	if m.loading && m.listing != nil {
		return m.listProgress
	}
	return strings.TrimSpace(m.status)
}
