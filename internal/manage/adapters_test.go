package manage

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/uploadqueue"
)

func TestStoreLocalHistoryReceiptsBadgesAndVerification(t *testing.T) {
	store := &Store{Library: library.Library{Root: t.TempDir()}}
	path := filepath.Join(t.TempDir(), "recording.tbt")
	if err := recording.Save(path, &recording.Recording{Sizes: []int{80, 24}, Lines: []recording.Event{{Lines: []string{"hello"}}}}); err != nil {
		t.Fatal(err)
	}
	entry, err := store.Library.Register(path)
	if err != nil {
		t.Fatal(err)
	}
	privateLink := "https://site.example/p/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa#k=private"
	if err := store.Library.SaveReceipt(path, privateLink, library.Receipt{Encrypted: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.Library.SetPinned(entry.ID, true); err != nil {
		t.Fatal(err)
	}
	receipts, _, err := store.SavedUploads(t.Context(), entry.ID)
	if err != nil || len(receipts) != 1 || receipts[0].Target() != strings.Repeat("a", 32) {
		t.Fatal(receipts, err)
	}
	link, err := store.SavedLink(receipts[0].ID, entry.ID)
	if err != nil || link != privateLink {
		t.Fatal(link, err)
	}
	if _, err := store.SavedLink(receipts[0].ID, strings.Repeat("b", 32)); err == nil {
		t.Fatal("returned key for an unrelated recording")
	}
	badges, _, err := store.Badges(t.Context())
	if err != nil || !badges.Pins[entry.ID] || badges.Uploads[entry.ID] != 1 {
		t.Fatal(badges, err)
	}
	if _, err := store.CheckUpload(t.Context(), receipts[0].Target()); err == nil {
		t.Fatal("checked availability without GitHub")
	}
	items, next, err := store.ListSource(t.Context(), LocalSource, 1)
	if err != nil || len(items) != 1 || next != 0 || items[0].ID() != entry.ID {
		t.Fatal("local source listing changed", items, next, err)
	}
	if _, _, err := store.ListSource(t.Context(), RepoSource, 1); err == nil {
		t.Fatal("opened a repository without credentials")
	}
	playback, err := store.LoadPlayback(t.Context(), Item{Local: entry})
	if err != nil {
		t.Fatal(err)
	}
	defer playback.Source.Close()
	event, err := playback.Source.Event(0)
	if err != nil || strings.Join(event.Lines, "") != "hello" {
		t.Fatal("local playback lost its lazy source", event, err)
	}
	if err := store.HistoryStore().Enable(true); err != nil {
		t.Fatal(err)
	}
	state, err := store.HistoryStore().State()
	if err != nil || !state.Enabled {
		t.Fatal(state, err)
	}
	checked := 0
	text, err := store.Verify(t.Context(), Item{Local: entry}, func(n, total int) {
		checked = n
		if n > total {
			t.Error("invalid verification progress", n, total)
		}
	})
	if err != nil || checked != 1 || !strings.Contains(text, "Verified 1 events") {
		t.Fatal(text, checked, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.Verify(ctx, Item{Local: entry}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestStoreQueueAdaptersRespectCancellationAndMissingJobs(t *testing.T) {
	store := &Store{Library: library.Library{Root: t.TempDir()}}
	jobs, err := store.Jobs(t.Context())
	if err != nil || len(jobs) != 0 {
		t.Fatal(jobs, err)
	}
	if err := store.RunQueue(t.Context(), func(_ uploadqueue.Job) {}); err == nil {
		t.Fatal("ran queue without credentials")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.Jobs(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, action := range []func(context.Context, string) error{store.RetryJob, store.CancelJob} {
		if err := action(ctx, strings.Repeat("a", 32)); !errors.Is(err, context.Canceled) {
			t.Fatal("lost cancellation", err)
		}
		if err := action(t.Context(), strings.Repeat("a", 32)); err == nil {
			t.Fatal("accepted nonexistent job")
		}
	}
}
