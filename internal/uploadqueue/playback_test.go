package uploadqueue

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
)

func TestQueueRetriesPlaybackCompanionUsingSavedEndpointWithoutRecreatingGist(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		t.Run(fmt.Sprintf("encrypted=%t", encrypted), func(t *testing.T) {
			store := Store{Root: t.TempDir()}
			source := filepath.Join(t.TempDir(), "source.json")
			if err := recording.Save(source, &recording.Recording{Sizes: []int{80, 24}, Lines: []recording.Event{{Lines: []string{"fixture"}}}}); err != nil {
				t.Fatal(err)
			}
			client := gh.New("token")
			client.SiteURL = "https://capture.example/"
			id := strings.Repeat("d", 32)
			var job Job
			creates, patches := 0, 0
			var readme, receipt string
			metadata := "# Queued title\n\nApproved queued description\n\n## Recording\n\n- Duration: 1 second\n"
			var firstReadme string
			client.HTTP.Transport = transport(func(req *http.Request) (*http.Response, error) {
				switch {
				case req.URL.Path == "/user":
					return response(map[string]any{"id": 42}), nil
				case req.URL.Path == "/api/v1/config":
					return response(map[string]any{"recordingFormats": []string{"tbt-recording-v1", "tbt-encrypted-v1"}}), nil
				case req.Method == http.MethodPost:
					creates++
					var creation struct {
						Files map[string]struct{ Content string }
					}
					if err := json.NewDecoder(req.Body).Decode(&creation); err != nil {
						t.Fatal(err)
					}
					if _, exists := creation.Files["metadata.md"]; exists || creation.Files[gh.PlaybackFilename].Content != metadata {
						t.Fatal("queued creation must contain metadata in README only")
					}
					return response(map[string]any{"id": id}), nil
				case req.Method == http.MethodPatch:
					patches++
					if req.URL.String() != "https://api.github.com/gists/"+id {
						t.Fatal("wrong Gist", req.URL)
					}
					var err error
					receipt, err = (library.Library{Root: store.Root}).ShareLink(source)
					if err != nil {
						t.Fatal("receipt missing before README update", err)
					}
					var patch struct {
						Files map[string]struct{ Content string }
					}
					if err := json.NewDecoder(req.Body).Decode(&patch); err != nil {
						t.Fatal(err)
					}
					readme = patch.Files[gh.PlaybackFilename].Content
					if !strings.HasPrefix(readme, "# Queued title\n\n[Play recording]") || !strings.Contains(readme, "Approved queued description") || !strings.Contains(readme, "## Recording") {
						t.Fatal("queued README lost approved metadata", readme)
					}
					if patches == 1 {
						firstReadme = readme
					} else if readme != firstReadme {
						t.Fatal("README retry changed or duplicated metadata", readme)
					}
					if !strings.Contains(readme, "https://capture.example/p/"+id) || strings.Contains(readme, "https://later.example") || strings.Contains(readme, "#k=") {
						t.Fatal("queued origin lost or key published", readme)
					}
					if encrypted {
						key, err := os.ReadFile(filepath.Join(store.path(job.ID), "key.txt"))
						if err != nil || len(key) == 0 || !strings.Contains(receipt, string(key)) || strings.Contains(readme, string(key)) {
							t.Fatal("encryption receipt lost or leaked", err)
						}
					}
					if patches == 1 {
						res := response(map[string]any{})
						res.StatusCode = 503
						return res, nil
					}
					return response(map[string]any{}), nil
				case req.URL.Path == "/gists":
					return response([]any{map[string]any{"id": id, "owner": map[string]any{"id": 42}, "files": map[string]any{gh.Filename: map[string]any{"size": 100}, gh.JobFilename(job.ID): map[string]any{"size": 100}}}}), nil
				case req.URL.Path == "/gists/"+id:
					companion, _ := json.Marshal(map[string]string{"format": "tbt-upload-job-v1", "id": job.ID})
					return response(map[string]any{"owner": map[string]any{"id": 42}, "files": map[string]any{gh.JobFilename(job.ID): map[string]any{"content": string(companion)}}}), nil
				default:
					t.Fatal("unexpected request", req)
					return nil, nil
				}
			})
			var err error
			job, err = store.Enqueue(t.Context(), client, source, source, encrypted, gh.UploadOptions{Metadata: metadata})
			if err != nil {
				t.Fatal(err)
			}
			// Running later must use the origin chosen when the upload was queued.
			client.SiteURL = "https://later.example"
			if err := store.Run(t.Context(), client, func(Job) {}); err != nil {
				t.Fatal(err)
			}
			saved, err := store.load(job.ID)
			if err != nil || creates != 1 || patches != 1 || saved.State != "needs attention" || saved.Result != "https://capture.example/p/"+id || !strings.Contains(saved.Error, "playback link failed") {
				t.Fatal("lost successful upload", saved, creates, patches, err)
			}
			if err := store.Retry(job.ID); err == nil {
				t.Fatal("README failure permitted duplicate creation")
			}
			if err := store.Run(t.Context(), client, func(Job) {}); err != nil {
				t.Fatal(err)
			}
			saved, err = store.load(job.ID)
			if err != nil || creates != 1 || patches != 2 || saved.State != "complete" || saved.Error != "" {
				t.Fatal("README retry recreated recording or failed", saved, creates, patches, err)
			}
			if err := store.Run(t.Context(), client, func(Job) {}); err != nil || creates != 1 || patches != 2 {
				t.Fatal("completed job repeated", err)
			}
		})
	}
}
