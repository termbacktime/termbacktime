package recording

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewDoesNotVerifyLaterChunks(t *testing.T) {
	r := &Recording{Sizes: []int{80, 24}, Lines: make([]Event, 4097)}
	for n := range r.Lines {
		r.Lines[n] = Event{Time: 1, Lines: []string{"x"}}
	}
	var original bytes.Buffer
	if err := Encode(&original, r); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(original.Bytes()), int64(original.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var corrupted bytes.Buffer
	writer := zip.NewWriter(&corrupted)
	for _, entry := range archive.File {
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name == chunkName(1) {
			data = bytes.Replace(data, []byte(`"t":1`), []byte(`"t":9`), 1)
		}
		output, err := writer.CreateHeader(&zip.FileHeader{Name: entry.Name, Method: entry.Method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = output.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "corrupt-later.tbt")
	if err := os.WriteFile(path, corrupted.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := Preview(t.Context(), path)
	if err != nil || preview.Events != 4097 || !strings.Contains(preview.Verification, "preview") {
		t.Fatal(preview, err)
	}
	if _, err := InspectContext(t.Context(), path); err == nil {
		t.Fatal("full inspection accepted corrupt timing")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Preview(ctx, path); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestPlaybackHistoryRevisionIsBoundedAndNotSerialized(t *testing.T) {
	path := filepath.Join("testdata", "recording-v1.tbt")
	source, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	expected, err := os.ReadFile(filepath.Join("testdata", "recording-v1.history-id"))
	if err != nil {
		t.Fatal(err)
	}
	if source.Manifest.Recording.HistoryID != strings.TrimSpace(string(expected)) {
		t.Fatal("shared history identity mismatch", source.Manifest.Recording.HistoryID)
	}
	r, err := source.Materialize(MaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if r.HistoryID != source.Manifest.Recording.HistoryID {
		t.Fatal("materialization lost revision")
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(r.HistoryID)) {
		t.Fatal("history identifier entered recording JSON")
	}
}
