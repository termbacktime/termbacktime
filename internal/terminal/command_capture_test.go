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

func TestCommandCapturePTY(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "termbacktime")
	build := exec.Command("go", "build", "-o", exe, "../..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v %s", err, out)
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			bin, err := exec.LookPath(shell)
			if err != nil {
				t.Skip("interpreter unavailable")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			if !supported(ShellContext(ctx, bin, nil)) {
				t.Skip("interpreter too old")
			}
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("ZDOTDIR", home)
			t.Setenv("PS1", "CAPTURE-READY> ")
			t.Setenv("HISTFILE", filepath.Join(home, "history"))
			t.Setenv("HISTCONTROL", "ignoredups")
			temp := t.TempDir()
			t.Setenv("TMPDIR", temp)
			profile := "export PS1='CAPTURE-READY> '\nprintf 'PROFILE-LOADED\\n'\n"
			if shell == "bash" {
				profile += "PROMPT_COMMAND=('printf USER-HOOK')\n"
			}
			file := ".bash_profile"
			if shell == "zsh" {
				_ = os.Unsetenv("ZDOTDIR")
				file = ".zshrc"
				profile = "if [[ -v ZDOTDIR ]]; then print INCORRECT-ZDOTDIR; fi\nPROMPT='CAPTURE-READY> '\nprint PROFILE-LOADED\nprecmd() { print -n USER-HOOK; }\n"
			}
			if shell == "fish" {
				file = ".config/fish/config.fish"
				profile = "printf 'PROFILE-LOADED\\n'\nfunction fish_prompt; printf 'CAPTURE-READY> '; end\n"
			}
			_ = os.MkdirAll(filepath.Dir(filepath.Join(home, file)), 0700)
			if err := os.WriteFile(filepath.Join(home, file), []byte(profile), 0600); err != nil {
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
			var mu sync.Mutex
			events := []CommandEvent{}
			done := make(chan error, 1)
			go func() {
				done <- Run(ctx, Options{Shell: bin, Input: input, Output: output, CommandCapture: true, CaptureExecutable: exe, CaptureNotice: func(s string) { t.Log(s) }, OnEvent: func(e Event) error {
					if e.Lifecycle != nil {
						mu.Lock()
						events = append(events, *e.Lifecycle)
						mu.Unlock()
					}
					return nil
				}})
			}()
			// Input pumping waits for the private ready message, independently of prompt rendering.
			status := "$?"
			if shell == "fish" {
				status = "$status"
			}
			_, _ = send.WriteString("printf 'hello 世界\\n'\nprintf 'hello 世界\\n'\nfalse\nprintf 'LAST:%s\\n' \"" + status + "\"\npwd\ncd '" + home + "'\nprintf '%s\\n' 'a quoted value' | cat\nprintf '%s\\n' 'first\nsecond'\npwd\nexit\n")
			err = <-done
			if err != nil {
				if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 0 {
					t.Fatal(err)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			starts := []CommandEvent{}
			ends := []CommandEvent{}
			for _, e := range events {
				if e.Phase == "start" {
					starts = append(starts, e)
				} else {
					ends = append(ends, e)
				}
			}
			if len(starts) != 10 || len(ends) != 10 {
				t.Fatalf("events: %#v", events)
			}
			if !strings.Contains(starts[0].Text, "hello 世界") || starts[0].Partial {
				t.Fatalf("first command %#v", starts[0])
			}
			if shell == "bash" && !starts[1].Partial {
				t.Fatal("ignored duplicate must be a gap")
			}
			if ends[2].ExitCode == nil || *ends[2].ExitCode != 1 {
				t.Fatal("failed status lost", ends[2])
			}
			if strings.TrimSpace(starts[2].Text) != "false" {
				t.Fatal("previous history entry substituted", starts[2])
			}
			if starts[6].CWD != home || !strings.Contains(starts[6].Text, "| cat") || !strings.Contains(starts[7].Text, "first\nsecond") {
				t.Fatalf("directory, pipeline or multiline lost: %#v", starts)
			}
			bytes, _ := os.ReadFile(output.Name())
			if !strings.Contains(string(bytes), "LAST:1") {
				t.Fatalf("command capture changed the shell's exit status: %s", bytes)
			}
			if !strings.Contains(string(bytes), "PROFILE-LOADED") || strings.Contains(string(bytes), "builtin source") || strings.Contains(string(bytes), "__tbt_") {
				t.Fatalf("bootstrap or profile output: %s", bytes)
			}
			if shell != "fish" && !strings.Contains(string(bytes), "USER-HOOK") {
				t.Fatal("existing prompt hook was lost")
			}
			if strings.Contains(string(bytes), "INCORRECT-ZDOTDIR") {
				t.Fatal("startup adapter changed an unset ZDOTDIR")
			}
			history, _ := os.ReadFile(filepath.Join(home, "history"))
			if strings.Contains(string(history), "builtin source") || strings.Contains(string(history), "__tbt_") {
				t.Fatal("bootstrap leaked into history")
			}
			entries, _ := os.ReadDir(temp)
			if len(entries) != 0 {
				t.Fatal("capture temporary files leaked", entries)
			}
		})
	}
}
func TestExplicitCommandCaptureAndOptOut(t *testing.T) {
	for _, capture := range []bool{false, true} {
		var events []CommandEvent
		in, _ := os.Open(os.DevNull)
		defer in.Close()
		out, _ := os.CreateTemp(t.TempDir(), "out")
		defer out.Close()
		err := Run(t.Context(), Options{Shell: "/bin/sh", Args: []string{"-c", "printf '%s' 'a quoted value'; exit 7"}, Input: in, Output: out, CommandCapture: capture, OnEvent: func(e Event) error {
			if e.Lifecycle != nil {
				events = append(events, *e.Lifecycle)
			}
			return nil
		}})
		if err == nil {
			t.Fatal("exit status lost")
		}
		if capture {
			if len(events) != 2 || len(events[0].Argv) != 3 || events[1].ExitCode == nil || *events[1].ExitCode != 7 {
				t.Fatal(events)
			}
		} else if len(events) != 0 {
			t.Fatal("opt-out captured commands")
		}
	}
}

