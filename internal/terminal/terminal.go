//go:build unix

// Package terminal owns a PTY, input/output pumps and terminal restoration
package terminal

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

type Event struct {
	Text       string
	Lifecycle  *CommandEvent
	Cols, Rows int
}

// Presentation renders the same PTY stream without becoming part of it.
type Presentation interface {
	io.Writer
	Run(context.Context, *os.File, *os.File, func([]byte) error, func(int, int) error) error
	Close() error
}

type Options struct {
	Shell             string
	CommandCapture    bool
	CaptureContext    *ExecutionContext // reuse the context already probed for the recording header
	CaptureExecutable string
	CaptureNotice     func(string)
	Args              []string // nil starts a login shell; an empty slice preserves explicit command startup
	Input, Output     *os.File
	OnEvent           func(Event) error
	Cols, Rows        int
	Presentation      Presentation
}

func Size(f *os.File) (int, int) {
	c, r, err := term.GetSize(int(f.Fd()))
	if err != nil || c < 1 || r < 1 {
		return 80, 24
	}
	return min(c, 500), min(r, 200)
}

// Decoder preserves UTF-8 code points split across PTY reads
type Decoder struct{ pending []byte }

func (d *Decoder) Push(b []byte, final bool) string {
	d.pending = append(d.pending, b...)
	n := 0
	for n < len(d.pending) {
		if !final && !utf8.FullRune(d.pending[n:]) {
			break
		}
		_, s := utf8.DecodeRune(d.pending[n:])
		n += s
	}
	out := string(d.pending[:n])
	d.pending = append(d.pending[:0], d.pending[n:]...)
	return out
}

