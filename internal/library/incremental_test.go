package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/termbacktime/termbacktime/internal/recording"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestUnchangedIndexIsNotRewrittenAndIdentityInvalidates(t *testing.T) {
	lib := Library{Root: t.TempDir()}
	path, _ := lib.Output("")
	if err := recording.Save(path, &recording.Recording{Sizes: []int{80, 24}, Title: "first"}); err != nil {
		t.Fatal(err)
	}
	entry, err := lib.Register(path)
	if err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(lib.Root, "library", entry.ID+".json")
	before, _ := os.Stat(index)
	for i := 0; i < 3; i++ {
		if _, err := lib.List(); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := os.Stat(index)
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("unchanged listing rewrote index")
	}
	replacement := path + ".new"
	if err := recording.Save(replacement, &recording.Recording{Sizes: []int{80, 24}, Title: "other"}); err != nil {
		t.Fatal(err)
	}
	original, _ := os.Stat(path)
	os.Chtimes(replacement, original.ModTime(), original.ModTime())
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	entries, err := lib.List()
	if err != nil || len(entries) != 1 || entries[0].Title != "other" {
		t.Fatal(entries, err)
	}
	resolved, err := lib.Resolve(entry.ID)
	if err != nil || resolved != path {
		t.Fatal(resolved, err)
	}
}

func BenchmarkLibraryListing(b *testing.B) {
	lib := Library{Root: b.TempDir()}
	recordingValue := &recording.Recording{Sizes: []int{80, 24}}
	for i := 0; i < 5000; i++ {
		recordingValue.Lines = append(recordingValue.Lines, recording.Event{Time: 1, Lines: []string{fmt.Sprintf("line %05d: a representative terminal output line with values 12345\r\n", i)}})
	}
	var paths []string
	for i := 0; i < 20; i++ {
		path, _ := lib.Output("")
		if err := recording.Save(path, recordingValue); err != nil {
			b.Fatal(err)
		}
		paths = append(paths, path)
	}
	if _, err := lib.List(); err != nil {
		b.Fatal(err)
	}
	b.Run("reinspect_all", func(b *testing.B) {
		for b.Loop() {
			for _, path := range paths {
				if _, err := recording.Inspect(path); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
	b.Run("unchanged_index", func(b *testing.B) {
		for b.Loop() {
			if _, err := lib.List(); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Reference is the previous List implementation, with .tbt discovery added.
func BenchmarkLibraryScale(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			lib := Library{Root: b.TempDir()}
			if err := lib.Init(); err != nil {
				b.Fatal(err)
			}
			var data bytes.Buffer
			if err := recording.Encode(&data, &recording.Recording{Sizes: []int{80, 24}, Lines: []recording.Event{{Lines: []string{"benchmark"}}}}); err != nil {
				b.Fatal(err)
			}
			paths := make([]string, count)
			for i := range paths {
				paths[i] = filepath.Join(lib.Root, "recordings", fmt.Sprintf("%05d.tbt", i))
				if err := os.WriteFile(paths[i], data.Bytes(), 0600); err != nil {
					b.Fatal(err)
				}
			}
			if _, err := lib.List(); err != nil {
				b.Fatal(err)
			}
			b.Run("cached_first_batch", func(b *testing.B) {
				for b.Loop() {
					ctx, cancel := context.WithCancel(context.Background())
					seen := 0
					err := lib.StreamList(ctx, func(update ListUpdate) error {
						seen += len(update.Entries)
						if seen > 0 {
							cancel()
						}
						return nil
					})
					cancel()
					if !errors.Is(err, context.Canceled) || seen == 0 {
						b.Fatal(err, seen)
					}
				}
			})

			b.Run("previous_listing", func(b *testing.B) {
				defer func() {
					b.ReportMetric(float64(count*2), "registrations/op")
					b.ReportMetric(float64(count*3), "index-reads/op")
					b.ReportMetric(float64(count*3), "recording-stats/op")
				}()
				for b.Loop() {
					if entries, err := previousList(lib); err != nil || len(entries) != count {
						b.Fatal(err, len(entries))
					}
				}
			})
			b.Run("listing_local_map", func(b *testing.B) {
				defer func() {
					b.ReportMetric(float64(count), "registrations/op")
					b.ReportMetric(float64(count), "index-reads/op")
					b.ReportMetric(float64(count*2), "recording-stats/op")
				}()
				for b.Loop() {
					if entries, err := lib.List(); err != nil || len(entries) != count {
						b.Fatal(err, len(entries))
					}
				}
			})
		})
	}
}

func previousList(l Library) ([]Entry, error) {
	entries := []Entry{}
	files, err := os.ReadDir(filepath.Join(l.Root, "recordings"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, f := range files {
		if !f.Type().IsRegular() || (!strings.HasSuffix(f.Name(), ".tbt") && !strings.HasSuffix(f.Name(), ".json") && !strings.HasSuffix(f.Name(), ".partial")) {
			continue
		}
		_, _ = l.Register(filepath.Join(l.Root, "recordings", f.Name()))
	}
	files, err = os.ReadDir(filepath.Join(l.Root, "library"))
	if errors.Is(err, os.ErrNotExist) {
		return entries, nil
	}
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if !f.Type().IsRegular() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		loaded, err := l.readEntry(strings.TrimSuffix(f.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		e := *loaded
		path := e.Path
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			path += ".partial"
		}
		if refreshed, err := l.Register(path); err == nil {
			e = *refreshed
		} else if errors.Is(err, os.ErrNotExist) {
			e.Status = "missing"
		} else {
			e.Status = "invalid"
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Started == entries[j].Started {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].Started > entries[j].Started
	})
	return entries, nil
}

func TestLegacyFingerprintDoesNotAuthorizeOldFormat(t *testing.T) {
	lib := Library{Root: t.TempDir()}
	if err := lib.Init(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{".json", ".json.partial"} {
		path := filepath.Join(lib.Root, "recordings", "old"+suffix)
		data := []byte(`{"s":[80,24],"r":[]}`)
		status := "ready"
		if strings.HasSuffix(path, ".partial") {
			data = []byte("{\"f\":\"tbt-journal-v99\",\"r\":{\"s\":[80,24]}}\n")
			status = "partial"
		}
		os.WriteFile(path, data, 0600)
		st, _ := os.Stat(path)
		base := strings.TrimSuffix(path, ".partial")
		e := Entry{Version: 2, ID: pathID(base), Path: base, Status: status, Fingerprint: fileFingerprint(st)}
		encoded, _ := json.Marshal(e)
		os.WriteFile(filepath.Join(lib.Root, "library", e.ID+".json"), encoded, 0600)
		entries, err := lib.List()
		if err != nil {
			t.Fatal(err)
		}
		for _, got := range entries {
			if got.Status != "unsupported" {
				t.Fatal(got)
			}
		}
		os.Remove(path)
	}
}
