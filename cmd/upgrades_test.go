package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/termbacktime/termbacktime/internal/buildinfo"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/sealed"
)

func TestPassiveCommandsDoNotMigrate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TERMBACKTIME_DATA_DIR", "")
	legacy := filepath.Join(home, ".termbacktime.json")
	os.WriteFile(legacy, []byte(`{"token":"secret"}`), 0600)
	for _, args := range [][]string{{"--help"}, {"--version"}, {"completion", "bash"}, {"record", "--help"}} {
		root := NewRoot()
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(home, "termbacktime")); !os.IsNotExist(err) {
			t.Fatal("passive command created data directory")
		}
		if _, err := os.Stat(legacy); err != nil {
			t.Fatal("passive command migrated config")
		}
	}
}
func TestEncryptedUploadPolicyTitleAndReceipts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("TERMBACKTIME_DATA_DIR", filepath.Join(dir, "library-root"))
	t.Setenv("TERMBACKTIME_TOKEN", "fake-token")
	path := filepath.Join(dir, "original.json")
	r := &recording.Recording{ID: recording.NewID(), Sizes: []int{80, 24}, Title: "original", Info: recording.Info{CLI: "v0.9.0-capture"}, Lines: []recording.Event{{Lines: []string{"token=syntheticsecret"}}}}
	if err := recording.Save(path, r); err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	uploads := 0
	var payload map[string]json.RawMessage
	var files map[string]struct {
		Content string `json:"content"`
	}
	http.DefaultTransport = recordTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPatch {
			return jsonResponse(200, `{}`), nil
		}
		body := `{"recordingFormats":["tbt-recording-v1","tbt-encrypted-v1"]}`
		status := 200
		if req.URL.Host == "api.github.com" {
			uploads++
			status = 201
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(payload["files"], &files); err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(payload)
			if strings.Contains(string(b), "syntheticsecret") || strings.Contains(string(b), "Override private title") || strings.Contains(string(b), r.Info.CLI) || strings.Contains(string(b), "upload_cli") {
				t.Fatal("plaintext leaked to Gist")
			}
			body = `{"id":"` + strings.Repeat("e", 32) + `"}`
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	execute := func(args ...string) (string, string, error) {
		root := NewRoot()
		var out, diag bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&diag)
		root.SetArgs(append([]string{"--endpoint", "https://site.example"}, args...))
		err := root.ExecuteContext(t.Context())
		return out.String(), diag.String(), err
	}
	if _, _, err := execute("upload", "--storage", "gist", path, "--fail-on-secrets"); err == nil || uploads != 0 {
		t.Fatal("scan policy did not stop upload")
	}
	output, diagnostic, err := execute("upload", "--storage", "gist", path, "--encrypt", "--title", "Override private title")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diagnostic, "masked finding") || strings.Contains(diagnostic, "syntheticsecret") {
		t.Fatal("unsafe diagnostic")
	}
	link := strings.TrimSpace(output)
	u, _ := url.Parse(link)
	fragment, _ := url.ParseQuery(u.Fragment)
	var manifest sealed.Manifest
	if err := json.Unmarshal([]byte(files[sealed.Filename].Content), &manifest); err != nil {
		t.Fatal(err)
	}
	plain, err := sealed.Decrypt(t.Context(), manifest, fragment.Get("k"), func(n string) ([]byte, error) { return []byte(files[n].Content), nil })
	if err != nil {
		t.Fatal(err)
	}
	got, err := recording.Decode(bytes.NewReader(plain))
	if err != nil || got.Title != "Override private title" {
		t.Fatal("title override failed", err)
	}
	if got.Info.CLI != r.Info.CLI || got.Info.UploadCLI != buildinfo.Tag() {
		t.Fatalf("upload changed capture version or omitted uploader version: %+v", got.Info)
	}
	original, _ := recording.Load(path)
	if original.Title != "original" || original.Info != r.Info {
		t.Fatal("source overwritten")
	}
	entries, err := (library.Library{Root: filepath.Join(dir, "library-root")}).List()
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	ordinary, _, err := execute("list", "--json")
	if err != nil || strings.Contains(ordinary, fragment.Get("k")) {
		t.Fatal("listing leaked key")
	}
	saved, _, err := execute("info", entries[0].ID, "--show-share-link")
	if err != nil || strings.TrimSpace(saved) != link {
		t.Fatal("missing receipt", err)
	}
}
func TestEncryptedNoSaveCleansAllArtifacts(t *testing.T) {
	dir, temporary := t.TempDir(), t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("TMPDIR", temporary)
	t.Setenv("TERMBACKTIME_DATA_DIR", filepath.Join(dir, "data"))
	t.Setenv("TERMBACKTIME_TOKEN", "fake-token")
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = recordTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPatch {
			return jsonResponse(200, `{}`), nil
		}
		body := `{"recordingFormats":["tbt-recording-v1","tbt-encrypted-v1"]}`
		if req.URL.Host == "api.github.com" {
			io.Copy(io.Discard, req.Body)
			body = `{"id":"` + strings.Repeat("e", 32) + `"}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	root := NewRoot()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"--endpoint", "https://site.example", "record", "--upload", "--storage", "gist", "--encrypt", "--no-save", "--", "/bin/sh", "-c", "printf synthetic"})
	if err := root.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireEmptyDirectory(t, temporary)
	if _, err := os.Stat(filepath.Join(dir, "data")); !os.IsNotExist(err) {
		t.Fatal("no-save created library")
	}
}
