package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/termbacktime/termbacktime/internal/buildinfo"
	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/recording"
)

type recordTransport func(*http.Request) (*http.Response, error)

func (transport recordTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func requireEmptyDirectory(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty directory %s, found %d entries", path, len(entries))
	}
}

// Exercise a real PTY and inspect temporary files while the mocked upload is in progress
func TestRecordNoSaveCleansTemporaryFiles(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		command      []string
		status       int
		cancelUpload bool
		cancelRecord bool
		expectUpload bool
		expectError  bool
	}{
		{name: "successful upload", status: http.StatusCreated, expectUpload: true},
		{name: "failed upload", status: http.StatusInternalServerError, expectUpload: true, expectError: true},
		{name: "cancelled upload", cancelUpload: true, expectUpload: true, expectError: true},
		{name: "failed command", command: []string{"/bin/sh", "-c", "exit 7"}, expectError: true},
		{name: "failed PTY startup", command: []string{"/nonexistent/termbacktime-test-command"}, expectError: true},
		{name: "cancelled recording", command: []string{"/bin/sh", "-c", "exec sleep 10"}, cancelRecord: true, expectError: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			working := t.TempDir()
			temporary := t.TempDir()
			configPath := filepath.Join(t.TempDir(), "config.json")
			t.Chdir(working)
			t.Setenv("TMPDIR", temporary)
			t.Setenv("TERMBACKTIME_TOKEN", "test-token")
			previousTransport := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = previousTransport })
			timeout := 5 * time.Second
			if scenario.cancelRecord {
				timeout = 200 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			uploads := 0
			http.DefaultTransport = recordTransport(func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodPatch {
					return jsonResponse(200, `{}`), nil
				}
				uploads++
				if request.URL.String() != "https://api.github.com/gists" || request.Method != "POST" {
					t.Fatalf("unexpected upload request: %s %s", request.Method, request.URL)
				}
				if request.Header.Get("Authorization") != "Bearer test-token" {
					t.Fatal("upload omitted authentication")
				}
				var payload struct {
					Public bool `json:"public"`
					Files  map[string]struct {
						Content string `json:"content"`
					} `json:"files"`
				}
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				r, err := recording.ReadWrapped(strings.NewReader(payload.Files[gh.Filename].Content))
				if err != nil || payload.Public || r.Title != "Upload fixture" {
					t.Fatal("invalid secret Gist recording", err)
				}
				if r.Info.CLI != buildinfo.Tag() || r.Info.UploadCLI != buildinfo.Tag() {
					t.Fatalf("missing capture/upload versions: %+v", r.Info)
				}
				var text strings.Builder
				for _, event := range r.Lines {
					text.WriteString(strings.Join(event.Lines, ""))
				}
				if !strings.Contains(text.String(), "temporary upload fixture") {
					t.Fatal("upload lost terminal output")
				}

				entries, err := os.ReadDir(temporary)
				if err != nil || len(entries) != 2 {
					t.Fatal("upload did not isolate capture and upload metadata", err)
				}
				for _, entry := range entries {
					dir := filepath.Join(temporary, entry.Name())
					for path, mode := range map[string]os.FileMode{dir: 0700, filepath.Join(dir, "recording.tbt"): 0600} {
						info, err := os.Stat(path)
						if err != nil || info.Mode().Perm() != mode {
							t.Fatalf("incorrect temporary permissions for %s: %v", path, err)
						}
					}
					if _, err := recording.Load(filepath.Join(dir, "recording.tbt")); err != nil {
						t.Fatal("temporary recording was not finalized", err)
					}
				}
				requireEmptyDirectory(t, working)
				if scenario.cancelUpload {
					cancel()
					return nil, request.Context().Err()
				}
				return &http.Response{
					StatusCode: scenario.status,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"id":"` + strings.Repeat("a", 32) + `"}`)),
				}, nil
			})
			command := scenario.command
			if command == nil {
				command = []string{"/bin/sh", "-c", "printf 'temporary upload fixture\\n'"}
			}
			root := NewRoot()
			var output, diagnostic bytes.Buffer
			root.SetOut(&output)
			root.SetErr(&diagnostic)
			args := []string{"--config", configPath, "--endpoint", "https://site.example", "record", "--upload", "--storage", "gist", "--no-save", "--title", "Upload fixture", "--"}
			root.SetArgs(append(args, command...))
			err := root.ExecuteContext(ctx)
			if (err != nil) != scenario.expectError {
				t.Fatalf("unexpected recording result: %v", err)
			}
			if scenario.cancelUpload && !errors.Is(err, context.Canceled) {
				t.Fatalf("upload lost cancellation error: %v", err)
			}
			if scenario.cancelRecord && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("recording lost cancellation error: %v", err)
			}
			if (uploads == 1) != scenario.expectUpload {
				t.Fatalf("unexpected upload count: %d", uploads)
			}
			if !scenario.expectError && output.String() != "https://site.example/p/"+strings.Repeat("a", 32)+"\n" {
				t.Fatalf("missing playback link: %s", output.String())
			}
			if strings.Contains(diagnostic.String(), "Saved") || (err != nil && strings.Contains(err.Error(), "local recording saved")) {
				t.Fatal("temporary recording was reported as saved locally")
			}
			requireEmptyDirectory(t, working)
			requireEmptyDirectory(t, temporary)
		})
	}
}

func TestRecordNoSaveValidatesBeforeStarting(t *testing.T) {
	for _, scenario := range []struct {
		name, token, endpoint, errorText string
		flags                            []string
	}{
		{name: "requires upload", flags: []string{"--no-save"}, errorText: "--no-save requires --upload"},
		{name: "upload explicitly disabled", flags: []string{"--no-save", "--upload=false"}, errorText: "--no-save requires --upload"},
		{name: "conflicting output", flags: []string{"--no-save", "--upload", "--storage", "gist", "-o", "saved.json"}, errorText: "--no-save cannot be used with --output"},
		{name: "missing authentication", flags: []string{"--no-save", "--upload"}, errorText: "run termbacktime auth --storage repo"},
		{name: "invalid endpoint", flags: []string{"--no-save", "--upload"}, token: "test-token", endpoint: "invalid", errorText: "set SITE_URL"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			working := t.TempDir()
			temporary := t.TempDir()
			configPath := filepath.Join(t.TempDir(), "config.json")
			t.Chdir(working)
			t.Setenv("TMPDIR", temporary)
			t.Setenv("TERMBACKTIME_TOKEN", scenario.token)
			root := NewRoot()
			args := []string{"--config", configPath, "--endpoint", scenario.endpoint, "record"}
			args = append(args, scenario.flags...)
			root.SetArgs(append(args, "--", "/bin/sh", "-c", "touch command-started"))
			if err := root.Execute(); err == nil || !strings.Contains(err.Error(), scenario.errorText) {
				t.Fatalf("expected %q, got %v", scenario.errorText, err)
			}
			requireEmptyDirectory(t, working)
			requireEmptyDirectory(t, temporary)
		})
	}
}

func TestRecordUploadKeepsLocalFileByDefault(t *testing.T) {
	t.Setenv("TERMBACKTIME_DATA_DIR", t.TempDir())
	t.Setenv("TERMBACKTIME_TOKEN", "test-token")
	previousTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	http.DefaultTransport = recordTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodPatch {
			return jsonResponse(200, `{}`), nil
		}
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})
	dir := t.TempDir()
	path := filepath.Join(dir, "retained.json")
	root := NewRoot()
	var diagnostic bytes.Buffer
	root.SetErr(&diagnostic)
	root.SetArgs([]string{"--config", filepath.Join(dir, "config.json"), "--endpoint", "", "record", "--upload", "--storage", "gist", "--no-save=false", "--output", path, "--", "/bin/sh", "-c", "printf 'retained fixture\\n'"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := root.ExecuteContext(ctx)
	if err == nil || !strings.Contains(err.Error(), "local recording saved; upload failed") {
		t.Fatalf("unexpected upload failure: %v", err)
	}
	saved, err := recording.Load(path)
	if err != nil {
		t.Fatal("upload failure lost the local recording", err)
	}
	if saved.Info.CLI != buildinfo.Tag() || saved.Info.UploadCLI != "" {
		t.Fatalf("incorrect saved capture version: %+v", saved.Info)
	}
	if !strings.Contains(diagnostic.String(), "Saved "+path) {
		t.Fatal("local recording was not reported")
	}
}
