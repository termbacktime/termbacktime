package cmd

import (
	"bytes"
	"encoding/json"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListFiltersKeepJSONCompatibleAndWarnOnBrokenEntries(t *testing.T) {
	dir := t.TempDir()
	lib := library.Library{Root: dir}
	for n, title := range []string{"Beta", "Alpha"} {
		path := filepath.Join(dir, "recordings", title+".tbt")
		if err := recording.Save(path, &recording.Recording{Title: title, Sizes: []int{80, 24}}); err != nil {
			t.Fatal(err)
		}
		entry, err := lib.Register(path)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			if err := lib.SetPinned(entry.ID, true); err != nil {
				t.Fatal(err)
			}
			if err := lib.SaveReceipt(path, "https://site.test/p/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa#k=private"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "library", "broken.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	root := NewRoot()
	var out, warnings bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&warnings)
	root.SetArgs([]string{"--data-dir", dir, "list", "--sort", "title", "--status", "ready,partial", "--pinned=false", "--uploaded=false", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["title"] != "Alpha" || rows[0]["pinned"] != false || rows[0]["receipt_count"] != float64(0) {
		t.Fatal(rows)
	}
	if !strings.Contains(warnings.String(), "broken.json") || strings.Contains(out.String(), "#k=") {
		t.Fatal(out.String(), warnings.String())
	}
}
