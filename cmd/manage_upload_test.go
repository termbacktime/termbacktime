package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/manage"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/review"
	"github.com/termbacktime/termbacktime/internal/sealed"
	"github.com/termbacktime/termbacktime/internal/upload"
	"github.com/termbacktime/termbacktime/internal/uploadqueue"
)

func uploadBackend(t *testing.T) (*manageBackend, manage.Item) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "source.json")
	r := &recording.Recording{ID: recording.NewID(), Title: "Original title", Sizes: []int{80, 24}, Metadata: &recording.Metadata{Version: 1, CaptureSystem: &recording.SystemInfo{OS: "linux", CPUModel: "privatecpu", RAMBytes: 1 << 30}}, Lines: []recording.Event{{Time: 100, Lines: []string{"hello"}}}, Callouts: []recording.Callout{{ID: recording.NewID(), At: 0, Text: "annotation", Duration: 100, Pause: true}}}
	if err := recording.Save(path, r); err != nil {
		t.Fatal(err)
	}
	lib := library.Library{Root: filepath.Join(dir, "data")}
	entry, err := lib.Register(path)
	if err != nil {
		t.Fatal(err)
	}
	client := gh.New("test-token")
	client.SiteURL = "https://site.example"
	return &manageBackend{Store: &manage.Store{Library: lib, GitHub: client}, options: &options{configPath: filepath.Join(dir, "config.json")}}, manage.Item{Local: entry}
}
func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}
func TestManagedUploadOptionsMetadataReceiptsAndQueue(t *testing.T) {
	for _, public := range []bool{false, true} {
		for _, encrypted := range []bool{false, true} {
			for _, queue := range []bool{false, true} {
				t.Run(fmt.Sprintf("public=%t/encrypted=%t/queue=%t", public, encrypted, queue), func(t *testing.T) {
					b, item := uploadBackend(t)
					before, _ := os.ReadFile(item.Path())
					draft, err := b.PrepareUpload(t.Context(), item)
					if err != nil {
						t.Fatal(err)
					}
					var payload struct {
						Public bool
						Files  map[string]struct{ Content string }
					}
					posts, patches := 0, 0
					b.GitHub.HTTP.Transport = recordTransport(func(req *http.Request) (*http.Response, error) {
						if req.Method == http.MethodPatch {
							patches++
							var patch struct {
								Files map[string]struct{ Content string }
							}
							if err := json.NewDecoder(req.Body).Decode(&patch); err != nil {
								t.Fatal(err)
							}
							if len(patch.Files) != 1 || !strings.Contains(patch.Files[gh.PlaybackFilename].Content, "https://site.example/p/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") || strings.Contains(patch.Files[gh.PlaybackFilename].Content, "#k=") {
								t.Fatal("incorrect playback companion", patch)
							}
							if !strings.Contains(patch.Files[gh.PlaybackFilename].Content, "Reviewed description") || strings.Contains(patch.Files[gh.PlaybackFilename].Content, "privatecpu") {
								t.Fatal("combined README lost approved metadata", patch)
							}
							return jsonResponse(200, `{}`), nil
						}
						switch req.URL.Path {
						case "/user":
							return jsonResponse(200, `{"id":42}`), nil
						case "/api/v1/config":
							return jsonResponse(200, `{"recordingFormats":["tbt-recording-v1","tbt-encrypted-v1"]}`), nil
						case "/gists":
							posts++
							if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
								return nil, err
							}
							return jsonResponse(201, `{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), nil
						}
						return nil, fmt.Errorf("unexpected request: %s", req.URL)
					})
					approved := review.Result{Title: "Reviewed title", Description: "Reviewed description", Fields: recording.MetadataFields{OS: true}, Public: public, Encrypted: encrypted, Queue: queue, RememberTemplate: true}
					var progress []upload.Progress
					result, err := b.Upload(t.Context(), item, draft, approved, func(p upload.Progress) { progress = append(progress, p) })
					if err != nil {
						t.Fatal(err)
					}
					if result.Warning != "" {
						t.Fatal(result.Warning)
					}
					if queue {
						jobs, err := (uploadqueue.Store{Root: b.Library.Root}).List()
						if err != nil || len(jobs) != 1 || jobs[0].ID != result.QueueID || jobs[0].Public != public || jobs[0].Encrypted != encrypted || posts != 0 || patches != 0 {
							t.Fatal("incorrect queue", jobs, err, posts)
						}
					} else {
						if posts != 1 || patches != 1 || payload.Public != public {
							t.Fatal("wrong visibility/request count")
						}
						if !strings.Contains(payload.Files["README.md"].Content, "Reviewed description") || strings.Contains(payload.Files["README.md"].Content, "privatecpu") {
							t.Fatal("metadata approval lost")
						}
						data := []byte(payload.Files[gh.Filename].Content)
						if encrypted {
							var manifest sealed.Manifest
							if err := json.Unmarshal([]byte(payload.Files[sealed.Filename].Content), &manifest); err != nil {
								t.Fatal(err)
							}
							link, _ := url.Parse(result.Link)
							fragment, _ := url.ParseQuery(link.Fragment)
							data, err = sealed.Decrypt(t.Context(), manifest, fragment.Get("k"), func(name string) ([]byte, error) { return []byte(payload.Files[name].Content), nil })
							if err != nil {
								t.Fatal(err)
							}
						}
						decode := recording.ReadWrapped
						if encrypted {
							decode = recording.Decode
						}
						uploaded, err := decode(bytes.NewReader(data))
						if err != nil {
							t.Fatal(err)
						}
						if uploaded.Title != "Reviewed title" || uploaded.Metadata.CaptureSystem.CPUModel != "" || uploaded.Metadata.CaptureSystem.OS != "linux" || len(uploaded.Callouts) != 1 {
							t.Fatal("review or compatibility lost", uploaded)
						}
						link, err := b.Library.ShareLink(item.Path())
						if err != nil || link != result.Link {
							t.Fatal("receipt missing", link, err)
						}
						found := false
						for _, p := range progress {
							found = found || p.Stage == "Uploading" && p.Sent > 0 && p.Total > 0
						}
						if !found {
							t.Fatal("no byte progress", progress)
						}
					}
					next, err := b.PrepareUpload(t.Context(), item)
					if err != nil || next.Fields != approved.Fields {
						t.Fatal("preferences not reused", err, next.Fields)
					}
					after, _ := os.ReadFile(item.Path())
					if !bytes.Equal(before, after) {
						t.Fatal("original changed")
					}
				})
			}
		}
	}
}
func TestManagedUploadFailuresAndCancellationNeverRetry(t *testing.T) {
	for _, mode := range []string{"no-auth", "rejected", "network", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			b, item := uploadBackend(t)
			draft, err := b.PrepareUpload(t.Context(), item)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "no-auth" {
				b.GitHub.Token = ""
			}
			calls := 0
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			b.GitHub.HTTP.Transport = recordTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				io.Copy(io.Discard, req.Body)
				if mode == "cancel" {
					cancel()
					return nil, ctx.Err()
				}
				if mode == "network" {
					return nil, fmt.Errorf("connection lost after send")
				}
				return jsonResponse(401, `{"message":"Bad credentials"}`), nil
			})
			result, err := b.Upload(ctx, item, draft, review.Result{Title: "approved"}, nil)
			if err == nil || result.Link != "" || calls > 1 {
				t.Fatal("failed request repeated or hidden", err, result, calls)
			}
			if mode == "no-auth" && calls != 0 {
				t.Fatal("unauthenticated request sent")
			}
			if link, _ := b.Library.ShareLink(item.Path()); link != "" {
				t.Fatal("failed upload receipt")
			}
		})
	}
}
