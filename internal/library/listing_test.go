package library

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/termbacktime/termbacktime/internal/recording"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStreamListsCachedRowsBeforeValidationAndCancellationDoesNotWrite(t *testing.T) {
	lib := Library{Root: t.TempDir()}
	path, _ := lib.Output("")
	if err := recording.Save(path, &recording.Recording{Sizes: []int{80, 24}, Title: "cached"}); err != nil {
		t.Fatal(err)
	}
	entry, err := lib.Register(path)
	if err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(lib.Root, "library", entry.ID+".json")
	before, _ := os.Stat(index)
	if err := os.WriteFile(path, []byte("changed unsupported data"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cached := false
	err = lib.StreamList(ctx, func(update ListUpdate) error {
		for _, e := range update.Entries {
			if e.Title == "cached" && !e.Checked {
				cached = true
				cancel()
			}
		}
		return nil
	})
	if !cached || !errors.Is(err, context.Canceled) {
		t.Fatal("no cancellable cached snapshot", cached, err)
	}
	after, _ := os.Stat(index)
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("canceled refresh published an index")
	}
}
func TestStreamWarningsFiltersAndReceiptEnrichment(t *testing.T) {
	lib := Library{Root: t.TempDir()}
	path, _ := lib.Output("")
	if err := recording.Save(path, &recording.Recording{Sizes: []int{80, 24}}); err != nil {
		t.Fatal(err)
	}
	entry, err := lib.Register(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.SetPinned(entry.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := lib.SaveReceipt(path, "https://site.test/p/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa#k=private"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib.Root, "library", "broken.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	warnings := 0
	var loaded Entry
	err = lib.StreamList(t.Context(), func(update ListUpdate) error {
		warnings += len(update.Warnings)
		for _, e := range update.Entries {
			if e.Checked {
				loaded = e
			}
		}
		return nil
	})
	if err != nil || warnings != 1 || !loaded.Pinned || loaded.UploadCount != 1 || !loaded.Enriched {
		t.Fatal(loaded, warnings, err)
	}
	yes, no := true, false
	if !(Filter{Pinned: &yes, Uploaded: &yes, Statuses: []string{"ready", "partial"}}).Match(loaded) {
		t.Fatal("combined filters rejected match")
	}
	if (Filter{Pinned: &no}).Match(loaded) {
		t.Fatal("negative pin filter ignored")
	}
	loaded.Enriched = false
	if (Filter{Uploaded: &no}).Match(loaded) {
		t.Fatal("unknown uploads treated as absent")
	}
}

func TestReceiptAssociationsKeepPrivateLinksOutOfSummaries(t *testing.T) {
	lib := Library{Root: t.TempDir()}
	path, _ := lib.Output("")
	if err := recording.Save(path, &recording.Recording{Sizes: []int{80, 24}}); err != nil {
		t.Fatal(err)
	}
	entry, err := lib.Register(path)
	if err != nil {
		t.Fatal(err)
	}
	links := []string{"https://site.test/p/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa#k=private-one", "https://site.test/p/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb#k=private-two"}
	for _, link := range links {
		if err := lib.SaveReceipt(path, link, Receipt{Encrypted: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(lib.Root, "shares", "cccccccccccccccccccccccccccccccc.json"), []byte(`{"link":"secret malformed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	rows, warnings, err := lib.Receipts(t.Context(), entry.ID)
	if err != nil || len(rows) != 2 || len(warnings) != 1 || rows[0].GistID != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatal(rows, warnings, err)
	}
	data, _ := json.Marshal(rows)
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "#k=") {
		t.Fatal("private link leaked")
	}
	if link, err := lib.ReceiptLink(rows[0].ID, entry.ID); err != nil || link != links[1] {
		t.Fatal(link, err)
	}
	if _, err := lib.ReceiptLink(rows[0].ID, "dddddddddddddddddddddddddddddddd"); err == nil {
		t.Fatal("wrong location resolved a link")
	}
	if err := os.WriteFile(path, []byte("replaced contents"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, _, err = lib.Receipts(t.Context(), entry.ID)
	if err != nil || len(rows) != 2 {
		t.Fatal("replacement lost upload history")
	}
}
func TestSortUsesDefaultsAndStableIDTieBreakers(t *testing.T) {
	input := []Entry{{ID: "c", Title: "Beta", Started: 1, Duration: 2, Bytes: 3}, {ID: "b", Title: "Alpha", Started: 2, Duration: 1, Bytes: 2}, {ID: "a", Title: "Alpha", Started: 2, Duration: 1, Bytes: 2}}
	for _, test := range []struct{ by, order, ids string }{{"date", "", "abc"}, {"title", "", "abc"}, {"duration", "", "cab"}, {"size", "asc", "abc"}, {"title", "desc", "cab"}} {
		rows := append([]Entry(nil), input...)
		Sort(rows, test.by, test.order)
		got := ""
		for _, e := range rows {
			got += e.ID
		}
		if got != test.ids {
			t.Fatal(test, got)
		}
	}
}
