package recording

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompressedSavesFinalizationAndRecovery(t *testing.T) {
	r := Recording{ID: NewID(), Title: "Compressed fixture", Info: Info{CLI: "v1.0.0"}, Sizes: []int{80, 24}, Metadata: &Metadata{Version: 1, Description: "Description"}, Markers: []Marker{{ID: NewID(), At: 0, Kind: "chapter", Label: "Chapter"}}, Lines: []Event{{Time: 1, Lines: []string{strings.Repeat("\x1b[32mhello 世界\x1b[0m\r\n", 1000)}}, {Time: 2, Command: "s", Sizes: []int{100, 30}}}}
	before, _ := json.Marshal(r)
	for _, mode := range []string{"save", "capture", "recover"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "recording.json")
			if mode == "save" {
				if err := Save(path, &r); err != nil {
					t.Fatal(err)
				}
			} else {
				header := r
				header.Lines = nil
				w, err := NewWriter(path, header)
				if err != nil {
					t.Fatal(err)
				}
				for _, e := range r.Lines {
					// Split the long output across bounded journal events without changing text.
					if len(e.Lines) > 0 {
						text := e.Lines[0]
						for len(text) > 0 {
							n := min(len(text), len("\x1b[32mhello 世界\x1b[0m\r\n")*1000)
							part := e
							part.Lines = []string{text[:n]}
							if err := w.Append(part); err != nil {
								t.Fatal(err)
							}
							e.Time = 0
							text = text[n:]
						}
					} else if err := w.Append(e); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "capture" {
					if err := w.Finish(); err != nil {
						t.Fatal(err)
					}
				} else {
					w.Close()
					journal, _ := os.ReadFile(path + ".partial")
					if err := RecoverFile(path+".partial", path); err != nil {
						t.Fatal(err)
					}
					after, _ := os.ReadFile(path + ".partial")
					if !bytes.Equal(journal, after) {
						t.Fatal("recovery changed journal")
					}
				}
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(data, []byte("PK\x03\x04")) {
				t.Fatal("not a v1 archive")
			}

			if len(data) > len(before)/10 {
				t.Fatalf("compression did not reduce repetitive output: %d -> %d", len(before), len(data))
			}
			got, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			var text strings.Builder
			for _, e := range got.Lines {
				for _, line := range e.Lines {
					text.WriteString(line)
				}
			}
			if text.String() != r.Lines[0].Lines[0] || Duration(got) != 3 || got.Info != r.Info || got.Markers[0] != r.Markers[0] || got.Metadata.Description != r.Metadata.Description {
				t.Fatal("compression lost recording data")
			}
			s, err := Inspect(path)
			if err != nil || s.Duration != 3 || s.Events != len(got.Lines) || s.Bytes != int64(len(data)) || s.Lines != nil || s.Pack != "" {
				t.Fatal("packed inspection failed", s, err)
			}
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0600 {
				t.Fatal("recording not private")
			}
		})
	}
	after, _ := json.Marshal(r)
	if !bytes.Equal(before, after) {
		t.Fatal("encoding changed source")
	}
}

func TestPackedEncodingEmptyDeterministicAndWriteFailures(t *testing.T) {
	var a, b bytes.Buffer
	r := &Recording{Sizes: []int{80, 24}}
	if err := Encode(&a, r); err != nil {
		t.Fatal(err)
	}
	if err := Encode(&b, r); err != nil || !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("non-deterministic encoding", err)
	}
	got, err := Decode(&a)
	if err != nil || len(got.Lines) != 0 {
		t.Fatal(got, err)
	}
	failure := errors.New("disk failed")
	writes := 0
	err = Encode(writeFunc(func(data []byte) (int, error) {
		writes++
		if writes > 0 {
			return 0, failure
		}
		return len(data), nil
	}), r)
	if !errors.Is(err, failure) {
		t.Fatal("final compression/write error lost", err)
	}
}

func TestCompressedOutputCannotBypassExpandedSizeLimit(t *testing.T) {
	event := Event{Lines: []string{strings.Repeat("x", 64<<10)}}
	err := writeArchive(io.Discard, Recording{Sizes: []int{80, 24}}, func(write func(Event) error) error {
		for range MaxExpandedBytes/(64<<10) + 1 {
			if err := write(event); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		t.Fatal("compression bypassed expanded recording limit")
	}
}

func TestPackedInspectionChecksChecksumShapesAndAmbiguity(t *testing.T) {
	packed := func(raw string) string {
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		io.WriteString(gz, raw)
		gz.Close()
		return base64.StdEncoding.EncodeToString(b.Bytes())
	}
	valid := packed(`[{"t":5,"l":["hello"]}]`)
	corrupt, _ := base64.StdEncoding.DecodeString(valid)
	corrupt[len(corrupt)-8] ^= 1
	for _, p := range []string{packed(`{"not":"array"}`), packed(`[{}] []`), packed(`[{"t":-1}]`), base64.StdEncoding.EncodeToString(corrupt), valid[:len(valid)-8], "not base64"} {
		b, _ := json.Marshal(Recording{Sizes: []int{80, 24}, Pack: p})
		path := filepath.Join(t.TempDir(), "bad.json")
		os.WriteFile(path, b, 0600)
		if _, err := Inspect(path); err == nil {
			t.Fatal("invalid packed recording inspected", p)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("invalid packed recording decoded", p)
		}
	}
	r := Recording{Sizes: []int{80, 24}, Pack: valid, Lines: []Event{{}}}
	b, _ := json.Marshal(r)
	path := filepath.Join(t.TempDir(), "ambiguous.json")
	os.WriteFile(path, b, 0600)
	if _, err := Inspect(path); err == nil {
		t.Fatal("ambiguous encoding accepted")
	}
}

// Called by the JavaScript interoperability test with fresh browser-produced
// gzip. It verifies browser→Go and writes the Go→browser half in the same run.
func TestCompressionBrowserInterop(t *testing.T) {
	dir := os.Getenv("TBT_COMPRESSION_FIXTURE_DIR")
	if dir == "" {
		t.Skip("optional website/tests/compression.test.mjs interoperability check")
	}
	want, err := Load(filepath.Join(dir, "original.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(filepath.Join(dir, "browser.json"))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(want)
	b, _ := json.Marshal(got)
	if !bytes.Equal(a, b) {
		t.Fatal("browser compression changed data")
	}
	if err := Save(filepath.Join(dir, "go.json"), want); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkPackedInspection(b *testing.B) {
	r := &Recording{Sizes: []int{80, 24}}
	for range 10000 {
		r.Lines = append(r.Lines, Event{Time: 1, Lines: []string{strings.Repeat("output", 100)}})
	}
	path := filepath.Join(b.TempDir(), "packed.json")
	if err := Save(path, r); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := Inspect(path); err != nil {
			b.Fatal(err)
		}
	}
}
