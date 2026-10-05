//go:build unix

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/uitest"
)

// The subprocess never contacts GitHub. Saving its upload lets the parent inspect
// the actual payload produced by the command, including temporary recordings.
func mockCommandProcessUpload(t *testing.T) {
	path := os.Getenv("TBT_COMMAND_TEST_UPLOAD")
	if path == "" {
		return
	}
	http.DefaultTransport = recordTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.github.com" {
			return nil, fmt.Errorf("unexpected test request: %s", r.URL)
		}
		if r.Method == http.MethodPatch {
			return jsonResponse(200, `{}`), nil
		}
		if r.Method != http.MethodPost || r.URL.Path != "/gists" || r.Header.Get("Authorization") != "Bearer test-token" {
			return nil, fmt.Errorf("unexpected test upload: %s %s", r.Method, r.URL)
		}
		var payload struct {
			Public bool `json:"public"`
			Files  map[string]struct {
				Content string `json:"content"`
			} `json:"files"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		if payload.Public {
			return nil, errors.New("test recording was made public")
		}
		rec, err := recording.ReadWrapped(strings.NewReader(payload.Files[gh.Filename].Content))
		if err != nil {
			return nil, err
		}
		if err := recording.Save(path, rec); err != nil {
			return nil, err
		}
		return jsonResponse(201, `{"id":"`+strings.Repeat("a", 32)+`"}`), nil
	})
}

func TestRecordInteractiveExitAllowsUploadAndPreservesCommandFailures(t *testing.T) {
	for _, test := range []struct {
		name      string
		upload    bool
		temporary bool
		command   []string
		input     string
		status    int
	}{
		{name: "interactive failed last command", input: "test\nexit\n"},
		{name: "interactive upload after failed last command", upload: true, input: "test\nexit\n"},
		{name: "temporary interactive upload", upload: true, temporary: true, input: "test\nexit\n"},
		{name: "explicit failure skips upload", upload: true, command: []string{"/bin/sh", "-c", "exit 7"}, status: 7},
		{name: "interactive signal skips upload", upload: true, input: "kill -KILL $$\n", status: 137},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newCommandHarness(t)
			t.Setenv("TERMBACKTIME_TOKEN", "test-token")
			t.Setenv("PS1", "$(parse_git_branch) TBT READY> ")
			temporary := t.TempDir()
			t.Setenv("TMPDIR", temporary)
			profile := "parse_git_branch() { printf PROFILE-BRANCH; }\nexport PS1='$(parse_git_branch) TBT READY> '\nprintf 'LOGIN-PROFILE\\n'\n"
			if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".bash_profile"), []byte(profile), 0600); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			path, uploaded := filepath.Join(dir, "recording.tbt"), filepath.Join(dir, "uploaded.tbt")
			args := []string{"--data-dir", h.data, "--config", h.config, "--endpoint", "https://site.example", "record", "--shell", "/bin/bash", "--no-metadata"}
			if test.upload {
				args = append(args, "--upload", "--storage", "gist")
			}
			if test.temporary {
				args = append(args, "--no-save")
			} else {
				args = append(args, "--output", path)
			}
			if test.command != nil {
				args = append(append(args, "--"), test.command...)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			c := commandProcess(t, ctx, args...)
			c.Env = append(c.Env, "TBT_COMMAND_TEST_UPLOAD="+uploaded)
			c.Dir = os.Getenv("HOME")
			tty := uitest.Open(t, 100, 24)
			c.Stdin, c.Stdout, c.Stderr = tty.Slave, tty.Slave, tty.Slave
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = c.Process.Kill() })
			if test.command == nil {
				tty.Wait(ctx, "PROFILE-BRANCH TBT READY>", "command not found")
				tty.Send(test.input)
			}
			err := c.Wait()
			status := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatal(err)
				}
				status = exit.ExitCode()
			}
			if status != test.status {
				t.Fatalf("expected status %d, got %d: %v\n%s", test.status, status, err, tty.Raw())
			}
			expectUpload := test.upload && test.status == 0
			if expectUpload {
				tty.Wait(ctx, "https://site.example/p/"+strings.Repeat("a", 32))
				r, err := recording.Load(uploaded)
				if err != nil {
					t.Fatal("interactive recording did not upload", err)
				}
				var text strings.Builder
				for _, event := range r.Lines {
					text.WriteString(strings.Join(event.Lines, ""))
				}
				if !strings.Contains(text.String(), "PROFILE-BRANCH") || strings.Contains(text.String(), "command not found") {
					t.Fatal("upload lost the configured prompt", text.String())
				}
			} else if _, err := os.Stat(uploaded); !os.IsNotExist(err) {
				t.Fatal("unexpected upload", err)
			}
			if !test.temporary {
				tty.Wait(ctx, "Saved ")
				if _, err := recording.Load(path); err != nil {
					t.Fatal("local recording was lost", err)
				}
				if _, err := os.Stat(path + ".partial"); !os.IsNotExist(err) {
					t.Fatal("finalized journal was not cleaned", err)
				}
			}
			tty.Restored()
			requireEmptyDirectory(t, temporary)
		})
	}
}
