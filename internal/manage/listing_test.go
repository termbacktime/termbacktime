package manage

import (
	tea "charm.land/bubbletea/v2"
	"context"
	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/sealed"
	"strings"
	"testing"
	"time"
)

type streamingFixture struct{ fakeStore }

func (s *streamingFixture) StreamList(context.Context, func(library.ListUpdate) error) error {
	return nil
}

func TestLateListingResultsCannotReplaceRowsBehindOtherScreens(t *testing.T) {
	for _, mode := range []string{"queue", "storage", "upload", "play", ""} {
		t.Run(mode, func(t *testing.T) {
			m := newModel(t.Context(), &streamingFixture{fakeStore{items: sampleItems()}}, false, nil)
			m.items = sampleItems()
			m.filter()
			m.refresh(false)
			old := m.listing
			m.refresh(false)
			m.mode = mode
			before := m.current().ID()
			m.update(listingMsg{owner: old, update: library.ListUpdate{Begin: true, Entries: []library.Entry{{ID: "stale", Title: "Deleted recording"}}}})
			m.update(listingMsg{owner: old, finished: true})
			if m.current() == nil || m.current().ID() != before || !m.loading || m.mode != mode {
				t.Fatal("late results altered current screen")
			}
			m.cancelList()
		})
	}
}

func TestCachedRowsAreReadOnlyAndFiltersRemainUsableDuringRefresh(t *testing.T) {
	m := newModel(t.Context(), &streamingFixture{fakeStore{items: sampleItems()}}, false, nil)
	m.refresh(false)
	defer m.cancelList()
	owner := m.listing
	m.update(listingMsg{owner: owner, update: library.ListUpdate{Begin: true}})
	entries := []library.Entry{*sampleItems()[0].Local, *sampleItems()[1].Local}
	m.update(listingMsg{owner: owner, update: library.ListUpdate{Entries: entries}})
	if m.actionReady() {
		t.Fatal("cached identity authorized an action")
	}
	press(m, "down")
	selected := m.current().ID()
	press(m, "f")
	if m.mode != "filters" {
		t.Fatal("refresh blocked filters")
	}
	press(m, "esc")
	entries[1].Checked = true
	m.update(listingMsg{owner: owner, update: library.ListUpdate{Entries: entries[1:]}})
	if m.current().ID() != selected || !m.actionReady() {
		t.Fatal("checked update did not retain selection/enable action")
	}
	m.width, m.height = 40, 16
	m.mode = "filters"
	m.filterCursor = 9
	if !strings.Contains(m.View().Content, "invalid") {
		t.Fatal("selected filter clipped in small terminal")
	}
}

func TestGitHubFiltersUseOnlyLoadedMetadata(t *testing.T) {
	m, _ := readyModel(t)
	a := gh.GistSummary{ID: "a", Description: "Alpha", Public: true, Updated: time.Unix(1, 0), Files: map[string]struct {
		Size int64 `json:"size"`
	}{gh.Filename: {Size: 200}, "README.md": {Size: 400}}}
	b := gh.GistSummary{ID: "b", Description: "Beta", Public: false, Updated: time.Unix(2, 0), Files: map[string]struct {
		Size int64 `json:"size"`
	}{sealed.Filename: {Size: 300}}}
	m.source = GistSource
	m.items = []Item{{Gist: &a}, {Gist: &b}}
	m.next = 3
	m.filters[1] = viewFilter{Filter: library.Filter{SortBy: "size"}}
	m.filter()
	if len(m.visible) != 2 || m.items[0].ID() != "a" || m.next != 3 {
		t.Fatal("Gist total size sorting changed page state")
	}
	m.selectedFilters().Encryption = "encrypted"
	m.selectedFilters().Visibility = "secret"
	m.filter()
	if len(m.visible) != 1 || m.current().ID() != "b" || m.next != 3 {
		t.Fatal("loaded metadata filters failed")
	}
}

type verificationFixture struct{ fakeStore }

func (*verificationFixture) Verify(ctx context.Context, _ Item, progress func(int, int)) (string, error) {
	progress(256, 1024)
	<-ctx.Done()
	return "", ctx.Err()
}
func TestVerificationLeavesListNavigableAndCannotReleaseOtherScreens(t *testing.T) {
	m := newModel(t.Context(), &verificationFixture{fakeStore{items: sampleItems()}}, false, nil)
	m.Update(m.Init()())
	commands := m.verifySelected()().(tea.BatchMsg)
	result := make(chan tea.Msg, 1)
	go func() { result <- commands[0]() }()
	m.update(commands[1]())
	if m.busy || m.mode != "" || !strings.Contains(m.listingStatus(), "256 / 1024") {
		t.Fatal("verification took list focus")
	}
	press(m, "down")
	if m.current().Title() != "Beta" {
		t.Fatal("verification blocked navigation")
	}
	m.mode = "queue"
	m.busy = true
	m.verification.cancel()
	m.update(<-result)
	if m.mode != "queue" || !m.busy || m.verification.running {
		t.Fatal("verification completion released another screen")
	}
}

func TestListingDoesNotOverwriteReceiptsSavedDuringRefresh(t *testing.T) {
	m := newModel(t.Context(), &streamingFixture{fakeStore{items: sampleItems()}}, false, nil)
	m.refresh(false)
	defer m.cancelList()
	entry := *sampleItems()[0].Local
	m.update(listingMsg{owner: m.listing, update: library.ListUpdate{Begin: true, Entries: []library.Entry{entry}}})
	m.update(badgesLoaded{listEpoch: m.listEpoch, epoch: m.badgeEpoch, value: library.Enrichment{Known: true, Pins: map[string]bool{entry.ID: true}, Uploads: map[string]int{entry.ID: 2}}})
	// Validation began with older pin/receipt maps and finishes after the upload.
	entry.Checked, entry.Enriched = true, true
	m.update(listingMsg{owner: m.listing, update: library.ListUpdate{Entries: []library.Entry{entry}}})
	if !m.current().Local.Pinned || m.current().Local.UploadCount != 2 {
		t.Fatal("late validation overwrote current associations")
	}
}
