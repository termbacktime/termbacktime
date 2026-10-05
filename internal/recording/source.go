package recording

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
)

var ErrUnsupported = errors.New("unsupported recording: only .tbt v1 recordings are supported")

// Source owns a seekable archive and one bounded decoded chunk. Call Close when done.
type Source struct {
	Manifest   Manifest
	Times      []int64
	files      map[string]*zip.File
	starts     []int
	closer     io.Closer
	mu         sync.Mutex
	closed     bool
	cacheIndex int
	cache      []Event
}

func Open(path string) (*Source, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !st.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("invalid recording file")
	}
	s, err := OpenReader(f, st.Size())
	if err != nil {
		f.Close()
		return nil, err
	}
	s.closer = f
	return s, nil
}
func OpenReader(reader io.ReaderAt, size int64) (*Source, error) {
	if size < 4 || size > MaxArchiveBytes {
		return nil, fmt.Errorf("invalid archive size")
	}
	var signature [4]byte
	if _, err := reader.ReadAt(signature[:], 0); err != nil {
		return nil, err
	}
	if string(signature[:]) != "PK\x03\x04" {
		return nil, ErrUnsupported
	}
	if err := checkZIPIndex(reader, size); err != nil {
		return nil, err
	}
	z, err := zip.NewReader(reader, size)
	if err != nil {
		return nil, err
	}
	if len(z.File) < 2 || len(z.File) > 4098 {
		return nil, fmt.Errorf("invalid archive entry count")
	}
	s := &Source{files: map[string]*zip.File{}, cacheIndex: -1}
	var total uint64
	for _, f := range z.File {
		if s.files[f.Name] != nil || f.Flags&1 != 0 || (f.Method != zip.Store && f.Method != zip.Deflate) {
			return nil, fmt.Errorf("duplicate, encrypted, or unsupported archive entry")
		}
		if f.CompressedSize64 > uint64(size) || f.UncompressedSize64 > MaxManifestBytes {
			return nil, fmt.Errorf("oversized archive entry")
		}
		total += f.UncompressedSize64
		if total > MaxArchiveBytes {
			return nil, fmt.Errorf("expanded archive exceeds limit")
		}
		s.files[f.Name] = f
	}
	mf := s.files["manifest.json"]
	if mf == nil || mf.Method != zip.Store {
		return nil, fmt.Errorf("missing stored manifest")
	}
	data, err := readEntry(mf, MaxManifestBytes)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for _, key := range []string{"f", "r", "n", "d", "b", "h", "c"} {
		if raw, ok := fields[key]; !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, fmt.Errorf("missing manifest field %s", key)
		}
	}
	if err = json.Unmarshal(data, &s.Manifest); err != nil {
		return nil, err
	}
	manifestBytes := data
	m := &s.Manifest
	if m.Format != Format {
		return nil, ErrUnsupported
	}
	if m.Count < 0 || m.Count > MaxEvents || m.Duration < 0 || m.Duration > 604800000 || m.Bytes < 0 || m.Bytes > MaxExpandedBytes || len(m.Chunks)+2 != len(z.File) || m.Recording.Pack != "" || len(m.Recording.Lines) != 0 {
		return nil, fmt.Errorf("invalid recording manifest")
	}
	if !validSize(m.Recording.Sizes) {
		return nil, fmt.Errorf("invalid terminal size")
	}
	if err := validateMetadata(&m.Recording, m.Duration); err != nil {
		return nil, err
	}
	header, err := json.Marshal(m.Recording)
	if err != nil {
		return nil, err
	}
	expanded, count := int64(0), 0
	for i, c := range m.Chunks {
		f := s.files[chunkName(i)]
		if c.Count < 1 || c.Count > ChunkEvents || c.Bytes < 2 || c.Bytes > ChunkBytes || f == nil || f.Method != zip.Deflate || f.UncompressedSize64 != uint64(c.Bytes) {
			return nil, fmt.Errorf("invalid recording chunk")
		}
		s.starts = append(s.starts, count)
		count += c.Count
		expanded += int64(c.Bytes)
	}
	if count != m.Count || expanded != m.Bytes || expanded+int64(len(header)) > MaxExpandedBytes {
		return nil, fmt.Errorf("inconsistent recording totals")
	}
	tf := s.files["timing.bin"]
	if tf == nil || tf.Method != zip.Store || tf.UncompressedSize64 != uint64(m.Count)*4 {
		return nil, fmt.Errorf("invalid timing table")
	}
	data, err = readEntry(tf, MaxEvents*4)
	if err != nil {
		return nil, err
	}
	s.Times = make([]int64, m.Count+1)
	for i := 0; i < m.Count; i++ {
		d := int64(binary.LittleEndian.Uint32(data[i*4:]))
		if d > 86400000 {
			return nil, fmt.Errorf("invalid event delay")
		}
		s.Times[i+1] = s.Times[i] + d
	}
	if s.Times[m.Count] != m.Duration {
		return nil, fmt.Errorf("inconsistent recording duration")
	}
	if m.Count > 0 {
		if _, err := s.Event(0); err != nil {
			return nil, err
		}
	}
	// A bounded revision token for playback history, not a proof of integrity.
	names := []string{"manifest.json", "timing.bin"}
	for i := range m.Chunks {
		names = append(names, chunkName(i))
	}
	entries := make([][]any, 0, len(names))
	for _, name := range names {
		f := s.files[name]
		entries = append(entries, []any{name, f.UncompressedSize64, f.CRC32})
	}
	descriptors, _ := json.Marshal(entries)
	h := sha256.New()
	h.Write([]byte("tbt-history-v1\n"))
	h.Write(manifestBytes)
	h.Write([]byte("\n"))
	h.Write(descriptors)
	s.Manifest.Recording.HistoryID = hex.EncodeToString(h.Sum(nil))
	return s, nil
}
func readEntry(f *zip.File, limit int64) ([]byte, error) {
	if f.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("archive entry too large")
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	b, err := ReadBounded(r, limit)
	if err != nil {
		return nil, err
	}
	if uint64(len(b)) != f.UncompressedSize64 {
		return nil, fmt.Errorf("archive entry size mismatch")
	}
	return b, nil
}
func (s *Source) Event(index int) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Event{}, os.ErrClosed
	}
	if index < 0 || index >= s.Manifest.Count {
		return Event{}, io.EOF
	}
	c := sort.Search(len(s.starts), func(i int) bool { return s.starts[i] > index }) - 1
	if c != s.cacheIndex {
		data, err := readEntry(s.files[chunkName(c)], ChunkBytes)
		if err != nil {
			return Event{}, err
		}
		var events []Event
		if err = json.Unmarshal(data, &events); err != nil {
			return Event{}, err
		}
		if len(events) != s.Manifest.Chunks[c].Count {
			return Event{}, fmt.Errorf("chunk event count mismatch")
		}
		duration := s.Times[s.starts[c]]
		for i, e := range events {
			encoded, err := json.Marshal(e)
			if err != nil || len(encoded) > MaxJournalLineBytes {
				return Event{}, fmt.Errorf("oversized event")
			}
			if err = validateEvent(e, &duration); err != nil {
				return Event{}, err
			}
			if duration != s.Times[s.starts[c]+i+1] {
				return Event{}, fmt.Errorf("chunk timing mismatch")
			}
		}
		s.cache, s.cacheIndex = events, c
	}
	return s.cache[index-s.starts[c]], nil
}
func (s *Source) Walk(ctx context.Context, visit func(Event) error) error {
	for i := 0; i < s.Manifest.Count; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		e, err := s.Event(i)
		if err != nil {
			return err
		}
		if err = visit(e); err != nil {
			return err
		}
	}
	return nil
}
func (s *Source) Materialize(limit int64) (*Recording, error) {
	header, err := json.Marshal(s.Manifest.Recording)
	if err != nil {
		return nil, err
	}
	if s.Manifest.Bytes+int64(len(header)) > limit {
		return nil, fmt.Errorf("this operation is limited to 64 MiB expanded recordings; local playback supports 1 GiB")
	}
	r := s.Manifest.Recording
	if err := s.Walk(context.Background(), func(e Event) error { r.Lines = append(r.Lines, e); return nil }); err != nil {
		return nil, err
	}
	return &r, r.Validate()
}
func (s *Source) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cache = nil
	if s.closer != nil {
		return s.closer.Close()
	}
	return nil
}
func OpenPlayback(path string) (*Recording, error) {
	if strings.HasSuffix(path, ".partial") {
		return openJournalPlayback(path)
	}
	s, err := Open(path)
	if err != nil {
		return nil, err
	}
	r := s.Manifest.Recording
	r.Source = s
	return &r, nil
}
func (r *Recording) Count() int {
	if r.Source != nil {
		return r.Source.Manifest.Count
	}
	return len(r.Lines)
}
func (r *Recording) Event(index int) (Event, error) {
	if r.Source != nil {
		return r.Source.Event(index)
	}
	if index < 0 || index >= len(r.Lines) {
		return Event{}, io.EOF
	}
	return r.Lines[index], nil
}
func (r *Recording) Close() error {
	if r.Source != nil {
		return r.Source.Close()
	}
	return nil
}
func decodeArchive(data []byte) (*Recording, error) {
	s, err := OpenReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return s.Materialize(MaxBytes)
}

// Playback never modifies the source journal. Incomplete final lines are ignored.
func openJournalPlayback(path string) (*Recording, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tmp, err := os.CreateTemp("", "tbt-playback-*.tbt")
	if err != nil {
		return nil, err
	}
	defer tmp.Close()
	defer os.Remove(tmp.Name())
	if err := finalizeJournal(tmp, f); err != nil {
		return nil, err
	}
	r, err := OpenPlayback(tmp.Name())
	if r != nil {
		r.HistoryID = ""
	}
	return r, err
}

// Window returns a bounded range for inspectors and other incremental consumers.
// Serialized events use at most 256 KiB, with at most 256 events per request.
func (s *Source) Window(start, limit int) ([]Event, error) {
	if start < 0 || limit < 1 || limit > 256 {
		return nil, fmt.Errorf("invalid event window")
	}
	events := make([]Event, 0, min(limit, max(0, s.Manifest.Count-start)))
	size := 0
	for index := start; index < s.Manifest.Count && len(events) < limit; index++ {
		e, err := s.Event(index)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		if size+len(data) > MaxJournalLineBytes {
			break
		}
		size += len(data)
		events = append(events, e)
	}
	return events, nil
}
