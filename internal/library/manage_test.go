package library

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/termbacktime/termbacktime/internal/recording"
)

func TestManageDeletionKeepsOtherFilesAndShareKeys(t *testing.T) {
	l := Library{Root: filepath.Join(t.TempDir(), "data")}
	p := filepath.Join(t.TempDir(), "external.json")
	if err := recording.Save(p, &recording.Recording{Title: "external", Sizes: []int{80, 24}}); err != nil {
		t.Fatal(err)
	}
	e, err := l.Register(p)
	if err != nil {
		t.Fatal(err)
	}
	link := "https://site.test/p/0123456789abcdef0123456789abcdef#k=private"
	if err = l.SaveReceipt(p, link); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(p)
	if err = l.Delete(*e, info); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	entries, err := l.List()
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
	got, err := l.GistShareLink("0123456789abcdef0123456789abcdef")
	if err != nil || got != link {
		t.Fatal("lost encryption receipt", err)
	}
}

func TestManageDeleteRefusesActiveChangedReplacedAndSymlinkFiles(t *testing.T) {
	for _, scenario := range []string{"active", "changed", "replaced", "symlink", "reappeared"} {
		t.Run(scenario, func(t *testing.T) {
			l := Library{Root: t.TempDir()}
			p, _ := l.Output("")
			w, err := recording.NewWriter(p, recording.Recording{Sizes: []int{80, 24}})
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			e, err := l.Register(p + ".partial")
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "active" {
				e.Status = "partial"
				info, _ := os.Lstat(p + ".partial")
				if err = l.Delete(*e, info); err == nil {
					t.Fatal("deleted active journal")
				}
				return
			}
			if err = w.Finish(); err != nil {
				t.Fatal(err)
			}
			e, err = l.Register(p)
			if err != nil {
				t.Fatal(err)
			}
			info, _ := os.Lstat(p)
			switch scenario {
			case "changed":
				os.WriteFile(p, []byte("changed"), 0600)
			case "replaced":
				os.Rename(p, p+".original")
				os.WriteFile(p, []byte("replacement"), 0600)
			case "symlink":
				os.Rename(p, p+".original")
				os.Symlink(p+".original", p)
			case "reappeared":
				e.Status = "missing"
			}
			if err = l.Delete(*e, info); err == nil {
				t.Fatal("deleted a changed file")
			}
			if _, err = os.Lstat(p); err != nil {
				t.Fatal("file disappeared", err)
			}
		})
	}
}

func TestManageDeletesInactiveJournalsAndForgetsMissingEntries(t *testing.T) {
	for _, scenario := range []string{"partial", "invalid", "unsupported", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			l := Library{Root: t.TempDir()}
			p, _ := l.Output("")
			w, err := recording.NewWriter(p, recording.Recording{Sizes: []int{80, 24}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = l.Register(p + ".partial"); err != nil {
				t.Fatal(err)
			}
			w.Close()
			if scenario == "invalid" {
				os.WriteFile(p+".partial", []byte("invalid"), 0600)
			}
			if scenario == "unsupported" {
				if err := os.WriteFile(p+".partial", []byte("{\"format\":\"tbt-journal-v99\",\"recording\":{\"s\":[80,24]}}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "missing" {
				os.Remove(p + ".partial")
			}
			entries, err := l.List()
			if err != nil || len(entries) != 1 || entries[0].Status != scenario {
				t.Fatal(entries, err)
			}
			info, _ := os.Lstat(p + ".partial")
			if err = l.Delete(entries[0], info); err != nil {
				t.Fatal(err)
			}
			entries, err = l.List()
			if err != nil || len(entries) != 0 {
				t.Fatal(entries, err)
			}
		})
	}
}