func Run(parent context.Context, o Options) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	if o.Input == nil {
		o.Input = os.Stdin
	}
	if o.Output == nil {
		o.Output = os.Stdout
	}
	if o.Shell == "" {
		o.Shell = os.Getenv("SHELL")
	}
	if o.Shell == "" {
		o.Shell = "/bin/sh"
	}
	cols, rows := Size(o.Output)
	if o.Cols > 0 && o.Rows > 0 {
		cols, rows = o.Cols, o.Rows
	}
	var mu sync.Mutex
	errCh := make(chan error, 1)
	fail := func(err error) {
		if err != nil {
			select {
			case errCh <- err:
			default:
			}
			cancel()
		}
	}
	emitCommand := func(c CommandEvent) error {
		mu.Lock()
		defer mu.Unlock()
		if o.OnEvent != nil {
			err := o.OnEvent(Event{Lifecycle: &c})
			fail(err)
			return err
		}
		return nil
	}
	var session *commandSession
	direct := o.CommandCapture && o.Args != nil
	if o.CommandCapture && !direct {
		var prepareErr error
		session, prepareErr = prepareCommandSession(ctx, o, emitCommand)
		if prepareErr != nil && o.CaptureNotice != nil {
			o.CaptureNotice(prepareErr.Error())
		}
		if session != nil {
			defer session.Close()
		}
	}
	childArgs := append([]string(nil), o.Args...)
	if session != nil {
		childArgs = append(session.args, childArgs...)
	}
	child := exec.Command(o.Shell, childArgs...)
	if o.Args == nil {
		// Interactive recordings should load the same login setup as a new
		// terminal, including prompt functions defined in .bash_profile.
		child.Args[0] = "-" + filepath.Base(o.Shell)
	}
	child.Env = append(os.Environ(), "TERM=xterm-256color")
	if session != nil {
		child.Env = append(child.Env, session.env...)
	}
	m, err := pty.StartWithSize(child, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return err
	}
	defer m.Close()
	var directID string
	if direct {
		directID = newCommandID()
		cwd, _ := os.Getwd()
		argv := append([]string{o.Shell}, o.Args...)
		bytes := 0
		for _, argument := range argv {
			bytes += len(argument)
		}
		partial := len(argv) > 4096 || bytes > 16384
		if partial {
			argv = nil
		}
		if err := emitCommand(CommandEvent{ID: directID, Phase: "start", CWD: cwd, Argv: argv, Partial: partial}); err != nil {
			_ = child.Process.Kill()
			_ = child.Wait()
			return err
		}
	}
	if session != nil {
		session.mu.Lock()
		session.pid = child.Process.Pid
		session.mu.Unlock()
		if session.bootstrap != "" {
			_ = captureEcho(m, false)
			if _, err = m.Write([]byte(session.bootstrap)); err != nil {
				_ = child.Process.Kill()
				_ = child.Wait()
				return err
			}
		}
	}
	if o.Presentation == nil && term.IsTerminal(int(o.Input.Fd())) {
		old, err := term.MakeRaw(int(o.Input.Fd()))
		if err != nil {
			_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
			_ = child.Wait()
			return err
		}
		defer term.Restore(int(o.Input.Fd()), old)
	}
	var decode Decoder
	emit := func(text string, final bool) error {
		mu.Lock()
		defer mu.Unlock()
		text = decode.Push([]byte(text), final)
		if text != "" && o.OnEvent != nil {
			return o.OnEvent(Event{Text: text})
		}
		return nil
	}
	var wg sync.WaitGroup
	resize := func(c, r int) error {
		mu.Lock()
		defer mu.Unlock()
		c, r = min(max(c, 1), 500), min(max(r, 1), 200)
		if err := pty.Setsize(m, &pty.Winsize{Cols: uint16(c), Rows: uint16(r)}); err != nil {
			return err
		}
		if o.OnEvent != nil {
			return o.OnEvent(Event{Cols: c, Rows: r})
		}
		return nil
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	defer signal.Stop(ch)
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
		_ = m.Close()
	}()
	awaitCapture := func() {
		if session != nil {
			select {
			case <-session.ready:
			case <-time.After(3 * time.Second):
				if o.CaptureNotice != nil {
					o.CaptureNotice("Command capture initialization unavailable; capture gaps will need review")
				}
			case <-ctx.Done():
			}
			if session.bootstrap != "" {
				_ = captureEcho(m, true)
			}
		}
	}
	if o.Presentation == nil {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ch:
					c, r := Size(o.Output)
					if err := resize(c, r); err != nil {
						fail(err)
						return
					}
				}
			}
		}()
		go func() {
			defer wg.Done()
			awaitCapture()
			buf := make([]byte, 4096)
			for ctx.Err() == nil {
				poll := []unix.PollFd{{Fd: int32(o.Input.Fd()), Events: unix.POLLIN}}
				n, err := unix.Poll(poll, 100)
				if errors.Is(err, unix.EINTR) {
					continue
				}
				if err != nil {
					fail(err)
					return
				}
				if n == 0 {
					continue
				}
				n, err = o.Input.Read(buf)
				if n > 0 {
					if _, e := m.Write(buf[:n]); e != nil {
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()
	} else {
		wg.Add(1)
		go func() {
			defer wg.Done()
			awaitCapture()
			err := o.Presentation.Run(ctx, o.Input, o.Output, func(b []byte) error { _, e := m.Write(b); return e }, resize)
			if ctx.Err() == nil {
				fail(err)
				cancel()
			}
		}()
	}
	var output io.Writer = o.Output
	if o.Presentation != nil {
		output = o.Presentation
	}

	buf := make([]byte, 32768)
	var outputErr error
	for {
		n, e := m.Read(buf)
		if n > 0 {
			if _, err = output.Write(buf[:n]); err != nil {
				outputErr = err
				break
			}
			if n > 0 {
				if err = emit(string(buf[:n]), false); err != nil {
					outputErr = err
					break
				}
			}
		}
		if e != nil {
			if !errors.Is(e, io.EOF) && !errors.Is(e, syscall.EIO) && ctx.Err() == nil {
				outputErr = e
			}
			break
		}
	}
	if outputErr == nil {
		outputErr = emit("", true)
	}
	if outputErr != nil {
		cancel()
	}
	waitErr := child.Wait()
	if session != nil {
		if err := session.finish(); err != nil && outputErr == nil {
			outputErr = err
		}
	}
	if direct {
		code := 0
		partial := false
		if waitErr != nil {
			var exit *exec.ExitError
			if errors.As(waitErr, &exit) && exit.ExitCode() >= 0 {
				code = exit.ExitCode()
			} else {
				code = 130
				partial = true
			}
		}
		if err := emitCommand(CommandEvent{ID: directID, Phase: "end", ExitCode: &code, Partial: partial}); err != nil && outputErr == nil {
			outputErr = err
		}
	}
	if o.Presentation != nil {
		_ = o.Presentation.Close()
	}
	cancel()
	wg.Wait()
	select {
	case e := <-errCh:
		return e
	default:
	}
	if outputErr != nil {
		return outputErr
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	return waitErr
}
