package recording

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func featureRecording() *Recording {
	status := 7
	id := strings.Repeat("a", 32)
	return &Recording{ID: NewID(), Sizes: []int{20, 4}, Title: "fixture", Lines: []Event{
		{Lines: []string{"discarded scrollback\r\nold\r\nthird\r\nfourth\r\nfifth\r\n\x1b[2J\x1b[H"}},
		{Time: 100, Lifecycle: &CommandEvent{ID: id, Phase: "start", Text: "echo password=secretword"}, Lines: []string{"\x1b[31m世界\x1b[0m"}},
		{Time: 100, Lines: []string{"\rprogress 1%"}},
		{Time: 100, Lines: []string{"\rprogress 100%\r\ncomplete\r\n"}, Lifecycle: &CommandEvent{ID: id, Phase: "end", ExitCode: &status}},
		{Time: 100, Lines: []string{"\x1b[?1049h\x1b[?25lapp"}},
		{Time: 100, Lines: []string{"\x1b[?1049lafter"}},
		{Time: 100},
	}, Callouts: []Callout{{ID: NewID(), At: 200, Text: "token=veryprivate", Duration: 5000, Pause: true}}}
}
func TestFeatureMetadataPackingRecoveryAndRedaction(t *testing.T) {
	r := featureRecording()
	var packed bytes.Buffer
	if err := Encode(&packed, r); err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(&packed)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Callouts) != 1 || len(Commands(decoded)) != 1 || *Commands(decoded)[0].ExitCode != 7 {
		t.Fatal("lost metadata")
	}
	path := filepath.Join(t.TempDir(), "recording.json")
	w, err := NewWriter(path, *r)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Append(Event{Time: 1}); err != nil {
		t.Fatal(err)
	}
	w.Close()
	recovered, err := Load(path + ".partial")
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.Callouts) != 1 || recovered.Lines[1].Lifecycle.Text != r.Lines[1].Lifecycle.Text {
		t.Fatal("journal lost metadata")
	}
	clean, err := Redact(r, Rules{Version: 1, Detected: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(Scan(clean)) != 0 {
		t.Fatal("secrets survived", Scan(clean))
	}
	data, _ := json.Marshal(clean)
	if bytes.Contains(data, []byte("secretword")) || bytes.Contains(data, []byte("veryprivate")) {
		t.Fatal("metadata not redacted")
	}
	cut, err := Redact(r, Rules{Version: 1, Intervals: []Interval{{Start: 150, End: 250}}})
	if err != nil || !Commands(cut)[0].Partial {
		t.Fatal("interior cut must mark command incomplete", err)
	}
}

func TestTranscriptBeyondScrollback(t *testing.T) {
	r := featureRecording()
	var text bytes.Buffer
	if err := WriteTranscript(context.Background(), &text, r, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text.String(), "progress 1%") || !strings.Contains(text.String(), "progress 100%") || !strings.Contains(text.String(), "app") {
		t.Fatal(text.String())
	}
	r = &Recording{Sizes: []int{80, 4}}
	for n := 0; n < 6001; n++ {
		r.Lines = append(r.Lines, Event{Time: 1, Lines: []string{fmt.Sprintf("unique line %d\r\n", n)}})
	}
	lines := 0
	if err := WalkTranscript(context.Background(), r, func(e TranscriptEntry) error { lines++; return nil }); err != nil || lines != 6001 {
		t.Fatal(lines, err)
	}
}
