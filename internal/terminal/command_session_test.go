//go:build unix

package terminal

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func captureTestSession(t *testing.T, emit func(CommandEvent) error) *commandSession {
	t.Helper()
	s, err := prepareCommandSession(t.Context(), Options{CaptureContext: &ExecutionContext{Shell: "bash", Version: "5.3"}}, emit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.mu.Lock()
	s.pid = os.Getpid()
	s.mu.Unlock()
	t.Setenv("TBT_CAPTURE_TOKEN", s.token)
	t.Setenv("TBT_CAPTURE_SOCKET", s.socket)
	return s
}

func sendCapture(t *testing.T, phase, input string) error {
	t.Helper()
	return RunCaptureHelper(strings.NewReader(input), []string{phase, strconv.Itoa(os.Getpid())})
}

func TestCaptureSessionReusesShellProbe(t *testing.T) {
	dir := t.TempDir()
	shell, probes := filepath.Join(dir, "bash"), filepath.Join(dir, "probes")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nprintf 'probe\\n' >> "+captureQuote(probes)+"\nprintf 'GNU bash, version 5.3.0\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	x := ShellContext(t.Context(), shell, nil)
	s, err := prepareCommandSession(t.Context(), Options{Shell: shell, CaptureContext: &x}, func(CommandEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	data, err := os.ReadFile(probes)
	if err != nil || string(data) != "probe\n" {
		t.Fatal("shell was probed more than once", string(data), err)
	}
}

func TestCaptureSessionMissingEndIsIncomplete(t *testing.T) {
	events := make(chan CommandEvent, 2)
	s := captureTestSession(t, func(e CommandEvent) error { events <- e; return nil })
	if err := sendCapture(t, "start", "exec true\x00/tmp\x00\x000\x00"); err != nil {
		t.Fatal(err)
	}
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	start, end := <-events, <-events
	if end.ID != start.ID || end.Phase != "end" || !end.Partial || end.ExitCode != nil {
		t.Fatal("missing command boundary invented a successful outcome", start, end)
	}
}

func TestCaptureSessionClosesIdleConnection(t *testing.T) {
	s := captureTestSession(t, func(CommandEvent) error { return nil })
	c, err := net.Dial("unix", s.socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	deadline := time.Now().Add(time.Second)
	for {
		s.connMu.Lock()
		accepted := s.conn != nil
		s.connMu.Unlock()
		if accepted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("connection was not accepted")
		}
		time.Sleep(time.Millisecond)
	}
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("idle client delayed capture cleanup")
	}
}

func TestCaptureSessionDoesNotAcknowledgeRecordingFailure(t *testing.T) {
	sentinel := errors.New("recording failed")
	captureTestSession(t, func(CommandEvent) error { return sentinel })
	if err := sendCapture(t, "start", "echo hello\x00/tmp\x00\x000\x00"); err == nil {
		t.Fatal("unsaved command was acknowledged")
	}
}

func TestCaptureEscapedInputAndOversizedCommands(t *testing.T) {
	events := make(chan CommandEvent, 8)
	captureTestSession(t, func(e CommandEvent) error { events <- e; return nil })
	// Escaping this valid input produces a message larger than 64 KiB.
	command := strings.Repeat("\x01", 14000)
	if err := sendCapture(t, "start", command+"\x00/tmp\x00\x000\x00"); err != nil {
		t.Fatal(err)
	}
	if e := <-events; e.Text != command || e.Partial {
		t.Fatal("escaped command was lost")
	}
	if err := sendCapture(t, "end", "\x00/tmp\x000\x000\x00"); err != nil {
		t.Fatal(err)
	}
	<-events
	for _, size := range []int{20000, captureInputLimit + 1} {
		if err := sendCapture(t, "start", strings.Repeat("x", size)+"\x00/tmp\x00\x000\x00"); err != nil {
			t.Fatal(err)
		}
		if e := <-events; e.Text != "" || !e.Partial {
			t.Fatal("oversized command was not recorded as a gap", e)
		}
		if err := sendCapture(t, "end", "\x00/tmp\x000\x000\x00"); err != nil {
			t.Fatal(err)
		}
		<-events
	}
}

func TestCaptureHelperRejectsInvalidBoundaries(t *testing.T) {
	for _, args := range [][]string{{"start", "-1"}, {"start", "invalid"}, {"unknown", "1"}} {
		if err := RunCaptureHelper(strings.NewReader(""), args); err == nil {
			t.Fatal("invalid helper arguments accepted", args)
		}
	}
	captureTestSession(t, func(CommandEvent) error { return nil })
	for _, input := range []string{"", "\x00/tmp\x00invalid\x00", "\x00/tmp\x00256\x00"} {
		if err := sendCapture(t, "end", input); err == nil {
			t.Fatal("invalid exit status became success", input)
		}
	}
}
