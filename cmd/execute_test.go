//go:build unix

package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/termbacktime/termbacktime/internal/buildinfo"
	"github.com/termbacktime/termbacktime/internal/recording"
)

// Execute intentionally exits the process. A child test binary exercises the
// real entry point without replacing os.Exit or requiring a separate build.
func TestCommandProcess(t *testing.T) {
	if os.Getenv("TBT_COMMAND_TEST_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			mockCommandProcessUpload(t)
			os.Args = append([]string{"termbacktime"}, os.Args[i+1:]...)
			buildinfo.Version = "dev"
			Execute()
			os.Exit(0)
		}
	}
	t.Fatal("missing child command")
}

func commandProcess(t *testing.T, ctx context.Context, args ...string) *exec.Cmd {
	t.Helper()
	c := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestCommandProcess$", "--"}, args...)...)
	c.Env = append(os.Environ(), "TBT_COMMAND_TEST_PROCESS=1", "GORACE=atexit_sleep_ms=0")
	return c
}

func TestExecutePreservesSuccessErrorsAndChildSignalStatus(t *testing.T) {
	for _, test := range []struct {
		name    string
		args    []string
		status  int
		message string
		record  bool
	}{
		{"version", []string{"--version"}, 0, "termbacktime dev", false},
		{"invalid flag", []string{"--not-a-real-flag"}, 1, "Error: unknown flag", false},
		{"missing argument", []string{"export"}, 1, "Error:", false},
		{"record success", []string{"record", "--no-metadata", "--", "/bin/sh", "-c", "printf captured"}, 0, "captured", true},
		{"child failure", []string{"record", "--no-metadata", "--", "/bin/sh", "-c", "printf captured; exit 23"}, 23, "Error:", true},
		{"child signal", []string{"record", "--no-metadata", "--", "/bin/sh", "-c", "printf captured; kill -TERM $$"}, 143, "Error:", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newCommandHarness(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "recording.tbt")
			args := []string{"--data-dir", h.data, "--config", h.config}
			if test.record {
				args = append(args, "record", "--output", path)
				args = append(args, test.args[1:]...)
			} else {
				args = append(args, test.args...)
			}
			out, err := commandProcess(t, ctx, args...).CombinedOutput()
			status := 0
			if err != nil {
				var child *exec.ExitError
				if !errors.As(err, &child) {
					t.Fatal(err)
				}
				status = child.ExitCode()
			}
			if status != test.status || !strings.Contains(string(out), test.message) {
				t.Fatal(status, string(out), err)
			}
			if test.record {
				r, err := recording.Load(path)
				if err != nil || len(r.Lines) == 0 || !strings.Contains(r.Lines[0].Lines[0], "captured") {
					t.Fatal(r, err)
				}
				if _, err := os.Stat(path + ".partial"); !os.IsNotExist(err) {
					t.Fatal("finalized journal was not cleaned", err)
				}
			}
		})
	}
}

func TestExecuteSIGTERMCancelsCaptureAndFinalizesRecording(t *testing.T) {
	h := newCommandHarness(t)
	dir := t.TempDir()
	path, ready := filepath.Join(dir, "recording.tbt"), filepath.Join(dir, "ready")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	c := commandProcess(t, ctx, "--data-dir", h.data, "--config", h.config, "record", "--no-metadata", "--output", path, "--", "/bin/sh", "-c", `printf ready > "$1"; exec sleep 10`, "test-shell", ready)
	var output bytes.Buffer
	c.Stdout, c.Stderr = &output, &output
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Process.Kill() })
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("recording command did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := c.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err := c.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(output.String(), "context canceled") || !strings.Contains(output.String(), "Saved ") {
		t.Fatal(output.String(), err)
	}
	if _, err := recording.Load(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".partial"); !os.IsNotExist(err) {
		t.Fatal("cancellation left an unfinished journal", err)
	}
}
