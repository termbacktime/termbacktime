package storage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/uploadqueue"
	"golang.org/x/sys/unix"
)

func put(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func capture(t *testing.T, root, name string) library.Entry {
	t.Helper()
	path := filepath.Join(root, "recordings", name+".tbt")
	os.MkdirAll(filepath.Dir(path), 0700)
	if err := recording.Save(path, &recording.Recording{Sizes: []int{80, 24}, Lines: []recording.Event{{Lines: []string{"hello"}}}}); err != nil {
		t.Fatal(err)
	}
	e, err := (library.Library{Root: root}).Register(path)
	if err != nil {
		t.Fatal(err)
	}
	return *e
}
func snapshot(t *testing.T, s Store) Report {
	t.Helper()
	r, e := s.Snapshot(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestPreviewApplyPinsImportsAndProtectedFiles(t *testing.T) {
	root := t.TempDir()
	s := Store{root}
	lib := library.Library{Root: root}
	a := capture(t, root, "first")
	b := capture(t, root, "pinned")
	if err := lib.SetPinned(b.ID, true); err != nil {
		t.Fatal(err)
	}
	external := capture(t, t.TempDir(), "external")
	if _, err := lib.Register(external.Path); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "exports", "page.html"), []byte("export"))
	put(t, filepath.Join(root, "config.json"), []byte("support"))
	put(t, filepath.Join(root, "shares", "receipt.json"), []byte("keep receipt"))
	put(t, filepath.Join(root, "recordings", "old.json"), []byte(`{"s":[80,24]}`))
	writer, err := recording.NewWriter(filepath.Join(root, "recordings", "active.tbt"), recording.Recording{Sizes: []int{80, 24}})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := os.Symlink(external.Path, filepath.Join(root, "exports", "link")); err != nil {
		t.Fatal(err)
	}
	r := snapshot(t, s)
	selected := r.Select(Filter{}, time.Now())
	if len(selected) != 2 || r.Totals["external"].Files != 1 {
		t.Fatal(selected, r.Totals)
	}
	st, _ := os.Stat(b.Path)
	if err := lib.Delete(b, st); err == nil {
		t.Fatal("deleted pinned recording")
	}
	if _, err := lib.List(); err != nil {
		t.Fatal(err)
	}
	pinned, _ := lib.Pinned(b.ID)
	if !pinned {
		t.Fatal("listing lost pin")
	}
	results, err := s.Apply(t.Context(), selected)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if !r.Deleted {
			t.Fatal(r)
		}
	}
	if _, err := os.Stat(a.Path); !os.IsNotExist(err) {
		t.Fatal("preview/apply disagreement")
	}
	for _, p := range []string{b.Path, external.Path, filepath.Join(root, "recordings", "active.tbt.partial"), filepath.Join(root, "recordings", "old.json"), filepath.Join(root, "shares", "receipt.json")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal("protected file lost", p, err)
		}
	}
	if err := lib.SetPinned(b.ID, false); err != nil {
		t.Fatal(err)
	}
	if len(snapshot(t, s).Select(Filter{Kind: "recordings"}, time.Now())) != 1 {
		t.Fatal("unpin failed")
	}
}
func TestFiltersReplacementsAndPartialFailures(t *testing.T) {
	root := t.TempDir()
	s := Store{root}
	a := filepath.Join(root, "exports", "a")
	b := filepath.Join(root, "exports", "b")
	put(t, a, []byte("123456"))
	put(t, b, []byte("12"))
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(a, old, old)
	r := snapshot(t, s)
	if got := r.Select(Filter{Kind: "exports", OlderThan: 24 * time.Hour, MinSize: 4}, time.Now()); len(got) != 1 || got[0].Path != a {
		t.Fatal(got)
	}
	selected := r.Select(Filter{}, time.Now())
	replacement := filepath.Join(root, "replacement")
	put(t, replacement, []byte("123456"))
	os.Chtimes(replacement, old, old)
	os.Rename(replacement, a)
	out, err := s.Apply(t.Context(), selected)
	if err != nil || len(out) != 2 || out[0].Deleted || !out[1].Deleted {
		t.Fatal(out, err)
	}
	data, _ := os.ReadFile(a)
	if string(data) != "123456" {
		t.Fatal("replacement deleted")
	}
	selected = snapshot(t, s).Select(Filter{}, time.Now())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Apply(ctx, selected); err == nil {
		t.Fatal("ignored cancellation")
	}
	target := filepath.Join(t.TempDir(), "target")
	put(t, target, []byte("keep"))
	os.Remove(a)
	os.Symlink(target, a)
	if out, err := s.Apply(t.Context(), selected); err != nil || out[0].Deleted {
		t.Fatal(out, err)
	}
	if data, _ := os.ReadFile(target); string(data) != "keep" {
		t.Fatal("followed symlink")
	}
}
func TestQueueLockUnresolvedJobsAndReceiptPreservation(t *testing.T) {
	root := t.TempDir()
	s := Store{root}
	states := []string{"complete", "canceled", "pending", "failed", "needs attention", "complete"}
	var encryptedID string
	for i, state := range states {
		id := recording.NewID()
		j := uploadqueue.Job{Version: 2, ID: id, State: state, Encrypted: i == 5, Result: "https://site.example/p/" + strings.Repeat("a", 32)}
		if i == 5 {
			encryptedID = id
		}
		b, _ := json.Marshal(j)
		dir := filepath.Join(root, "queue", id)
		put(t, filepath.Join(dir, "job.json"), b)
		put(t, filepath.Join(dir, "request.json"), []byte("request"))
		put(t, filepath.Join(dir, "key.txt"), []byte("private-key"))
	}
	selected := snapshot(t, s).Select(Filter{Kind: "queue"}, time.Now())
	if len(selected) != 2 {
		t.Fatal(selected)
	}
	unlock, err := (uploadqueue.Store{Root: root}).CleanupLock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(t.Context(), selected); err == nil {
		t.Fatal("ignored queue lock")
	}
	unlock()
	receipt := library.Receipt{Encrypted: true, Link: "https://site.example/p/" + strings.Repeat("a", 32) + "#k=private-key"}
	b, _ := json.Marshal(receipt)
	path := filepath.Join(root, "shares", "receipt.json")
	put(t, path, b)
	selected = snapshot(t, s).Select(Filter{Kind: "queue"}, time.Now())
	if len(selected) != 3 {
		t.Fatal(selected)
	}
	out, err := s.Apply(t.Context(), selected)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range out {
		if !r.Deleted {
			t.Fatal(r)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "queue", encryptedID)); !os.IsNotExist(err) {
		t.Fatal("job not removed")
	}
	if data, _ := os.ReadFile(path); string(data) != string(b) {
		t.Fatal("receipt lost")
	}
	if snapshot(t, s).Totals["queue"].Files < 9 {
		t.Fatal("unresolved jobs removed")
	}
}
func TestCleanupHonorsFileLocksAndNewPins(t *testing.T) {
	root := t.TempDir()
	s := Store{root}
	e := capture(t, root, "capture")
	preview := snapshot(t, s).Select(Filter{}, time.Now())
	f, _ := os.Open(e.Path)
	defer f.Close()
	unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	out, err := s.Apply(t.Context(), preview)
	if err != nil || out[0].Deleted {
		t.Fatal(out, err)
	}
	unix.Flock(int(f.Fd()), unix.LOCK_UN)
	if err := (library.Library{Root: root}).SetPinned(e.ID, true); err != nil {
		t.Fatal(err)
	}
	out, err = s.Apply(t.Context(), preview)
	if err != nil || out[0].Deleted {
		t.Fatal(out, err)
	}
}

func TestImportedFileInExportsRemainsExternal(t *testing.T) {
	root := t.TempDir()
	lib := library.Library{Root: root}
	path := filepath.Join(root, "exports", "imported.tbt")
	if err := recording.Save(path, &recording.Recording{Sizes: []int{80, 24}}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Register(path); err != nil {
		t.Fatal(err)
	}
	report := snapshot(t, Store{root})
	if len(report.Select(Filter{}, time.Now())) != 0 || report.Totals["external"].Files != 1 || report.Totals["exports"].Files != 0 {
		t.Fatal(report)
	}
}

func TestPlaybackHistoryCountsAsProtectedSupportStorage(t *testing.T) {
	root := t.TempDir()
	put(t, filepath.Join(root, "history", "state.json"), []byte(`{"version":1,"enabled":false,"entries":{}}`))
	put(t, filepath.Join(root, "history", "lock"), []byte{})
	report := snapshot(t, Store{root})
	if report.Totals["protected"].Files != 2 || report.Totals["protected"].Bytes == 0 {
		t.Fatal(report.Totals)
	}
	for _, item := range report.Items {
		if strings.Contains(item.Path, "history") && item.Eligible {
			t.Fatal("history eligible for cleanup")
		}
	}
}
