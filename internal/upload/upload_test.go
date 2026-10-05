package upload

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
)

type uploadTransport func(*http.Request) (*http.Response, error)

func (f uploadTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
func uploadResponse(status int, content string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(content))}
}

func TestPlaybackCompanionPreservesSuccessfulUploadAndPrivateReceipt(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		for _, retain := range []bool{false, true} {
			for _, patchFails := range []bool{false, true} {
				t.Run(fmt.Sprintf("encrypted=%t/retain=%t/patchFails=%t", encrypted, retain, patchFails), func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "recording.json")
					if err := recording.Save(path, &recording.Recording{Title: "Private title", Sizes: []int{80, 24}, Lines: []recording.Event{{Lines: []string{"private output"}}}}); err != nil {
						t.Fatal(err)
					}
					lib := library.Library{Root: t.TempDir()}
					c := gh.New("test-token")
					c.SiteURL = "https://selfhost.example"
					id := strings.Repeat("b", 32)
					creates, patches := 0, 0
					var readme string
					c.HTTP.Transport = uploadTransport(func(req *http.Request) (*http.Response, error) {
						switch {
						case req.URL.Path == "/api/v1/config":
							return uploadResponse(200, `{"recordingFormats":["tbt-recording-v1","tbt-encrypted-v1"]}`), nil
						case req.Method == http.MethodPost:
							creates++
							io.Copy(io.Discard, req.Body)
							return uploadResponse(201, `{"id":"`+id+`"}`), nil
						case req.Method == http.MethodPatch:
							patches++
							if retain {
								link, err := lib.ShareLink(path)
								if err != nil || !strings.HasPrefix(link, c.SiteURL+"/p/"+id) || strings.Contains(link, "#k=") != encrypted {
									t.Fatal("receipt not safe before PATCH", link, err)
								}
							}
							var patch struct {
								Files map[string]struct{ Content string }
							}
							if err := json.NewDecoder(req.Body).Decode(&patch); err != nil {
								t.Fatal(err)
							}
							readme = patch.Files[gh.PlaybackFilename].Content
							if patchFails {
								return uploadResponse(503, `{}`), nil
							}
							return uploadResponse(200, `{}`), nil
						default:
							t.Fatal("unexpected request", req)
							return nil, nil
						}
					})
					result, err := Run(t.Context(), c, lib, Request{Path: path, Encrypted: encrypted, Retain: retain}, nil)
					if err != nil || creates != 1 || patches != 1 || !strings.HasPrefix(result.Link, c.SiteURL+"/p/"+id) {
						t.Fatal("successful creation lost", result, creates, patches, err)
					}
					if strings.Contains(result.Warning, "could not add the playback link") != patchFails {
						t.Fatal("incorrect warning", result.Warning)
					}
					if !strings.Contains(readme, c.SiteURL+"/p/"+id) || strings.Contains(readme, "private output") || strings.Contains(readme, "Private title") || strings.Contains(readme, "#k=") {
						t.Fatal("incorrect companion", readme)
					}
					u, _ := url.Parse(result.Link)
					fragment, _ := url.ParseQuery(u.Fragment)
					if encrypted && (fragment.Get("k") == "" || strings.Contains(readme, fragment.Get("k")) || strings.Contains(result.Warning, fragment.Get("k"))) {
						t.Fatal("lost or published encryption key")
					}
					saved, err := lib.ShareLink(path)
					if retain && (err != nil || saved != result.Link) {
						t.Fatal("receipt lost", err)
					}
					if !retain && saved != "" {
						t.Fatal("no-save retained a receipt")
					}
				})
			}
		}
	}
}
