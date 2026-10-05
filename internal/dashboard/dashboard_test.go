//go:build unix

package dashboard

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/termbacktime/termbacktime/internal/live"
	"github.com/termbacktime/termbacktime/internal/terminal"
	"golang.org/x/term"
)

func TestDashboardPTYInputResizeAndRestoration(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		mode                 Mode
		initialRows, endRows int
	}{
		{"recording", RecordingMode, 25, 31},
		{"live recording", LiveMode, 21, 27},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testDashboardPTYInputResizeAndRestoration(t, tc.mode, tc.initialRows, tc.endRows)
		})
	}
}

func testDashboardPTYInputResizeAndRestoration(t *testing.T, mode Mode, initialRows, endRows int) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	if err = pty.Setsize(slave, &pty.Winsize{Cols: 90, Rows: 30}); err != nil {
		t.Fatal(err)
	}
	old, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	d := New(slave, "/private/test-recording.json", mode, nil)
	cols, rows := d.Geometry()
	if cols != 90 || rows != initialRows {
		t.Fatalf("initial pane = %dx%d, want 90x%d", cols, rows, initialRows)
	}
	d.Set(func(s *State) {
		s.Viewer = "https://example.invalid/#k=private-viewer"
		s.Host = "https://example.invalid/#m=private-manager"
		s.Diagnostics = live.Diagnostics{ObservedAt: time.Now(), RTTAvailable: true, RTT: 123 * time.Millisecond, RateAvailable: true, BytesPerSecond: 1048576, BufferAvailable: true, BufferedBytes: 1024, ReconnectAttempts: 3}
	})
	var mu sync.Mutex
	var captured strings.Builder
	var sizes []terminal.Event
	rendered := make(chan struct{})
	var once sync.Once
	go func() {
		b := make([]byte, 32768)
		for {
			n, e := master.Read(b)
			if n > 0 && strings.Contains(string(b[:n]), "TermBackTime") {
				once.Do(func() { close(rendered) })
			}
			if e != nil {
				return
			}
		}
	}()
	done := make(chan error, 1)
	go func() {
		done <- terminal.Run(ctx, terminal.Options{Input: slave, Output: slave, Cols: cols, Rows: rows, Presentation: d, Shell: "/bin/sh", Args: []string{"-c", `stty -echo; printf 'READY\n'; read first; printf 'INPUT:%s\n' "$first"; read second; printf '\033[?1049h\033[31mFULLSCREEN 世界\033[0m'; stty size; printf '\033[?1049lDONE\n'`}, OnEvent: func(e terminal.Event) error {
			mu.Lock()
			defer mu.Unlock()
			captured.WriteString(e.Text)
			if e.Cols > 0 {
				sizes = append(sizes, e)
			}
			return nil
		}})
	}()
	select {
	case <-rendered:
	case <-ctx.Done():
		t.Fatal("dashboard did not render")
	}
	// Normal input is sent through Bubble Tea to the child; dashboard controls stay outside it.
	master.Write([]byte("hello\r"))
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	for {
		mu.Lock()
		seen := strings.Contains(captured.String(), "INPUT:hello")
		mu.Unlock()
		if seen {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("shell did not receive keyboard input")
		case <-time.After(10 * time.Millisecond):
		}
	}
	d.program.Send(tea.WindowSizeMsg{Width: 100, Height: 36})
	// Enter controls and forward a literal Ctrl+] before returning to shell input.
	master.Write([]byte{0x1d, ']', '\r'})
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("dashboard failed to stop")
	}
	current, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if *old != *current {
		t.Fatal("terminal modes not restored")
	}
	mu.Lock()
	defer mu.Unlock()
	text := captured.String()
	if !strings.Contains(text, "FULLSCREEN 世界") || !strings.Contains(text, fmt.Sprintf("%d 100", endRows)) || !strings.Contains(text, "DONE") {
		t.Fatalf("missing child output/resize: %q", text)
	}
	if len(sizes) == 0 || sizes[len(sizes)-1].Cols != 100 || sizes[len(sizes)-1].Rows != endRows {
		t.Fatal("pane resize was not recorded", sizes)
	}
	for _, private := range []string{"RTT to SFU", "Reconnects", "Connection good", "TermBackTime", "private-viewer", "private-manager", "test-recording", "dashboard controls", strings.Repeat("─", 90)} {
		if strings.Contains(text, private) {
			t.Fatal("dashboard content leaked into capture", private)
		}
	}
}
func TestDashboardRequiresTerminal(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if Check(f, f) == nil {
		t.Fatal("accepted redirected dashboard")
	}
}

