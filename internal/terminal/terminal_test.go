//go:build unix

package terminal

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSplitUTF8(t *testing.T) {
	var d Decoder
	if got := d.Push([]byte{0xe4, 0xb8}, false); got != "" {
		t.Fatal(got)
	}
	if got := d.Push([]byte{0x96, '!'}, false); got != "世!" {
		t.Fatal(got)
	}
}

func TestPTYOutputAndCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	in, _ := os.Open(os.DevNull)
	defer in.Close()
	out, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	var mu sync.Mutex
	var text strings.Builder
	err = Run(ctx, Options{Shell: "/bin/sh", Args: []string{"-c", "printf 'hello 世界\\n'"}, Input: in, Output: out, OnEvent: func(e Event) error {
		mu.Lock()
		defer mu.Unlock()
		text.WriteString(e.Text)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "hello 世界") {
		t.Fatal(text.String())
	}
}

func TestPTYLoginProfileAndExplicitCommandStartup(t *testing.T) {
	for _, test := range []struct {
		name  string
		args  []string
		login bool
		code  int
	}{
		{"interactive login shell", nil, true, 1},
		{"explicit shell without arguments", []string{}, false, 1},
		{"explicit command", []string{"-c", "printf explicit-command; exit 23"}, false, 23},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("PS1", "TBT READY> ")
			profile := "parse_git_branch() { printf PROFILE-BRANCH; }\nexport PS1='$(parse_git_branch) TBT READY> '\nprintf 'LOGIN-PROFILE\\n'\n"
			if err := os.WriteFile(filepath.Join(home, ".bash_profile"), []byte(profile), 0600); err != nil {
				t.Fatal(err)
			}
			// System bashrc files can replace an inherited PS1 on Linux.
			if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("export PS1='TBT READY> '\n"), 0600); err != nil {
				t.Fatal(err)
			}
			input, send, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			defer send.Close()
			output, err := os.CreateTemp(t.TempDir(), "output")
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			var text strings.Builder
			var readyOnce sync.Once
			ready := make(chan struct{})
			done := make(chan error, 1)
			stopped := make(chan struct{})
			go func() {
				defer close(stopped)
				done <- Run(ctx, Options{Shell: "/bin/bash", Args: test.args, Input: input, Output: output, OnEvent: func(e Event) error {
					text.WriteString(e.Text)
					if strings.Contains(text.String(), "TBT READY> ") {
						readyOnce.Do(func() { close(ready) })
					}
					return nil
				}})
			}()
			defer func() {
				cancel()
				<-stopped // Finish the pumps before closing their input/output files.
			}()
			if len(test.args) == 0 {
				select {
				case <-ready:
				case err := <-done:
					t.Fatalf("shell exited before its prompt: %v", err)
				case <-ctx.Done():
					t.Fatal("shell did not show its prompt")
				}
				if _, err := send.WriteString("test\nexit\n"); err != nil {
					t.Fatal(err)
				}
			}
			err = <-done
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != test.code {
				t.Fatalf("expected shell status %d, got %v", test.code, err)
			}
			got := text.String()
			if strings.Contains(got, "LOGIN-PROFILE") != test.login || strings.Contains(got, "PROFILE-BRANCH") != test.login || strings.Contains(got, "command not found") {
				t.Fatalf("incorrect shell startup: %s", got)
			}
			if len(test.args) > 0 && !strings.Contains(got, "explicit-command") {
				t.Fatal("explicit command output was lost")
			}
		})
	}
}
