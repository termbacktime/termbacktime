package sealed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/termbacktime/termbacktime/internal/recording"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTripAndTampering(t *testing.T) {
	data := bytes.Repeat([]byte("private terminal 世界\n"), 140000)
	path := filepath.Join(t.TempDir(), "recording.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	m, key, files, err := EncryptFile(t.Context(), path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Parts) < 2 {
		t.Fatal("multipart fixture required")
	}
	manifest, err := os.ReadFile(files[Filename])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(manifest, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 7 {
		t.Fatal("unexpected manifest fields", fields)
	}
	for _, key := range []string{"f", "i", "b", "s", "c", "n", "p"} {
		if fields[key] == nil {
			t.Fatalf("missing compact manifest key %q", key)
		}
	}
	// Decrypt from the serialized wire manifest, including its authenticated values.
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	parts := map[string][]byte{}
	for _, n := range m.Parts {
		parts[n], err = os.ReadFile(files[n])
		if err != nil {
			t.Fatal(err)
		}
		if len(parts[n]) > MaxPartBytes || bytes.Contains(parts[n], []byte("private terminal")) {
			t.Fatal("part size or plaintext leak")
		}
	}
	read := func(n string) ([]byte, error) {
		b, ok := parts[n]
		if !ok {
			return nil, fmt.Errorf("missing")
		}
		return b, nil
	}
	got, err := Decrypt(t.Context(), m, key, read)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("round trip: %v", err)
	}
	cases := map[string]func(*Manifest, map[string][]byte){
		"missing":   func(m *Manifest, p map[string][]byte) { delete(p, m.Parts[0]) },
		"truncated": func(m *Manifest, p map[string][]byte) { p[m.Parts[0]] = p[m.Parts[0]][:10] },
		"modified":  func(m *Manifest, p map[string][]byte) { p[m.Parts[0]][0] = '!' },
		"reordered": func(m *Manifest, p map[string][]byte) {
			lines := strings.Split(string(p[m.Parts[0]]), "\n")
			lines[0], lines[1] = lines[1], lines[0]
			p[m.Parts[0]] = []byte(strings.Join(lines, "\n"))
		},
		"duplicated": func(m *Manifest, p map[string][]byte) {
			lines := strings.Split(string(p[m.Parts[0]]), "\n")
			lines[1] = lines[0]
			p[m.Parts[0]] = []byte(strings.Join(lines, "\n"))
		},
		"identity": func(m *Manifest, p map[string][]byte) { m.ID = strings.Repeat("0", 32) },
		"length":   func(m *Manifest, p map[string][]byte) { m.Bytes-- },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			copyM := m
			copyP := map[string][]byte{}
			for n, b := range parts {
				copyP[n] = bytes.Clone(b)
			}
			mutate(&copyM, copyP)
			b, e := Decrypt(t.Context(), copyM, key, func(n string) ([]byte, error) {
				v, ok := copyP[n]
				if !ok {
					return nil, fmt.Errorf("missing")
				}
				return v, nil
			})
			if e == nil || b != nil {
				t.Fatal("accepted unauthenticated data")
			}
		})
	}
	if _, err := Decrypt(t.Context(), m, "", read); err == nil {
		t.Fatal("missing key accepted")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Decrypt(canceled, m, key, read); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestInteroperabilityFixture(t *testing.T) {
	var fixture struct {
		Manifest  Manifest          `json:"manifest"`
		Key       string            `json:"key"`
		Plaintext string            `json:"plaintext"`
		Parts     map[string]string `json:"parts"`
	}
	b, err := os.ReadFile("testdata/sealed-legacy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	got, err := Decrypt(t.Context(), fixture.Manifest, fixture.Key, func(n string) ([]byte, error) { return []byte(fixture.Parts[n]), nil })
	if err != nil || string(got) != fixture.Plaintext {
		t.Fatalf("fixture: %v", err)
	}
}

func BenchmarkEncryption(b *testing.B) {
	source := filepath.Join(b.TempDir(), "recording.json")
	data := bytes.Repeat([]byte("benchmark output\n"), 65536)
	os.WriteFile(source, data, 0600)
	dir := b.TempDir()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _, files, err := EncryptFile(b.Context(), source, dir)
		if err != nil {
			b.Fatal(err)
		}
		for _, p := range files {
			os.Remove(p)
		}
	}
}

func TestV1RecordingFixtureAndLegacyPayloadRejection(t *testing.T) {
	for _, version := range []string{"legacy", "v1"} {
		data, err := os.ReadFile("testdata/sealed-" + version + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var fixture struct {
			Manifest Manifest
			Key      string
			Parts    map[string]string
		}
		if err := json.Unmarshal(data, &fixture); err != nil {
			t.Fatal(err)
		}
		plain, err := Decrypt(t.Context(), fixture.Manifest, fixture.Key, func(name string) ([]byte, error) { return []byte(fixture.Parts[name]), nil })
		if err != nil {
			t.Fatal(err)
		}
		_, err = recording.Decode(bytes.NewReader(plain))
		if (err == nil) != (version == "v1") {
			t.Fatal(version, err)
		}
	}
}
