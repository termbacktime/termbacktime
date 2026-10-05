//go:build unix

// Package uitest observes the rendered terminal, rather than matching raw output
// chunks which may contain earlier screens or incomplete escape sequences.
package uitest

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
	"github.com/termbacktime/termbacktime/internal/live"
	"golang.org/x/term"
)

type Terminal struct {
	Master, Slave *os.File
	screen        *live.Screen
	mu            sync.Mutex
	raw           strings.Builder
	stopped       chan struct{}
	before        *term.State
	t             *testing.T
}

func Open(t *testing.T, width, height int) *Terminal {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err = pty.Setsize(slave, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)}); err != nil {
		t.Fatal(err)
	}
	state, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	p := &Terminal{Master: master, Slave: slave, screen: live.NewScreen(width, height), stopped: make(chan struct{}), before: state, t: t}
	go func() {
		defer close(p.stopped)
		b := make([]byte, 32768)
		for {
			n, e := master.Read(b)
			if n > 0 {
				p.mu.Lock()
				p.raw.Write(b[:n])
				p.mu.Unlock()
				_ = p.screen.Write(string(b[:n]))
			}
			if e != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { slave.Close(); master.Close(); <-p.stopped; p.screen.Close() })
	return p
}
func (p *Terminal) Send(keys string) {
	p.t.Helper()
	if _, err := p.Master.Write([]byte(keys)); err != nil {
		p.t.Fatal(err)
	}
}
func (p *Terminal) Screen() string { f, _ := p.screen.Frame(true); return ansi.Strip(f.Screen) }
func (p *Terminal) Raw() string    { p.mu.Lock(); defer p.mu.Unlock(); return p.raw.String() }
func (p *Terminal) Wait(ctx context.Context, contains string, absent ...string) {
	p.t.Helper()
	for {
		text := p.Screen()
		good := strings.Contains(text, contains)
		for _, s := range absent {
			good = good && !strings.Contains(text, s)
		}
		if good {
			return
		}
		select {
		case <-ctx.Done():
			p.t.Fatalf("waiting for %q without %q:\n%s", contains, absent, text)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
func (p *Terminal) Restored() {
	p.t.Helper()
	after, err := term.GetState(int(p.Slave.Fd()))
	if err != nil || *p.before != *after {
		p.t.Fatal("terminal modes were not restored", err)
	}
	for i := 0; i < 100; i++ {
		frame, _ := p.screen.Frame(true)
		if !frame.Alternate && frame.Visible {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.t.Fatal("alternate screen or cursor visibility was not restored")
}
func (p *Terminal) Resize(width, height int) {
	p.t.Helper()
	if err := pty.Setsize(p.Slave, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)}); err != nil {
		p.t.Fatal(err)
	}
	p.screen.Resize(width, height)
}
