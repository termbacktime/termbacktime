package uploadqueue

import (
	"context"
	"encoding/json"
	"errors"
	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/recording"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(value any) *http.Response {
	b, _ := json.Marshal(value)
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b)))}
}
func TestInterruptedCreationReconcilesWithoutRepeating(t *testing.T) {
	store := Store{Root: t.TempDir()}
	source := filepath.Join(t.TempDir(), "source.json")
	recording.Save(source, &recording.Recording{Sizes: []int{80, 24}, Lines: []recording.Event{{Lines: []string{"safe output"}}}})
	client := gh.New("token")
	client.SiteURL = "https://example.com"
	var job Job
	creates := 0
	gistID := strings.Repeat("b", 32)
	client.HTTP.Transport = transport(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPatch {
			return response(map[string]any{}), nil
		}
		switch {
		case req.URL.Path == "/user":
			return response(map[string]any{"id": 42}), nil
		case req.Method == "POST":
			creates++
			if _, err := os.Stat(filepath.Join(store.path(job.ID), "key.txt")); err != nil {
				t.Fatal("key receipt missing before send")
			}
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			return nil, errors.New("connection closed after creation")
		case req.URL.Path == "/gists":
			return response([]any{map[string]any{"id": gistID, "owner": map[string]any{"id": 42}, "files": map[string]any{gh.Filename: map[string]any{"size": 100}, gh.JobFilename(job.ID): map[string]any{"size": 100}}}}), nil
		default:
			companion, _ := json.Marshal(map[string]string{"format": "tbt-upload-job-v1", "id": job.ID})
			return response(map[string]any{"owner": map[string]any{"id": 42}, "files": map[string]any{gh.JobFilename(job.ID): map[string]any{"content": string(companion)}}}), nil
		}
	})
	var err error
	job, err = store.Enqueue(context.Background(), client, source, source, false, gh.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Run(context.Background(), client, func(Job) {}); err != nil {
		t.Fatal(err)
	}
	jobs, _ := store.List()
	if jobs[0].State != "needs attention" || creates != 1 {
		t.Fatal(jobs, creates)
	}
	if err = store.Retry(job.ID); err == nil {
		t.Fatal("uncertain POST blindly retried")
	}
	if err = store.Run(context.Background(), client, func(Job) {}); err != nil {
		t.Fatal(err)
	}
	jobs, _ = store.List()
	if jobs[0].State != "complete" || creates != 1 {
		t.Fatal(jobs, creates)
	}
}
func TestQueueRunnerLockAndUnsentCancellation(t *testing.T) {
	store := Store{Root: t.TempDir()}
	lock, err := store.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock(lock)
	if another, err := store.lock(); err == nil {
		unlock(another)
		t.Fatal("duplicate runner allowed")
	}
}

func TestQueueRetainsKeyAndOwnerAndDoesNotNeedOriginal(t *testing.T) {
	store := Store{Root: t.TempDir()}
	source := filepath.Join(t.TempDir(), "source.json")
	if err := recording.Save(source, &recording.Recording{Sizes: []int{80, 24}, Lines: []recording.Event{{Lines: []string{"private content"}}}}); err != nil {
		t.Fatal(err)
	}
	client := gh.New("token")
	client.SiteURL = "https://example.com"
	owner, creates := int64(42), 0
	var job Job
	var retainedKey string
	client.HTTP.Transport = transport(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPatch {
			return response(map[string]any{}), nil
		}
		switch req.URL.Path {
		case "/user":
			return response(map[string]any{"id": owner}), nil
		case "/api/v1/config":
			return response(map[string]any{"recordingFormats": []string{"tbt-recording-v1", "tbt-encrypted-v1"}}), nil
		case "/gists":
			if req.Method != "POST" {
				t.Fatal("unsent job incorrectly reconciled")
			}
			creates++
			key, err := os.ReadFile(filepath.Join(store.path(job.ID), "key.txt"))
			if err != nil || len(key) == 0 {
				t.Fatal("missing durable key", err)
			}
			retainedKey = string(key)
			b, _ := io.ReadAll(req.Body)
			if strings.Contains(string(b), "private content") {
				t.Fatal("plaintext in encrypted request")
			}
			return response(map[string]any{"id": strings.Repeat("c", 32)}), nil
		}
		t.Fatalf("unexpected request %s", req.URL)
		return nil, nil
	})
	var err error
	job, err = store.Enqueue(context.Background(), client, source, source, true, gh.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	owner = 43
	if err = store.Run(context.Background(), client, func(Job) {}); err != nil {
		t.Fatal(err)
	}
	jobs, _ := store.List()
	if jobs[0].State != "pending" || creates != 0 {
		t.Fatal(jobs, creates)
	}
	owner = 42
	if err = os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err = store.Run(context.Background(), client, func(Job) {}); err != nil {
		t.Fatal(err)
	}
	jobs, _ = store.List()
	if jobs[0].State != "complete" || creates != 1 {
		t.Fatal(jobs, creates)
	}
	receipts, _ := filepath.Glob(filepath.Join(store.Root, "shares", "*.json"))
	if len(receipts) != 1 {
		t.Fatal(receipts)
	}
	receipt, _ := os.ReadFile(receipts[0])
	if !strings.Contains(string(receipt), retainedKey) {
		t.Fatal("receipt key changed")
	}
	if strings.Contains(jobs[0].Result, retainedKey) {
		t.Fatal("queue list exposes key")
	}
}

func TestLegacyPreparedJobIsNeverSent(t *testing.T) {
	root := t.TempDir()
	id := recording.NewID()
	dir := filepath.Join(root, "queue", id)
	os.MkdirAll(dir, 0700)
	job := Job{Version: 1, ID: id, Owner: 1, State: "pending"}
	data, _ := json.Marshal(job)
	os.WriteFile(filepath.Join(dir, "job.json"), data, 0600)
	os.WriteFile(filepath.Join(dir, "request.json"), []byte("old request"), 0600)
	client := gh.New("token")
	calls := 0
	client.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/user" {
			t.Fatal("legacy job made a request", r.URL.Path)
		}
		return response(map[string]any{"id": 1}), nil
	})
	reports := 0
	if err := (Store{Root: root}).Run(t.Context(), client, func(j Job) {
		reports++
		if !strings.Contains(j.Error, "unsupported") {
			t.Fatal(j)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if reports != 1 || calls != 0 {
		t.Fatal(reports, calls)
	}
}
