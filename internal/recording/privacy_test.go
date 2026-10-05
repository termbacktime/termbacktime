package recording

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPrivacyFixtures(t *testing.T) {
	var fixtures []struct {
		Name      string
		Recording Recording
		Rules     Rules
		Expected  Recording
		Findings  int
	}
	data, err := os.ReadFile("testdata/privacy-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			before, _ := json.Marshal(f.Recording)
			found := Scan(&f.Recording)
			if len(found) != f.Findings {
				t.Fatalf("findings %v", found)
			}
			clean, err := Redact(&f.Recording, f.Rules)
			if err != nil {
				t.Fatal(err)
			}
			if !ValidID(clean.ID) {
				t.Fatal("new ID missing")
			}
			clean.ID = ""
			if !reflect.DeepEqual(*clean, f.Expected) {
				a, _ := json.Marshal(clean)
				b, _ := json.Marshal(f.Expected)
				t.Fatalf("got %s\nwant%s", a, b)
			}
			after, _ := json.Marshal(f.Recording)
			if !bytes.Equal(before, after) {
				t.Fatal("original mutated")
			}
			if f.Rules.Detected && len(Scan(clean)) != 0 {
				t.Fatal("secrets remain")
			}
		})
	}
}
func TestIdleTiming(t *testing.T) {
	r := &Recording{Lines: []Event{{Time: 0}, {Time: 1000}, {Time: 20000}, {Time: 0}, {Time: 1000}}}
	original, presented := Times(r, 2000)
	if !reflect.DeepEqual(presented, []int64{0, 0, 1000, 3000, 3000, 4000}) {
		t.Fatal(presented)
	}
	if MapTime(11000, original, presented) != 2000 || MapTime(2000, presented, original) != 11000 {
		t.Fatal("timestamp mapping")
	}
	_, full := Times(r, 0)
	if !reflect.DeepEqual(original, full) {
		t.Fatal("default changed timing")
	}
}
func TestSyncIntervalFinalizesAndUnlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.json")
	r := &Recording{Sizes: []int{80, 24}}
	w, err := NewWriterWithInterval(path, *r, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Append(Event{Lines: []string{"saved before next timer"}}); err != nil {
		t.Fatal(err)
	}
	if err = w.Finish(); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || len(got.Lines) != 1 {
		t.Fatalf("finalize: %v", err)
	}
}
func BenchmarkScan(b *testing.B) {
	r := &Recording{Sizes: []int{80, 24}}
	for range 1000 {
		r.Lines = append(r.Lines, Event{Time: 10, Lines: []string{strings.Repeat("terminal output ", 64)}})
	}
	b.SetBytes(1000 * 1024)
	b.ReportAllocs()
	for b.Loop() {
		Scan(r)
	}
}
func BenchmarkCapture(b *testing.B) {
	path := filepath.Join(b.TempDir(), "capture.json")
	e := Event{Time: 1, Lines: []string{strings.Repeat("x", 128)}}
	b.SetBytes(1000 * 128)
	b.ReportAllocs()
	for b.Loop() {
		w, err := NewWriterWithInterval(path, Recording{Sizes: []int{80, 24}}, time.Second)
		if err != nil {
			b.Fatal(err)
		}
		for range 1000 {
			if err := w.Append(e); err != nil {
				b.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			b.Fatal(err)
		}
		if err := os.Remove(path + ".partial"); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkFinalization(b *testing.B) {
	path := filepath.Join(b.TempDir(), "finalize.json")
	w, err := NewWriterWithInterval(path, Recording{Sizes: []int{80, 24}}, time.Second)
	if err != nil {
		b.Fatal(err)
	}
	for range 10000 {
		if err = w.Append(Event{Time: 1, Lines: []string{strings.Repeat("x", 128)}}); err != nil {
			b.Fatal(err)
		}
	}
	w.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		f, e := os.Open(path + ".partial")
		if e != nil {
			b.Fatal(e)
		}
		err := finalizeJournal(io.Discard, f)
		f.Close()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestDeferredSyncFailureReportedWhileIdle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "failed.json")
	w, err := NewWriterWithInterval(path, Recording{Sizes: []int{80, 24}}, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	w.file.Close()
	w.mu.Unlock()
	select {
	case <-w.SyncErrors():
	case <-time.After(time.Second):
		t.Fatal("idle disk failure not reported")
	}
	if err := w.Finish(); err == nil {
		t.Fatal("failed recording reported saved")
	}
	if _, err := os.Stat(path + ".partial"); err != nil {
		t.Fatal("journal lost")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("incomplete recording published")
	}
}

func TestRenamedJournalIsNotAcceptedAsFinalRecording(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.json")
	w, err := NewWriter(path, Recording{Sizes: []int{80, 24}})
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(path+".partial", path); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("accepted journal as finalized recording")
	}
	if _, err := Inspect(path); err == nil {
		t.Fatal("indexed journal as ready")
	}
}
