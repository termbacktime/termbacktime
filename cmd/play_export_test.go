package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/termbacktime/termbacktime/internal/history"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
)

func TestPlaybackCommandValidationAndNoninteractiveHistoryIsolation(t *testing.T) {
	h := newCommandHarness(t)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--speed", "0"}, "speed"}, {[]string{"--speed", "-1"}, "speed"},
		{[]string{"--speed", "NaN"}, "speed"}, {[]string{"--speed", "+Inf"}, "speed"},
		{[]string{"--speed", "100.01"}, "speed"}, {[]string{"--idle-limit", "-1s"}, "idle limit"},
	} {
		out, _, err := h.run(t.Context(), append([]string{"play", "missing.tbt"}, test.args...)...)
		if err == nil || !strings.Contains(err.Error(), test.want) || out != "" {
			t.Fatal(test.args, out, err)
		}
	}
	path, entry := h.recording(&recording.Recording{Sizes: []int{40, 8}, Lines: []recording.Event{{Lines: []string{"first\r\n"}}, {Time: 10000, Lines: []string{"second"}}}})
	r, err := recording.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	store := history.Store{Root: h.data}
	if err := store.Enable(true); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(r.HistoryID, 1200, 10000); err != nil {
		t.Fatal(err)
	}
	before, err := store.State()
	if err != nil {
		t.Fatal(err)
	}
	for _, flags := range [][]string{nil, {"--no-interactive"}, {"--no-history"}} {
		args := append([]string{"play", entry.ID[:12], "--speed", "100", "--idle-limit", "1ms"}, flags...)
		out, _, err := h.run(t.Context(), args...)
		if err != nil || !strings.Contains(out, "first") || !strings.Contains(out, "second") {
			t.Fatal(out, err)
		}
		after, err := store.State()
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("noninteractive playback changed history", after, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := h.run(ctx, "play", path); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestLocalOrRemoteResolutionDoesNotReplaceMissingLibraryEntriesWithShares(t *testing.T) {
	h := newCommandHarness(t)
	path, entry := h.recording(&recording.Recording{Sizes: []int{80, 24}})
	o := &options{dataDir: h.data}
	for _, value := range []string{path, entry.ID, entry.ID[:12]} {
		resolved, local, err := localOrRemote(o, value)
		if err != nil || !local || resolved != path {
			t.Fatal(value, resolved, local, err)
		}
	}
	for _, value := range []string{strings.Repeat("b", 32), "github-user/" + strings.Repeat("b", 32), "https://play.example/p/" + strings.Repeat("b", 32) + "#t=1"} {
		resolved, local, err := localOrRemote(o, value)
		if err != nil || local || resolved != value {
			t.Fatal(value, resolved, local, err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := localOrRemote(o, entry.ID); !errors.Is(err, library.ErrMissing) {
		t.Fatal(err)
	}
	for _, value := range []string{"missing.tbt", "https://unrelated.example/random", "not-a-recording"} {
		if _, _, err := localOrRemote(o, value); err == nil {
			t.Fatal(value)
		}
	}
}

func TestExportCommandFailuresLeaveNoPartialTranscriptOrOverwrite(t *testing.T) {
	h := newCommandHarness(t)
	path, _ := h.recording(&recording.Recording{Sizes: []int{80, 24}, Lines: []recording.Event{{Lines: []string{"hello"}}}})
	if out, _, err := h.run(t.Context(), "export", path); err == nil || !strings.Contains(err.Error(), "--output") || out != "" {
		t.Fatal(out, err)
	}
	for _, test := range []struct {
		name                      string
		cancel, conflict, missing bool
	}{
		{"cancellation", true, false, false}, {"existing destination", false, true, false}, {"missing source", false, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			destination := filepath.Join(dir, "transcript.md")
			if test.conflict {
				if err := os.WriteFile(destination, []byte("existing"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.cancel {
				cancel()
			}
			source := path
			if test.missing {
				source = "missing.tbt"
			}
			out, _, err := h.run(ctx, "export", source, "--format", "md", "--output", destination)
			if err == nil || out != "" || test.cancel && !errors.Is(err, context.Canceled) {
				t.Fatal(out, err)
			}
			data, err := os.ReadFile(destination)
			if test.conflict {
				if err != nil || string(data) != "existing" {
					t.Fatal(string(data), err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
			files, err := filepath.Glob(filepath.Join(dir, ".tbt-write-*"))
			if err != nil || len(files) != 0 {
				t.Fatal("partial transcript leaked", files, err)
			}
		})
	}
}
