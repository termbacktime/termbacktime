package manage

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/termbacktime/termbacktime/internal/history"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/sharing"
)

type preferencesBackend struct {
	fakeStore
	root, checked, opened string
	receipts              []library.ReceiptSummary
	failure               error
}

func (s *preferencesBackend) HistoryStore() history.Store { return history.Store{Root: s.root} }
func (s *preferencesBackend) SavedUploads(context.Context, string) ([]library.ReceiptSummary, []string, error) {
	return s.receipts, []string{"unreadable older receipt"}, s.failure
}
func (s *preferencesBackend) SavedLink(string, string) (string, error) {
	return "https://site.example/p/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa#k=private", s.failure
}
func (s *preferencesBackend) CheckUpload(_ context.Context, target string) (string, error) {
	s.checked = target
	return "available", s.failure
}
func (s *preferencesBackend) Badges(context.Context) (library.Enrichment, []string, error) {
	return library.Enrichment{Known: true, Pins: map[string]bool{s.items[0].ID(): true}, Uploads: map[string]int{s.items[0].ID(): 2}}, nil, s.failure
}
func (s *preferencesBackend) OpenLink(link string) error { s.opened = link; return nil }

func TestManagerSavedUploadsCheckOpenAndCancel(t *testing.T) {
	backend := &preferencesBackend{fakeStore: fakeStore{items: sampleItems()}, root: t.TempDir()}
	receipt := library.ReceiptSummary{ID: strings.Repeat("c", 32), Remote: sharing.Reference{Storage: sharing.Repo, Owner: "alice", ID: strings.Repeat("d", 32)}, Encrypted: true, Public: true}
	backend.receipts = []library.ReceiptSummary{receipt}
	m := newModel(t.Context(), backend, false, nil)
	m.Update(m.Init()())
	m.width, m.height = 100, 24
	m.update(m.refreshBadges()())
	if !m.items[0].Local.Pinned || m.items[0].Local.UploadCount != 2 {
		t.Fatal("badges did not enrich local rows", m.items)
	}
	command := m.openAssociations()
	view := m.associations
	m.update(command())
	screen := m.associationScreen().Content
	if !strings.Contains(screen, receipt.Target()) || !strings.Contains(screen, "encrypted") || strings.Contains(screen, "#k=") {
		t.Fatal("saved upload view missing public facts or exposes key", screen)
	}
	m.update(press(m, "v")())
	if backend.checked != receipt.Target() || view.checks[receipt.ID] != "available" {
		t.Fatal("availability not checked", backend.checked, view.checks)
	}
	m.update(press(m, "o")())
	if !strings.HasSuffix(backend.opened, "#k=private") || strings.Contains(m.associationScreen().Content, "#k=private") {
		t.Fatal("private link not preserved for opening or leaked in view")
	}
	backend.failure = errors.New("network unavailable")
	m.update(press(m, "v")())
	if view.status != backend.failure.Error() {
		t.Fatal(view.status)
	}
	press(m, "esc")
	if view.ctx.Err() != context.Canceled || m.associations != nil || m.mode != "" {
		t.Fatal("closing did not cancel saved-upload work")
	}
	m.update(associationResult{owner: view, epoch: view.epoch, text: "stale"})
	if m.mode != "" {
		t.Fatal("stale result reopened closed view")
	}
}

func TestManagerHistorySettingsAndStaleResults(t *testing.T) {
	backend := &preferencesBackend{fakeStore: fakeStore{items: sampleItems()}, root: t.TempDir()}
	m := newModel(t.Context(), backend, false, nil)
	m.width, m.height = 100, 24
	m.update(m.openHistory()())
	view := m.historySettings
	if view.busy || view.state.Enabled {
		t.Fatal(view)
	}
	for _, key := range []string{"e", "d", "e"} {
		command := press(m, key)
		if duplicate := press(m, key); duplicate != nil {
			t.Fatal("busy settings permitted duplicate writes")
		}
		m.update(command())
		if view.state.Enabled != (key == "e") {
			t.Fatal(view.state)
		}
	}
	if err := backend.HistoryStore().Save(strings.Repeat("a", 64), 10, 100); err != nil {
		t.Fatal(err)
	}
	m.update(press(m, "c")())
	if len(view.state.Entries) != 0 || !view.state.Enabled || !strings.Contains(m.historyScreen().Content, "Enabled") {
		t.Fatal("clear changed opt-in setting", view.state)
	}
	press(m, "esc")
	m.update(historyLoaded{owner: view, text: "stale"})
	if m.historySettings != nil || m.mode != "" {
		t.Fatal("stale result reopened history")
	}
}

func TestManagerFilterControlsCycleAndResetIndependently(t *testing.T) {
	m, _ := readyModel(t)
	m.width, m.height, m.mode = 80, 24, "filters"
	for cursor := range 10 {
		m.filterCursor = cursor
		for range 3 {
			m.filterUpdate(tea.KeyPressMsg{Code: ' '})
		}
	}
	// Three-state booleans return to "any"; toggled statuses remain selected.
	f := m.selectedFilters()
	if f.Pinned != nil || f.Uploaded != nil || len(f.Statuses) != len(statuses) {
		t.Fatal(f)
	}
	m.filterUpdate(tea.KeyPressMsg{Code: 'x'})
	if len(f.Statuses) != 0 || f.SortBy != "" {
		t.Fatal("reset retained a filter", f)
	}
	m.source = GistSource
	m.filterCursor = 2
	m.filterUpdate(tea.KeyPressMsg{Code: ' '})
	m.filterCursor = 3
	m.filterUpdate(tea.KeyPressMsg{Code: ' '})
	if m.selectedFilters().Visibility != "public" || m.selectedFilters().Encryption != "encrypted" || m.filters[LocalSource].Visibility != "" {
		t.Fatal("source filters leaked", m.filters)
	}
	if rows := strings.Join(m.filterRows(), "\n"); !strings.Contains(rows, "public") || !strings.Contains(rows, "encrypted") {
		t.Fatal(rows)
	}
}
