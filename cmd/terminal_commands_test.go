//go:build unix

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/uitest"
)

func TestManageCommandLoadsLocalSourceAndRestoresTerminalOnQuitOrCancel(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "quit", true: "cancel"}[canceled], func(t *testing.T) {
			h := newCommandHarness(t)
			h.recording(&recording.Recording{Title: "Command manager fixture", Sizes: []int{80, 24}})
			tty := uitest.Open(t, 110, 30)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			c := h.command("manage")
			c.SetIn(tty.Slave)
			c.SetOut(tty.Slave)
			done := make(chan error, 1)
			stopped := make(chan struct{})
			go func() { defer close(stopped); done <- c.ExecuteContext(ctx) }()
			t.Cleanup(func() { cancel(); <-stopped })
			tty.Wait(ctx, "Which recordings")
			tty.Send("1")
			tty.Wait(ctx, "Command manager fixture")
			if canceled {
				cancel()
			} else {
				tty.Send("q")
			}
			select {
			case err := <-done:
				if !canceled && err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				if !canceled {
					t.Fatal("manager did not quit")
				}
				<-stopped
			}
			tty.Restored()
		})
	}
}

func TestStorageCleanupCommandRequiresTypedConfirmationOnTerminal(t *testing.T) {
	for _, confirmation := range []string{"keep", "delete"} {
		t.Run(confirmation, func(t *testing.T) {
			h := newCommandHarness(t)
			path := filepath.Join(h.data, "exports", "transcript.txt")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			tty := uitest.Open(t, 110, 30)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			c := h.command("storage", "clean", "--kind", "exports", "--apply", "--json")
			c.SetIn(tty.Slave)
			c.SetOut(tty.Slave)
			c.SetErr(tty.Slave)
			done := make(chan error, 1)
			stopped := make(chan struct{})
			go func() { defer close(stopped); done <- c.ExecuteContext(ctx) }()
			// A failed assertion must also release the canonical line reader.
			t.Cleanup(func() { cancel(); _, _ = tty.Master.Write([]byte("\n")); <-stopped })
			tty.Wait(ctx, "Type delete:")
			tty.Send(confirmation + "\n")
			select {
			case err := <-done:
				if confirmation == "delete" {
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil || !strings.Contains(err.Error(), "cleanup canceled") {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("cleanup did not finish")
			}
			_, err := os.Stat(path)
			if confirmation == "delete" {
				if !os.IsNotExist(err) {
					t.Fatal("confirmed cleanup retained file", err)
				}
			} else if err != nil {
				t.Fatal("canceled cleanup removed file", err)
			}
			tty.Restored()
		})
	}
}

func TestStoragePinCommandsAndReadableUsageAndCleanup(t *testing.T) {
	h := newCommandHarness(t)
	path, entry := h.recording(&recording.Recording{Title: "Pinned fixture", Sizes: []int{80, 24}})
	for _, action := range []string{"pin", "unpin"} {
		out, _, err := h.run(t.Context(), "storage", action, entry.ID[:12])
		if err != nil || out != action+" "+entry.ID+"\n" {
			t.Fatal(out, err)
		}
		out, _, err = h.run(t.Context(), "list", "--json", "--pinned="+map[string]string{"pin": "true", "unpin": "false"}[action])
		if err != nil || !strings.Contains(out, "Pinned fixture") {
			t.Fatal(out, err)
		}
	}
	out, _, err := h.run(t.Context(), "storage", "usage")
	if err != nil || !strings.Contains(out, "recordings: 1 files") || !strings.Contains(out, "external:") {
		t.Fatal(out, err)
	}
	out, _, err = h.run(t.Context(), "storage", "clean", "--kind", "recordings")
	if err != nil || !strings.Contains(out, "Preview only.") || !strings.Contains(out, path) {
		t.Fatal(out, err)
	}
	for _, flags := range [][]string{{"--kind", "private"}, {"--min-size", "-1"}, {"--older-than", "yesterday"}} {
		if _, _, err := h.run(t.Context(), append([]string{"storage", "clean"}, flags...)...); err == nil {
			t.Fatal(flags)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := h.run(ctx, "storage", "usage", "--json"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
