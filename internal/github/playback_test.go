package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddPlaybackLinkUsesConfiguredOriginAndOnlyUpdatesReadme(t *testing.T) {
	for _, origin := range []string{"https://termbackti.me", "https://next.termbackti.me/", "https://custom.example:8443", "http://localhost:8787/"} {
		for _, encrypted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/encrypted=%t", origin, encrypted), func(t *testing.T) {
				c := New("test-token")
				c.SiteURL = origin
				id, calls := strings.Repeat("a", 32), 0
				c.HTTP.Transport = uploadTransport(func(req *http.Request) (*http.Response, error) {
					calls++
					if req.Method != http.MethodPatch || req.URL.String() != "https://api.github.com/gists/"+id || req.Header.Get("Authorization") != "Bearer test-token" {
						t.Fatal("incorrect update request", req)
					}
					var body map[string]json.RawMessage
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if len(body) != 1 {
						t.Fatal("update changed description or visibility", body)
					}
					var files map[string]struct{ Content string }
					if err := json.Unmarshal(body["files"], &files); err != nil {
						t.Fatal(err)
					}
					if len(files) != 1 {
						t.Fatal("update changed recording or metadata", files)
					}
					want := "[Play recording](" + strings.TrimRight(origin, "/") + "/p/" + id + ")"
					text := files[PlaybackFilename].Content
					if !strings.Contains(text, want) || strings.Contains(text, "#k=") || strings.Contains(text, "private sharing link") != encrypted {
						t.Fatal("incorrect playback README", text)
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(uploadReadFunc(func([]byte) (int, error) { t.Fatal("buffered an echoed recording"); return 0, io.EOF }))}, nil
				})
				if err := c.AddPlaybackLink(t.Context(), id, encrypted, ""); err != nil || calls != 1 {
					t.Fatal(calls, err)
				}
			})
		}
	}
}

func TestPlaybackLinkSkipsUnconfiguredOriginAndRejectsInvalidDestinations(t *testing.T) {
	c := New("test-token")
	c.HTTP.Transport = uploadTransport(func(req *http.Request) (*http.Response, error) { t.Fatal("unexpected request", req); return nil, nil })
	if err := c.AddPlaybackLink(t.Context(), strings.Repeat("a", 32), false, ""); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"https://site.example/#k=private", "https://site.example/?key=private", "https://user:password@site.example", "javascript:alert(1)"} {
		c.SiteURL = origin
		if err := c.AddPlaybackLink(t.Context(), strings.Repeat("a", 32), false, ""); err == nil {
			t.Fatal("invalid origin accepted")
		}
	}
	c.SiteURL = "https://site.example"
	if err := c.AddPlaybackLink(t.Context(), "../../other", false, ""); err == nil {
		t.Fatal("invalid Gist ID accepted")
	}
}

func TestPlaybackLinkFailureDoesNotRetry(t *testing.T) {
	c := New("token")
	c.SiteURL = "https://site.example"
	calls := 0
	c.HTTP.Transport = uploadTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	var httpErr *HTTPError
	if err := c.AddPlaybackLink(t.Context(), strings.Repeat("b", 32), false, ""); !errors.As(err, &httpErr) || calls != 1 {
		t.Fatal("update failure lost or retried", calls, err)
	}
}

func TestPreparedPlaybackReadmePreservesMetadataAndCombinesLegacyCompanion(t *testing.T) {
	for _, filename := range []string{PlaybackFilename, "metadata.md"} {
		t.Run(filename, func(t *testing.T) {
			dir := t.TempDir()
			metadata := "# Approved title\n\nDescription with \\[escaped\\] prose and 世界.\n\n## Recording\n\n- Duration: 12 seconds\n"
			if err := os.WriteFile(filepath.Join(dir, filename), []byte(metadata), 0600); err != nil {
				t.Fatal(err)
			}
			c := New("token")
			c.SiteURL = "https://site.example"
			calls := 0
			c.HTTP.Transport = uploadTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				var patch struct {
					Files map[string]*struct{ Content string }
				}
				if err := json.NewDecoder(req.Body).Decode(&patch); err != nil {
					t.Fatal(err)
				}
				readme := patch.Files[PlaybackFilename].Content
				_, details, _ := strings.Cut(metadata, "\n\n")
				if !strings.HasPrefix(readme, "# Approved title\n\n[Play recording]") || !strings.HasSuffix(readme, details) || !strings.Contains(readme, "complete private sharing link") || strings.Contains(readme, "#k=") {
					t.Fatal("lost approved metadata or keyless playback link", readme)
				}
				deleted, exists := patch.Files["metadata.md"]
				wantFiles := 1
				if filename == "metadata.md" {
					wantFiles++
				}
				if exists != (filename == "metadata.md") || deleted != nil || len(patch.Files) != wantFiles {
					t.Fatal("legacy companion must be removed only when present", patch.Files)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			})
			if err := c.AddPreparedPlaybackLink(t.Context(), strings.Repeat("a", 32), true, true, dir); err != nil || calls != 1 {
				t.Fatal(err, calls)
			}
		})
	}
}

func TestPreparedPlaybackReadmeRefusesMissingOrOversizedMetadata(t *testing.T) {
	c := New("token")
	c.SiteURL = "https://site.example"
	c.HTTP.Transport = uploadTransport(func(req *http.Request) (*http.Response, error) {
		t.Fatal("must not overwrite the README after losing approved metadata")
		return nil, nil
	})
	dir := t.TempDir()
	if err := c.AddPreparedPlaybackLink(t.Context(), strings.Repeat("a", 32), false, true, dir); err == nil {
		t.Fatal("missing metadata accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, PlaybackFilename), []byte(strings.Repeat("x", (4<<20)+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.AddPreparedPlaybackLink(t.Context(), strings.Repeat("a", 32), false, true, dir); err == nil {
		t.Fatal("oversized metadata accepted")
	}
}