func TestDashboardShowsOnlyRelevantSessionDetails(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode Mode
		path string
	}{
		{"recording", RecordingMode, "/recordings/example.json"},
		{"live", LiveMode, ""},
		{"live recording", LiveMode, "/recordings/example.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &Dashboard{mode: tc.mode, state: State{Path: tc.path, Room: "12 viewers", Viewer: "https://example.invalid/viewer", Host: "https://example.invalid/host"}}
			em := vt.NewEmulator(120, 24-tc.mode.chromeRows())
			defer em.Close()
			m := &model{owner: d, screen: em, started: time.Now(), cols: 120, rows: 24 - tc.mode.chromeRows()}
			for _, controls := range []bool{false, true} {
				m.controls = controls
				content := m.View().Content
				if got := strings.Count(content, "\n") + 1; got != 24 {
					t.Fatalf("dashboard occupies %d rows, want 24", got)
				}
				rows := strings.Split(ansi.Strip(content), "\n")
				if rows[1] != strings.Repeat("─", 120) || rows[m.rows+2] != strings.Repeat("─", 120) {
					t.Fatal("missing full-width separators")
				}
				for _, row := range rows {
					if ansi.StringWidth(row) != 120 {
						t.Fatal("dashboard does not fill viewport")
					}
				}
				for _, label := range []string{"12 viewers", "Viewer:", "Private host:", "https://example.invalid"} {
					if strings.Contains(content, label) != (tc.mode == LiveMode) {
						t.Fatalf("unexpected visibility of %q: %q", label, content)
					}
				}
				if strings.Contains(content, "open viewer/host") != (controls && tc.mode == LiveMode) {
					t.Fatal("link shortcuts do not match session mode")
				}
				if tc.path != "" && !strings.Contains(content, tc.path) {
					t.Fatal("recording location missing")
				}
			}
			_, cmd := m.Update(tea.KeyPressMsg{Code: 'v'})
			if (cmd != nil) != (tc.mode == LiveMode) {
				t.Fatal("link action does not match session mode")
			}
		})
	}
}

func TestDashboardCancellationRestoresTerminal(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	pty.Setsize(slave, &pty.Winsize{Cols: 80, Rows: 24})
	before, _ := term.GetState(int(slave.Fd()))
	go io.Copy(io.Discard, master)
	ctx, cancel := context.WithCancel(t.Context())
	d := New(slave, "", LiveMode, nil)
	done := make(chan error, 1)
	go func() {
		done <- terminal.Run(ctx, terminal.Options{Input: slave, Output: slave, Cols: 80, Rows: 18, Presentation: d, Shell: "/bin/sh", Args: []string{"-c", "printf ready; sleep 30"}})
	}()
	select {
	case <-d.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation leaked background work")
	}
	after, _ := term.GetState(int(slave.Fd()))
	if *before != *after {
		t.Fatal("cancellation failed to restore terminal")
	}
}

func TestDashboardPasteMouseAndTerminalReplies(t *testing.T) {
	em := vt.NewEmulator(80, 20)
	output := make(chan string, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		b := make([]byte, 1024)
		for {
			n, e := em.Read(b)
			if n > 0 {
				output <- string(b[:n])
			}
			if e != nil {
				return
			}
		}
	}()
	defer func() { em.InputPipe().(io.Closer).Close(); <-done; em.Close() }()
	m := &model{screen: em, cols: 80, rows: 20}
	m.Update(outputMsg("\x1b[?2004h\x1b[?1000h\x1b[?1006h"))
	m.Update(tea.PasteMsg{Content: "hello 世界"})
	take := func(want string) {
		t.Helper()
		select {
		case got := <-output:
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatal("missing terminal input")
		}
	}
	take("\x1b[200~")
	take("hello 世界")
	take("\x1b[201~")
	m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	take("\x03")
	m.Update(tea.MouseClickMsg{X: 4, Y: 3, Button: tea.MouseLeft})
	take("\x1b[<0;5;2M")
	m.Update(outputMsg("\x1b[6n"))
	take("\x1b[1;1R")
}

func TestDiagnosticsUnavailableAndStaleRendering(t *testing.T) {
	now := time.Unix(100, 0)
	text := diagnosticsText(live.Diagnostics{}, now)
	if strings.Count(text, "unavailable") != 4 {
		t.Fatal(text)
	}
	text = diagnosticsText(live.Diagnostics{ObservedAt: now.Add(-6 * time.Second), RTTAvailable: true, RTT: 120 * time.Millisecond}, now)
	if !strings.Contains(text, "stale") || !strings.Contains(text, "120 ms") {
		t.Fatal(text)
	}
}
