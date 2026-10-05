package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
)

func TestScanAndRedactCommandsMaskFindingsAndPreserveOriginal(t *testing.T) {
	h := newCommandHarness(t)
	path, entry := h.recording(&recording.Recording{Title: "Privacy demo", Sizes: []int{80, 24}, Lines: []recording.Event{{Lines: []string{"password=syntheticprivatevalue\r\nprivate literal\r\n"}}}})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, diagnostic, err := h.run(t.Context(), "scan", entry.ID[:12])
	var findings []recording.Finding
	if err != nil || diagnostic != "" || json.Unmarshal([]byte(out), &findings) != nil || len(findings) != 1 || strings.Contains(out, "syntheticprivatevalue") {
		t.Fatal(out, diagnostic, err)
	}
	rules := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(rules, []byte(`{"version":1,"detected":true,"literals":["private literal"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	out, diagnostic, err = h.run(t.Context(), "redact", path, "--rules", rules)
	if err != nil || diagnostic != "" {
		t.Fatal(out, diagnostic, err)
	}
	cleaned := strings.TrimSpace(out)
	if filepath.Dir(cleaned) != filepath.Join(h.data, "recordings") {
		t.Fatal(cleaned)
	}
	r, err := recording.Load(cleaned)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(r)
	if err != nil || bytes.Contains(data, []byte("syntheticprivatevalue")) || bytes.Contains(data, []byte("private literal")) {
		t.Fatal(string(data), err)
	}
	out, _, err = h.run(t.Context(), "scan", cleaned)
	if err != nil || out != "[]\n" {
		t.Fatal(out, err)
	}
	if _, err := (library.Library{Root: h.data}).Resolve(cleaned); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("redaction changed original", err)
	}
	if err := os.WriteFile(rules, []byte(`{"version":1,"literals":["private literal"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, diagnostic, err = h.run(t.Context(), "redact", path, "--rules", rules)
	if err != nil || !strings.Contains(diagnostic, "still has 1 likely secret") || strings.Contains(diagnostic, "syntheticprivatevalue") {
		t.Fatal(diagnostic, err)
	}
}

func TestRedactCommandInvalidRulesAndConflictsPublishNothing(t *testing.T) {
	for _, test := range []struct {
		name, rules string
		missing     bool
	}{
		{"missing flag", "", true}, {"malformed", "{", false}, {"version", `{"version":2}`, false},
		{"invalid interval", `{"version":1,"intervals":[{"start_ms":20,"end_ms":10}]}`, false},
		{"oversized", strings.Repeat(" ", 1<<20+1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newCommandHarness(t)
			path, _ := h.recording(&recording.Recording{Sizes: []int{80, 24}})
			destination := filepath.Join(t.TempDir(), "cleaned.tbt")
			args := []string{"redact", path, "--output", destination}
			if !test.missing {
				rules := filepath.Join(t.TempDir(), "rules.json")
				if err := os.WriteFile(rules, []byte(test.rules), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--rules", rules)
			}
			if out, _, err := h.run(t.Context(), args...); err == nil || out != "" {
				t.Fatal(out, err)
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal(err)
			}
		})
	}
	h := newCommandHarness(t)
	path, _ := h.recording(&recording.Recording{Sizes: []int{80, 24}})
	rules := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(rules, []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if _, _, err := h.run(t.Context(), "redact", path, "--rules", rules, "--output", path); err == nil {
		t.Fatal("overwrote original")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("conflict changed original")
	}
	if _, _, err := h.run(t.Context(), "scan", "missing.tbt"); err == nil {
		t.Fatal("scanned missing input")
	}
}
