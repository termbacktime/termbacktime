package uploadqueue

import (
	"encoding/json"
	"errors"
	"fmt"
	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryQueuePersistsThenReconcilesWithoutOriginal(t *testing.T) {
	store := Store{Root: t.TempDir()}
	source := filepath.Join(t.TempDir(), "source.tbt")
	if err := recording.Save(source, &recording.Recording{ID: recording.NewID(), Started: 100, Sizes: []int{80, 24}, Lines: []recording.Event{{Lines: []string{"safe"}}}}); err != nil {
		t.Fatal(err)
	}
	client := gh.New("app-token")
	client.Storage = "repo"
	client.AppID = 3
	client.SiteURL = "https://play.example"
	var job Job
	var prepared gh.RepoRequest
	published, writes := false, 0
	repo := map[string]any{"id": 21, "name": "TBT-Recordings", "default_branch": "main", "owner": map[string]any{"id": 7, "login": "alice"}}
	sha := strings.Repeat("a", 40)
	client.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
		p := r.URL.Path
		if r.URL.Host == "raw.githubusercontent.com" {
			name := filepath.Base(p)
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(prepared.Files[name])))}, nil
		}
		switch p {
		case "/user":
			return response(map[string]any{"id": 7, "login": "alice"}), nil
		case "/user/installations":
			return response(map[string]any{"total_count": 1, "installations": []any{map[string]any{"id": 2, "app_id": 3, "account": map[string]any{"id": 7}, "repository_selection": "selected", "permissions": map[string]string{"contents": "write", "metadata": "read"}}}}), nil
		case "/user/installations/2/repositories":
			return response(map[string]any{"total_count": 1, "repositories": []any{repo}}), nil
		case "/repos/alice/TBT-Recordings":
			return response(repo), nil
		case "/api/v1/config":
			return response(map[string]any{"recordingSources": []string{"repo"}}), nil
		}
		if strings.Contains(p, "/git/ref/heads/") {
			return response(map[string]any{"object": map[string]string{"sha": sha}}), nil
		}
		if r.Method == "GET" && strings.Contains(p, "/git/commits/") {
			return response(map[string]any{"tree": map[string]string{"sha": sha}}), nil
		}
		if r.Method == "GET" && strings.Contains(p, "/git/trees/") {
			entries := []any{}
			if published {
				for name := range prepared.Files {
					entries = append(entries, map[string]any{"path": "recordings/" + job.ID + "/" + name, "mode": "100644", "type": "blob", "sha": sha})
				}
			}
			return response(map[string]any{"tree": entries}), nil
		}
		if r.Method == "POST" || r.Method == "PATCH" {
			writes++
			current, err := store.load(job.ID)
			if err != nil || current.State != "sending" {
				t.Fatal("write before durable sending state", current, err)
			}
			if prepared, err = gh.ReadRepoRequest(filepath.Join(store.path(job.ID), "request.json")); err != nil {
				t.Fatal(err)
			}
			if r.Method == "PATCH" {
				published = true
				return nil, errors.New("lost commit response")
			}
			return response(map[string]string{"sha": sha}), nil
		}
		return nil, fmt.Errorf("unexpected request %s", r.URL)
	})
	var err error
	job, err = store.Enqueue(t.Context(), client, source, source, false, gh.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if job.Version != 3 || job.Storage != "repo" || !job.Public {
		t.Fatal(job)
	}
	if err = os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err = store.Run(t.Context(), client, func(Job) {}); err != nil {
		t.Fatal(err)
	}
	jobs, _ := store.List()
	if jobs[0].State != "needs attention" {
		t.Fatal(jobs)
	}
	count := writes
	if err = store.Run(t.Context(), client, func(Job) {}); err != nil {
		t.Fatal(err)
	}
	link, err := store.PublishedLink(job.ID)
	if err != nil || link != "https://play.example/p/alice/"+job.ID || writes != count {
		t.Fatal(link, err, writes, count)
	}
	receipts, _, err := (library.Library{Root: store.Root}).Receipts(t.Context(), "")
	if err != nil || len(receipts) != 1 {
		t.Fatal(receipts, err)
	}
	b, _ := json.Marshal(jobs)
	if strings.Contains(string(b), "app-token") {
		t.Fatal("leaked credential")
	}
}
