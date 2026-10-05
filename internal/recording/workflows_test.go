package recording

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharingWrapperRoundTripAndMalformedInputs(t *testing.T) {
	record := &Recording{Title: "demo", Sizes: []int{80, 24}, Lines: []Event{{Time: 5, Lines: []string{"hello 世界"}}}}
	var archive, wrapped bytes.Buffer
	if err := EncodeSmall(&archive, record); err != nil {
		t.Fatal(err)
	}
	if err := WriteWrapped(&wrapped, bytes.NewReader(archive.Bytes())); err != nil {
		t.Fatal(err)
	}
	got, err := ReadWrapped(bytes.NewReader(wrapped.Bytes()))
	if err != nil || got.Title != "demo" || got.Lines[0].Lines[0] != "hello 世界" {
		t.Fatal(got, err)
	}
	for _, input := range []string{"broken", "null", `{"f":"wrong","p":""}`, `{"f":"tbt-recording-v1","p":"!"}`, `{"f":"tbt-recording-v1","p":"aGk=\n"}`, `{"f":"tbt-recording-v1","p":"aGk="}`} {
		if _, err := ReadWrapped(strings.NewReader(input)); err == nil {
			t.Fatal(input)
		}
	}
	errExpected := errors.New("read failure")
	if _, err := ReadWrapped(errorReader{errExpected}); !errors.Is(err, errExpected) {
		t.Fatal(err)
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func TestFullVerificationJournalPlaybackAndTranscriptFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.tbt")
	record := Recording{Sizes: []int{80, 24}, Lines: []Event{{Time: 1, Lines: []string{"hello\r\nworld"}}}}
	if err := Save(path, &record); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(t.Context(), path, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Verify(ctx, path, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, markdown := range []bool{false, true} {
		var output bytes.Buffer
		if err := WriteTranscript(t.Context(), &output, &record, markdown); err != nil || !strings.Contains(output.String(), "hello") {
			t.Fatal(output.String(), err)
		}
		if err := WriteTranscript(t.Context(), io.Discard, &Recording{Sizes: []int{80, 24}}, markdown); err != nil {
			t.Fatal(err)
		}
		if err := WriteTranscript(ctx, io.Discard, &record, markdown); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	writer, err := NewWriter(filepath.Join(t.TempDir(), "journal.tbt"), Recording{Sizes: []int{80, 24}})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Append(Event{Lines: []string{"journal"}}); err != nil {
		t.Fatal(err)
	}
	journal := writer.path + ".partial"
	if err := RecoverFile(journal, filepath.Join(t.TempDir(), "recovered.tbt")); err == nil {
		t.Fatal("active journal recovered")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	source, err := OpenPlayback(journal)
	if err != nil {
		t.Fatal(err)
	}
	if source.Count() != 1 {
		t.Fatal(source.Count())
	}
	event, err := source.Event(0)
	if err != nil || event.Lines[0] != "journal" {
		t.Fatal(event, err)
	}
	if _, err := source.Event(2); err == nil {
		t.Fatal("out of range event accepted")
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(journal); err != nil {
		t.Fatal("playback removed journal", err)
	}
}
