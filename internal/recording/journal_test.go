package recording

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStreamingFinalizationMatchesRecovery(t *testing.T) {
	for _, events := range []string{
		"",
		"{\"l\":[\"Unicode: 世界 \\u001b[31mred\\u001b[0m\\n\"]}\n{\"t\":23,\"c\":\"s\",\"s\":[100,30]}\n",
		"{\"t\":5,\"l\":[\"complete\"]}\n{\"l\":[\"truncated",
	} {
		journal := `{"format":"tbt-journal-v1","recording":{"i":{"a":"amd64","o":"linux","v":"test","cli":"v1.2.3"},"d":123,"t":"fixture","s":[80,24]}}` + "\n" + events
		expected, err := Recover(strings.NewReader(journal))
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if err := finalizeJournal(&output, strings.NewReader(journal)); err != nil {
			t.Fatal(err)
		}
		actual, err := Decode(&output)
		actualJSON, _ := json.Marshal(actual)
		expectedJSON, _ := json.Marshal(expected)
		if err != nil || !bytes.Equal(actualJSON, expectedJSON) {
			t.Fatalf("finalization changed recording: %v", err)
		}
		if actual.Info.CLI != "v1.2.3" {
			t.Fatal("journal finalization or recovery lost the CLI version")
		}
	}
}

type countingReader struct {
	source io.Reader
	read   int
}

func (r *countingReader) Read(data []byte) (int, error) {
	n, err := r.source.Read(data)
	r.read += n
	return n, err
}

type writeFunc func([]byte) (int, error)

func (f writeFunc) Write(data []byte) (int, error) { return f(data) }

// The output must advance before the complete recording has been read into memory
func TestFinalizationStreamsBeforeReadingTheWholeJournal(t *testing.T) {
	header := `{"format":"tbt-journal-v1","recording":{"s":[80,24]}}` + "\n"
	event, err := json.Marshal(Event{Lines: []string{strings.Repeat("x", 16<<10)}})
	if err != nil {
		t.Fatal(err)
	}
	event = append(event, '\n')
	input := &countingReader{source: io.MultiReader(strings.NewReader(header), bytes.NewReader(bytes.Repeat(event, 1024)))}
	writes := 0
	output := writeFunc(func(data []byte) (int, error) {
		if writes == 0 && input.read > 2<<20 {
			t.Fatal("finalization buffered the recording before writing")
		}
		writes++
		return len(data), nil
	})
	if err := finalizeJournal(output, input); err != nil {
		t.Fatal(err)
	}
	if writes < 2 {
		t.Fatal("finalization did not write incrementally")
	}
}

func TestJournalLimitsAndValidation(t *testing.T) {
	header := `{"format":"tbt-journal-v1","recording":{"s":[80,24]}}` + "\n"
	for _, journal := range []string{
		`{"format":"invalid"}` + "\n",
		`{"format":"tbt-journal-v1","recording":{"s":[0,24]}}` + "\n",
		header + "not JSON\n",
		header + "{\"t\":-1}\n",
		header + "{\"c\":\"s\",\"s\":[0,24]}\n",
		header + "{\"c\":\"unknown\"}\n",
		header + strings.Repeat("x", MaxJournalLineBytes+1),
		header + strings.Repeat("{\"t\":86400000}\n", 8),
	} {
		if err := finalizeJournal(io.Discard, strings.NewReader(journal)); err == nil {
			t.Fatal("accepted malformed or oversized journal")
		}
	}
	reader, err := newJournalReader(strings.NewReader(header + "{}\n"))
	if err != nil {
		t.Fatal(err)
	}
	reader.events = MaxEvents
	if _, err := reader.next(); err == nil {
		t.Fatal("accepted an event beyond the count limit")
	}
	reader, err = newJournalReader(strings.NewReader(header + "{}\n"))
	if err != nil {
		t.Fatal(err)
	}
	reader.bytes = MaxExpandedBytes
	if _, err := reader.next(); err == nil {
		t.Fatal("accepted journal data beyond the size limit")
	}
	if _, err := (&recordingOutput{destination: io.Discard, remaining: 2}).Write([]byte("123")); err == nil {
		t.Fatal("accepted recording output beyond the size limit")
	}
}

func TestFailedFinalizationPreservesJournalAndDestination(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		dir := t.TempDir()
		path := filepath.Join(dir, "recording.json")
		writer, err := NewWriter(path, Recording{Sizes: []int{80, 24}})
		if err != nil {
			t.Fatal(err)
		}
		event := Event{Lines: []string{"fixture"}}
		if !conflict {
			event.Time = -1
		}
		if err := writer.Append(event); err != nil {
			t.Fatal(err)
		}
		if conflict {
			if err := os.WriteFile(path, []byte("existing"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Finish(); err == nil {
			t.Fatal("accepted invalid or conflicting finalization")
		}
		if _, err := os.Stat(path + ".partial"); err != nil {
			t.Fatal("failed finalization lost the recovery journal", err)
		}
		if conflict {
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "existing" {
				t.Fatal("overwrote existing destination", err)
			}
		} else if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("published an invalid recording")
		}
		matches, err := filepath.Glob(filepath.Join(dir, ".tbt-recording-*"))
		if err != nil || len(matches) != 0 {
			t.Fatal("failed finalization left temporary files", err)
		}
	}
}
