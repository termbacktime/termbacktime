package library

import (
	"bytes"
	"github.com/termbacktime/termbacktime/internal/recording"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestExternalRecordingLifecycle(t *testing.T) {
	l := Library{Root: filepath.Join(t.TempDir(), "data")}
	path := filepath.Join(t.TempDir(), "nested", "demo.json")
	w, err := recording.NewWriter(path, recording.Recording{Title: "demo", Sizes: []int{80, 24}})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(recording.Event{Time: 42, Lines: []string{"hello"}}); err != nil {
		t.Fatal(err)
	}
	e, err := l.Register(path + ".partial")
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != "recording" {
		t.Fatal(e.Status)
	}
	if err := recording.RecoverFile(path+".partial", path+".recovered"); err == nil {
		t.Fatal("recovered active journal")
	}
	if err := w.Finish(); err != nil {
		t.Fatal(err)
	}
	resolved, err := l.Resolve(e.ID[:12])
	if err != nil || resolved != path {
		t.Fatal(resolved, err)
	}
	entries, err := l.List()
	if err != nil || len(entries) != 1 || entries[0].Duration != 42 || entries[0].Status != "ready" {
		t.Fatal(entries, err)
	}
	if err := l.SaveReceipt(path, "https://example.test/p/id#k=secret"); err != nil {
		t.Fatal(err)
	}
	link, err := l.ShareLink(path)
	if err != nil || link == "" {
		t.Fatal(link, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	entries, err = l.List()
	if err != nil || entries[0].Status != "missing" {
		t.Fatal(entries, err)
	}
}

func TestConcurrentDiscoveryPermissionsAndAmbiguousPrefixes(t *testing.T) {
	l := Library{Root: filepath.Join(t.TempDir(), "data")}
	if err := l.Init(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 20)
	for range 20 {
		wg.Go(func() {
			path, _ := l.Output("")
			if err := recording.Save(path, &recording.Recording{ID: recording.NewID(), Sizes: []int{80, 24}, Title: "concurrent"}); err != nil {
				failures <- err
				return
			}
			_, err := l.Register(path)
			if err != nil {
				failures <- err
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	entries, err := l.List()
	if err != nil || len(entries) != 20 {
		t.Fatal(entries, err)
	}
	prefixes := map[string]bool{}
	for _, e := range entries {
		p := e.ID[:1]
		if prefixes[p] {
			if _, err := l.Resolve(p); err == nil || !strings.Contains(err.Error(), "ambiguous") {
				t.Fatal("ambiguous prefix accepted")
			}
			break
		}
		prefixes[p] = true
	}
	filepath.Walk(l.Root, func(p string, st os.FileInfo, e error) error {
		if e != nil {
			t.Error(e)
			return nil
		}
		mode := os.FileMode(0600)
		if st.IsDir() {
			mode = 0700
		}
		if st.Mode().Perm() != mode {
			t.Errorf("%s permissions %o", p, st.Mode().Perm())
		}
		return nil
	})
}
func TestRecoveryKeepsSourceAndRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.json")
	w, err := recording.NewWriter(path, recording.Recording{Sizes: []int{80, 24}})
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Append(recording.Event{Time: 42, Lines: []string{"retained"}}); err != nil {
		t.Fatal(err)
	}
	w.Close()
	before, _ := os.ReadFile(path + ".partial")
	out := path + ".recovered.json"
	if err := recording.RecoverFile(path+".partial", out); err != nil {
		t.Fatal(err)
	}
	if err := recording.RecoverFile(path+".partial", out); err == nil {
		t.Fatal("overwrote existing recording")
	}
	after, _ := os.ReadFile(path + ".partial")
	if !bytes.Equal(before, after) {
		t.Fatal("source changed")
	}
}
