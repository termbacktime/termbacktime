package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
)

func TestImportAndInfoCommandsKeepExternalFilesAndPrivateLinksSeparate(t *testing.T) {
	h := newCommandHarness(t)
	lib := library.Library{Root: h.data}
	path := filepath.Join(t.TempDir(), "external recording.tbt")
	r := &recording.Recording{Title: "External demo", Started: 1700000000, Sizes: []int{80, 24}, Lines: []recording.Event{{Time: 20, Lines: []string{"hello 世界"}}}}
	if err := recording.Save(path, r); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := h.run(t.Context(), "import", path)
	if err != nil || !strings.Contains(out, path) {
		t.Fatal(out, err)
	}
	entry, err := lib.Register(path)
	if err != nil || !strings.HasPrefix(out, entry.ID+" ") {
		t.Fatal(entry, out, err)
	}
	link := "https://play.example/p/" + strings.Repeat("b", 32) + "#k=private-encryption-key"
	if err := lib.SaveReceipt(path, link); err != nil {
		t.Fatal(err)
	}
	for _, quick := range []bool{false, true} {
		args := []string{"info", entry.ID[:12]}
		if quick {
			args = append(args, "--quick")
		}
		out, _, err := h.run(t.Context(), args...)
		var summary struct {
			Title    string `json:"t"`
			Events   int    `json:"events"`
			Duration int64  `json:"duration_ms"`
			Status   string `json:"status"`
		}
		if err != nil || json.Unmarshal([]byte(out), &summary) != nil || summary.Title != r.Title || summary.Events != 1 || summary.Duration != 20 || summary.Status != "ready" {
			t.Fatal(out, err)
		}
		if strings.Contains(out, "private-encryption-key") {
			t.Fatal("ordinary info leaked a private link", out)
		}
	}
	out, _, err = h.run(t.Context(), "info", path, "--show-share-link")
	if err != nil || out != link+"\n" {
		t.Fatal(out, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("import or info changed the source", err)
	}
	other, _ := h.recording(&recording.Recording{Sizes: []int{80, 24}})
	if out, _, err := h.run(t.Context(), "info", other, "--show-share-link"); err == nil || out != "" {
		t.Fatal(out, err)
	}
}

func TestListCommandDateBoundariesSortingAndReadableOutput(t *testing.T) {
	h := newCommandHarness(t)
	day := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC).Unix()
	for _, r := range []*recording.Recording{
		{Title: "Before", Started: day - 1, Sizes: []int{80, 24}},
		{Title: "Beta", Started: day, Sizes: []int{80, 24}},
		{Title: "Alpha", Started: day + 86399, Sizes: []int{80, 24}},
		{Title: "After", Started: day + 86400, Sizes: []int{80, 24}},
	} {
		h.recording(r)
	}
	out, _, err := h.run(t.Context(), "list", "--after", "2026-10-04", "--before", "2026-10-04", "--sort", "title", "--json")
	var rows []library.Entry
	if err != nil || json.Unmarshal([]byte(out), &rows) != nil || len(rows) != 2 || rows[0].Title != "Alpha" || rows[1].Title != "Beta" {
		t.Fatal(out, err)
	}
	out, _, err = h.run(t.Context(), "list", "--title", "aLPHa")
	if err != nil || !strings.Contains(out, "Alpha") || strings.Contains(out, "Beta") || !strings.Contains(out, "PINNED") {
		t.Fatal(out, err)
	}
	for _, flags := range [][]string{{"--after", "yesterday"}, {"--before", "2026-02-30"}, {"--sort", "secret"}, {"--order", "sideways"}, {"--status", "unknown"}} {
		if out, _, err := h.run(t.Context(), append([]string{"list"}, flags...)...); err == nil || out != "" {
			t.Fatal(flags, out, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if out, _, err := h.run(ctx, "list", "--json"); !errors.Is(err, context.Canceled) || out != "" {
		t.Fatal(out, err)
	}
	if out, _, err := h.run(ctx, "info", rows[0].Path); !errors.Is(err, context.Canceled) || out != "" {
		t.Fatal(out, err)
	}
}

func TestRecoverCommandRefusesActiveAndExistingFilesAndIndexesNewCopy(t *testing.T) {
	h := newCommandHarness(t)
	path := filepath.Join(t.TempDir(), "interrupted.tbt")
	w, err := recording.NewWriter(path, recording.Recording{Title: "Recovered demo", Sizes: []int{80, 24}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if err := w.Append(recording.Event{Time: 10, Lines: []string{"saved output"}}); err != nil {
		t.Fatal(err)
	}
	journal := path + ".partial"
	destination := filepath.Join(t.TempDir(), "recovered.tbt")
	if _, _, err := h.run(t.Context(), "recover", journal, "--output", destination); err == nil {
		t.Fatal("recovered an active journal")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.run(t.Context(), "recover", journal, "--output", destination); err == nil {
		t.Fatal("overwrote destination")
	}
	existing, err := os.ReadFile(destination)
	if err != nil || string(existing) != "existing" {
		t.Fatal(string(existing), err)
	}
	out, _, err := h.run(t.Context(), "recover", journal)
	if err != nil {
		t.Fatal(out, err)
	}
	recovered := strings.TrimSpace(out)
	if filepath.Dir(recovered) != filepath.Join(h.data, "recordings") {
		t.Fatal(recovered)
	}
	r, err := recording.Load(recovered)
	if err != nil || r.Title != "Recovered demo" || r.Lines[0].Lines[0] != "saved output" {
		t.Fatal(r, err)
	}
	entries, err := (library.Library{Root: h.data}).List()
	if err != nil || len(entries) != 1 || entries[0].Path != recovered || entries[0].Status != "ready" {
		t.Fatal(entries, err)
	}
	after, err := os.ReadFile(journal)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("recovery changed its source", err)
	}
	if _, _, err := h.run(t.Context(), "recover", recovered); err == nil {
		t.Fatal("recovered a finalized file")
	}
	for _, args := range [][]string{{"import", "missing.tbt"}, {"import", h.data}, {"info", "missing.tbt"}} {
		if _, _, err := h.run(t.Context(), args...); err == nil {
			t.Fatal(args)
		}
	}
}
