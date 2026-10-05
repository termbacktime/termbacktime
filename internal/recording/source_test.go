package recording

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/rand"
	"strings"
	"testing"
)

func archiveFixture(t *testing.T, r Recording) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := Encode(&b, &r); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func rewriteArchive(t *testing.T, data []byte, edit func(string, []byte) (string, []byte, uint16), extra bool) []byte {
	t.Helper()
	in, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	out := zip.NewWriter(&b)
	for _, f := range in.File {
		raw, err := readEntry(f, MaxManifestBytes)
		if err != nil {
			t.Fatal(err)
		}
		name, raw, method := edit(f.Name, raw)
		w, err := out.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		w.Write(raw)
	}
	if extra {
		w, _ := out.CreateHeader(&zip.FileHeader{Name: "timing.bin", Method: zip.Store})
		w.Write(nil)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestSourceChunkBoundariesAndDisposal(t *testing.T) {
	r := Recording{Sizes: []int{80, 24}}
	for i := range 9000 {
		e := Event{Time: int64(i % 3), Lines: []string{"世界 🙂"}}
		if i == 4096 {
			e = Event{Time: 3, Command: "s", Sizes: []int{120, 40}}
		}
		r.Lines = append(r.Lines, e)
	}
	data := archiveFixture(t, r)
	s, err := OpenReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Manifest.Chunks) != 3 || s.Manifest.Chunks[0].Count != 4096 {
		t.Fatal(s.Manifest.Chunks)
	}
	for _, i := range []int{0, 4095, 4096, 8191, 8192, 8999, 12} {
		e, err := s.Event(i)
		a, _ := json.Marshal(e)
		b, _ := json.Marshal(r.Lines[i])
		if err != nil || !bytes.Equal(a, b) {
			t.Fatal(i, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Walk(ctx, func(Event) error { return nil }); err == nil {
		t.Fatal("ignored cancellation")
	}
	s.Close()
	if _, err := s.Event(0); err == nil {
		t.Fatal("read disposed source")
	}
}
func TestSourceRejectsCorruptArchive(t *testing.T) {
	data := archiveFixture(t, Recording{Sizes: []int{80, 24}, Lines: []Event{{Time: 4, Lines: []string{"hello"}}}})
	for _, kind := range []string{"duplicate", "unexpected", "manifest count", "duration", "expanded", "chunk count", "chunk size", "timing", "event timing", "compression", "unsupported version"} {
		t.Run(kind, func(t *testing.T) {
			bad := rewriteArchive(t, data, func(name string, b []byte) (string, []byte, uint16) {
				method := uint16(zip.Store)
				if strings.HasPrefix(name, "events/") {
					method = zip.Deflate
				}
				if name == "manifest.json" {
					var m Manifest
					json.Unmarshal(b, &m)
					switch kind {
					case "manifest count":
						m.Count++
					case "duration":
						m.Duration++
					case "expanded":
						m.Bytes++
					case "chunk count":
						m.Chunks[0].Count++
					case "chunk size":
						m.Chunks[0].Bytes++
					case "unsupported version":
						m.Format = "tbt-recording-v99"
					}
					b, _ = json.Marshal(m)
				}
				if name == "timing.bin" && kind == "timing" {
					binary.LittleEndian.PutUint32(b, 86400001)
				}
				if strings.HasPrefix(name, "events/") {
					switch kind {
					case "unexpected":
						name = "other.json"
					case "compression":
						method = zip.Store
					case "event timing":
						b = bytes.Replace(b, []byte(`"t":4`), []byte(`"t":5`), 1)
					}
				}
				return name, b, method
			}, kind == "duplicate")
			s, err := OpenReader(bytes.NewReader(bad), int64(len(bad)))
			if err == nil {
				s.Close()
				t.Fatal("accepted", kind)
			}
		})
	}
	// CRC corruption of a stored entry is rejected even when its JSON still parses.
	bad := append([]byte(nil), data...)
	i := bytes.Index(bad, []byte(`tbt-recording-v1`))
	bad[i] = 'T'
	if _, err := OpenReader(bytes.NewReader(bad), int64(len(bad))); err == nil {
		t.Fatal("accepted corrupt CRC")
	}
}

type measuredReader struct {
	*bytes.Reader
	bytes int64
}

func (r *measuredReader) ReadAt(p []byte, off int64) (int, error) {
	n, e := r.Reader.ReadAt(p, off)
	r.bytes += int64(n)
	return n, e
}
func TestLargeSourceLazyAndBoundedMaterialization(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	payload := make([]byte, 64<<10)
	for i := range payload {
		payload[i] = byte(32 + rng.Intn(90))
	}
	text := string(payload)
	var b bytes.Buffer
	if err := writeArchive(&b, Recording{Sizes: []int{80, 24}}, func(write func(Event) error) error {
		for range 1100 {
			if err := write(Event{Lines: []string{text}}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	input := &measuredReader{Reader: bytes.NewReader(b.Bytes())}
	s, err := OpenReader(input, int64(b.Len()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Manifest.Bytes <= MaxBytes {
		t.Fatal("fixture too small")
	}
	if input.bytes > int64(b.Len())/2 {
		t.Fatal("initial read inflated/read the complete archive", input.bytes, b.Len())
	}
	reads := input.bytes
	if _, err := s.Materialize(MaxBytes); err == nil || input.bytes != reads {
		t.Fatal("oversized materialization read chunks", err)
	}
	if _, err := s.Event(1099); err != nil {
		t.Fatal(err)
	}
	if len(s.cache) > ChunkEvents {
		t.Fatal("unbounded cache")
	}
}
func TestExpandedOneGiBBoundary(t *testing.T) {
	if testing.Short() {
		t.Skip("1 GiB streaming boundary")
	}
	header := Recording{Sizes: []int{80, 24}}
	encoded, _ := json.Marshal(header)
	remaining := int64(MaxExpandedBytes - len(encoded))
	chunk := int64(2)
	remaining -= 2
	var b bytes.Buffer
	err := writeArchive(&b, header, func(write func(Event) error) error {
		for remaining > 0 {
			comma := int64(0)
			if chunk > 2 {
				comma = 1
			}
			size := min(int64(240000), remaining-comma)
			if chunk+comma+size > ChunkBytes {
				remaining -= 2
				chunk = 2
				comma = 0
				size = min(int64(240000), remaining)
			}
			if size < 10 {
				return io.ErrUnexpectedEOF
			}
			e := Event{Lines: []string{strings.Repeat("x", int(size)-10)}}
			data, _ := json.Marshal(e)
			if int64(len(data)) != size {
				t.Fatal("fixture accounting", len(data), size)
			}
			if err := write(e); err != nil {
				return err
			}
			chunk += comma + size
			remaining -= comma + size
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := OpenReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Manifest.Bytes+int64(len(encoded)) != MaxExpandedBytes {
		t.Fatal(s.Manifest.Bytes)
	}
}

func TestSharedBrowserV1Fixture(t *testing.T) {
	r, err := Load("testdata/recording-v1.tbt")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Lines) != 3 || Duration(r) != 35 || r.Metadata.CaptureSystem.CPUModel != "Example CPU" {
		t.Fatal("shared fixture changed")
	}
}

func TestSourceWindowTransferBounds(t *testing.T) {
	r := Recording{Sizes: []int{80, 24}}
	for range 12 {
		r.Lines = append(r.Lines, Event{Lines: []string{strings.Repeat("x", 64<<10)}})
	}
	data := archiveFixture(t, r)
	s, err := OpenReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	window, err := s.Window(0, 256)
	if err != nil || len(window) != 3 {
		t.Fatal(len(window), err)
	}
	if _, err := s.Window(0, 257); err == nil {
		t.Fatal("unbounded window")
	}
	if _, err := s.Window(-1, 2); err == nil {
		t.Fatal("invalid start")
	}
}