type capturePresentation struct{ command string }

func (*capturePresentation) Write(data []byte) (int, error) { return len(data), nil }
func (*capturePresentation) Close() error                   { return nil }
func (p *capturePresentation) Run(ctx context.Context, _, _ *os.File, write func([]byte) error, _ func(int, int) error) error {
	command := p.command
	if command == "" {
		command = "echo started; sleep 10\n"
	}
	if err := write([]byte(command)); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestCommandCapturePropagatesRecordingFailures(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "termbacktime")
	if out, err := exec.Command("go", "build", "-o", exe, "../..").CombinedOutput(); err != nil {
		t.Fatalf("helper %v %s", err, out)
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		for _, phase := range []string{"start", "end"} {
			t.Run(shell+"/"+phase, func(t *testing.T) {
				bin, err := exec.LookPath(shell)
				if err != nil || !supported(ShellContext(t.Context(), bin, nil)) {
					t.Skip("supported interpreter unavailable")
				}
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("ZDOTDIR", home)
				input, err := os.Open(os.DevNull)
				if err != nil {
					t.Fatal(err)
				}
				defer input.Close()
				output, err := os.CreateTemp(t.TempDir(), "out")
				if err != nil {
					t.Fatal(err)
				}
				defer output.Close()
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				failure := errors.New("recording write failed")
				err = Run(ctx, Options{Shell: bin, Input: input, Output: output, CommandCapture: true, CaptureExecutable: exe, Presentation: &capturePresentation{command: "echo done\n"}, OnEvent: func(e Event) error {
					if e.Lifecycle != nil && e.Lifecycle.Phase == phase {
						return failure
					}
					return nil
				}})
				if !errors.Is(err, failure) || ctx.Err() != nil {
					t.Fatal("capture failure did not promptly reach the caller", err, ctx.Err())
				}
			})
		}
	}
}
func TestCommandCaptureInterruptedPresentation(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "termbacktime")
	if out, err := exec.Command("go", "build", "-o", exe, "../..").CombinedOutput(); err != nil {
		t.Fatalf("helper %v %s", err, out)
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			bin, err := exec.LookPath(shell)
			if err != nil || !supported(ShellContext(t.Context(), bin, nil)) {
				t.Skip("supported interpreter unavailable")
			}
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("ZDOTDIR", home)
			input, _ := os.Open(os.DevNull)
			defer input.Close()
			output, _ := os.CreateTemp(t.TempDir(), "out")
			defer output.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var events []CommandEvent
			err = Run(ctx, Options{Shell: bin, Input: input, Output: output, CommandCapture: true, CaptureExecutable: exe, Presentation: &capturePresentation{}, OnEvent: func(e Event) error {
				if e.Lifecycle != nil {
					events = append(events, *e.Lifecycle)
					if e.Lifecycle.Phase == "start" {
						time.AfterFunc(50*time.Millisecond, cancel)
					}
				}
				return nil
			}})
			if err == nil || len(events) != 2 || events[0].Phase != "start" || events[1].Phase != "end" || !events[1].Partial {
				t.Fatalf("interruption lost boundary: %v %#v", err, events)
			}
		})
	}
}

func TestBashHistoryFilteringPreservesExistingHistory(t *testing.T) {
	bin, err := exec.LookPath("bash")
	if err != nil || !supported(ShellContext(t.Context(), bin, nil)) {
		t.Skip("supported Bash unavailable")
	}
	exe := filepath.Join(t.TempDir(), "termbacktime")
	if out, err := exec.Command("go", "build", "-o", exe, "../..").CombinedOutput(); err != nil {
		t.Fatalf("helper %v %s", err, out)
	}
	home := t.TempDir()
	history := filepath.Join(home, "history")
	_ = os.WriteFile(history, []byte("echo keep-me\n"), 0600)
	_ = os.WriteFile(filepath.Join(home, ".bash_profile"), []byte("PS1='READY> '\n"), 0600)
	t.Setenv("HOME", home)
	t.Setenv("HISTFILE", history)
	t.Setenv("HISTCONTROL", "ignoreboth")
	input, send, _ := os.Pipe()
	defer input.Close()
	defer send.Close()
	output, _ := os.CreateTemp(t.TempDir(), "out")
	defer output.Close()
	_, _ = send.WriteString(" printf 'ignored\\n'\necho captured\nexit\n")
	var starts []CommandEvent
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err = Run(ctx, Options{Shell: bin, Input: input, Output: output, CommandCapture: true, CaptureExecutable: exe, OnEvent: func(e Event) error {
		if e.Lifecycle != nil && e.Lifecycle.Phase == "start" {
			starts = append(starts, *e.Lifecycle)
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(starts) != 3 || !starts[0].Partial || starts[0].Text != "" || strings.TrimSpace(starts[1].Text) != "echo captured" {
		t.Fatal("filtered input reused history", starts)
	}
	data, _ := os.ReadFile(history)
	if !strings.Contains(string(data), "echo keep-me") || strings.Contains(string(data), "builtin source") {
		t.Fatal("existing history changed or bootstrap leaked", string(data))
	}
}
