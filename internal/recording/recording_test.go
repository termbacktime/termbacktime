package recording

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyPackedAndUnpacked(t *testing.T) {
	events := []Event{{Lines: []string{"hello 世界"}}, {Time: 12, Command: "s", Sizes: []int{100, 30}}}
	r := Recording{Sizes: []int{80, 24}, Lines: events}
	b, _ := json.Marshal(r)
	_, err := Decode(bytes.NewReader(b))
	if err == nil {
		t.Fatalf("unpacked: %v", err)
	}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	_ = json.NewEncoder(gz).Encode(events)
	_ = gz.Close()
	r.Lines = nil
	r.Pack = base64.StdEncoding.EncodeToString(compressed.Bytes())
	b, _ = json.Marshal(r)
	_, err = Decode(bytes.NewReader(b))
	if err == nil {
		t.Fatalf("packed: %v", err)
	}
}

func TestJournalRecoveryAndNoClobber(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.json")
	w, err := NewWriter(path, Recording{Sizes: []int{80, 24}})
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Append(Event{Lines: []string{"saved"}}); err != nil {
		t.Fatal(err)
	}
	if err = w.Finish(); err != nil {
		t.Fatal(err)
	}
	r, err := Load(path)
	if err != nil || r.Lines[0].Lines[0] != "saved" {
		t.Fatalf("load: %v", err)
	}
	if _, err = NewWriter(path, *r); err == nil {
		t.Fatal("overwrote recording")
	}
	journal := `{"format":"tbt-journal-v1","recording":{"s":[80,24]}}` + "\n" + `{"l":["preserved"]}` + "\n" + `{"l":["truncated`
	r, err = Recover(strings.NewReader(journal))
	if err != nil || len(r.Lines) != 1 {
		t.Fatalf("recovery: %v", err)
	}
	if _, err = os.Stat(path + ".partial"); !os.IsNotExist(err) {
		t.Fatal("journal not removed")
	}
}

func TestRejectInvalidRecording(t *testing.T) {
	for _, data := range []string{`{"s":[0,24]}`, `{"r":[{"c":"s","s":[80]}]}`, `{"r":[{"t":-1}]}`, `{"p":"not-gzip"}`, `{"p":"abc","r":[{}]}`} {
		if _, err := Decode(strings.NewReader(data)); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	if _, err := ReadBounded(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("unbounded read")
	}
}
