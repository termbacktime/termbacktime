package recording

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func assertCompactKeys(t *testing.T, data []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	var check func(any)
	check = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			for key, item := range v {
				if len(key) != 1 || key[0] < 'a' || key[0] > 'z' {
					t.Fatalf("noncompact key %q", key)
				}
				check(item)
			}
		case []any:
			for _, item := range v {
				check(item)
			}
		}
	}
	check(value)
}

func TestCompactKeysAndReadableRoundTrip(t *testing.T) {
	data, err := os.ReadFile("testdata/recording-keys-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ Compact, Readable json.RawMessage }
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	var compact, readable Recording
	if err := json.Unmarshal(fixture.Compact, &compact); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fixture.Readable, &readable); err != nil || !reflect.DeepEqual(compact, readable) {
		t.Fatal("schema roundtrip", err)
	}
	canonical, _ := json.Marshal(readable)
	assertCompactKeys(t, canonical)
	var want, got any
	json.Unmarshal(fixture.Compact, &want)
	json.Unmarshal(canonical, &got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wire mapping changed: %s", canonical)
	}
	var packed bytes.Buffer
	if err := Encode(&packed, &readable); err != nil {
		t.Fatal(err)
	}
	source, err := OpenReader(bytes.NewReader(packed.Bytes()), int64(packed.Len()))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	for name, entry := range source.files {
		if name != "timing.bin" {
			data, err := readEntry(entry, MaxManifestBytes)
			if err != nil {
				t.Fatal(err)
			}
			assertCompactKeys(t, data)
		}
	}
	decoded, err := Decode(bytes.NewReader(packed.Bytes()))
	if err == nil {
		if decoded.HistoryID == "" {
			t.Fatal("missing recording revision")
		}
		decoded.HistoryID = "" // Runtime identity is not a serialized recording field.
	}
	if err != nil || !reflect.DeepEqual(decoded, &readable) {
		t.Fatal("container roundtrip", err)
	}
	path := filepath.Join(t.TempDir(), "recording.tbt")
	if err := os.WriteFile(path, packed.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	summary, err := Inspect(path)
	if err != nil || summary.Events != 3 || summary.Duration != 35 || summary.Metadata.CaptureSystem.CPUModel != "Example CPU" {
		t.Fatal("inspection", summary, err)
	}
	var header Recording

	// A new capture writes compact recovery data before it is finalized.
	header = readable
	header.Lines, header.Markers, header.Callouts = nil, nil, nil
	path = filepath.Join(t.TempDir(), "capture.json")
	w, err := NewWriter(path, header)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range readable.Lines {
		if err := w.Append(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	journal, err := os.ReadFile(path + ".partial")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(bytes.TrimSpace(journal), []byte{'\n'}) {
		assertCompactKeys(t, line)
	}
	recovered, err := Recover(bytes.NewReader(append(journal, []byte(`{"t":`)...)))
	if err != nil || !reflect.DeepEqual(recovered.Lines, readable.Lines) || recovered.Info != readable.Info {
		t.Fatal("compact journal recovery", err)
	}
	if err := RecoverFile(path+".partial", path); err != nil {
		t.Fatal(err)
	}
	finalized, err := Load(path)
	if err == nil {
		finalized.HistoryID = ""
	}
	if err != nil || !reflect.DeepEqual(finalized, recovered) {
		t.Fatal("journal finalization", err)
	}
}

func TestRejectAmbiguousKeyAliases(t *testing.T) {
	for _, data := range []string{
		`{"u":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`,
		`{"i":{"cli":"old","c":"new"}}`,
		`{"m":{"v":1,"version":1}}`,
		`{"metadata":{"version":1,"capture_system":{"o":"linux","os":"darwin"}}}`,
		`{"r":[{"t":1,"delay_ms":2}]}`,
		`{"r":[{"e":{"i":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","p":"end","phase":"start"}}]}`,
		`{"r":[],"events":[]}`,
		`{"f":"tbt-journal-v1","format":"tbt-journal-v1"}`,
		`{"f":"tbt-offline-v1"}`,
	} {
		if _, err := Decode(strings.NewReader(data)); err == nil {
			t.Fatalf("decoded ambiguous data: %s", data)
		}
		path := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Inspect(path); err == nil {
			t.Fatalf("inspected ambiguous data: %s", data)
		}
	}
}
