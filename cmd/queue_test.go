package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/uploadqueue"
)

func TestQueueCommandsListCancelRetryRunAndKeepEncryptionKeysPrivate(t *testing.T) {
	h := newCommandHarness(t)
	path, _ := h.recording(&recording.Recording{Title: "Queued demo", Sizes: []int{80, 24}, Lines: []recording.Event{{Lines: []string{"hello"}}}})
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	creates, status := 0, 422
	http.DefaultTransport = authTransport(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/user":
			return jsonResponse(200, `{"id":7,"login":"test-user"}`), nil
		case r.URL.Path == "/api/v1/config":
			return jsonResponse(200, `{"recordingFormats":["tbt-recording-v1","tbt-encrypted-v1"]}`), nil
		case r.Method == "POST" && r.URL.Path == "/gists":
			creates++
			return jsonResponse(status, `{"id":"`+strings.Repeat("a", 32)+`"}`), nil
		case r.Method == "PATCH" && r.URL.Path == "/gists/"+strings.Repeat("a", 32):
			return jsonResponse(200, `{}`), nil
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			return nil, errors.New("unexpected request")
		}
	})
	client := gh.New("runtime-token")
	client.SiteURL = "https://play.example"
	store := uploadqueue.Store{Root: h.data}
	job, err := store.Enqueue(t.Context(), client, path, path, true, gh.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(filepath.Join(h.data, "queue", job.ID, "key.txt"))
	if err != nil || len(key) == 0 {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"queue", "list"}, {"queue", "list", "--json"}} {
		out, _, err := h.run(t.Context(), args...)
		if err != nil || !strings.Contains(out, job.ID) || strings.Contains(out, string(key)) || strings.Contains(out, "#k=") {
			t.Fatal(out, err)
		}
	}
	for _, action := range []string{"cancel", "retry"} {
		out, _, err := h.run(t.Context(), "queue", action, job.ID)
		if err != nil || out != action+" "+job.ID+"\n" {
			t.Fatal(out, err)
		}
		jobs, err := store.List()
		want := "canceled"
		if action == "retry" {
			want = "pending"
		}
		if err != nil || len(jobs) != 1 || jobs[0].State != want {
			t.Fatal(jobs, err)
		}
	}
	out, diagnostic, err := h.run(t.Context(), "--token", "runtime-token", "queue", "run")
	if err != nil || creates != 1 || !strings.Contains(out, "sending") || !strings.Contains(out, "failed") || diagnostic == "" {
		t.Fatal(out, diagnostic, creates, err)
	}
	if _, _, err := h.run(t.Context(), "queue", "retry", job.ID); err != nil {
		t.Fatal(err)
	}
	status = 201
	out, diagnostic, err = h.run(t.Context(), "--token", "runtime-token", "queue", "run")
	if err != nil || creates != 2 || !strings.Contains(out, "complete") || diagnostic != "" || strings.Contains(out, string(key)) {
		t.Fatal(out, diagnostic, creates, err)
	}
	jobs, err := store.List()
	if err != nil || jobs[0].State != "complete" || strings.Contains(jobs[0].Result, "#") {
		t.Fatal(jobs, err)
	}
	link, err := (library.Library{Root: h.data}).ShareLink(path)
	if err != nil || !strings.HasSuffix(link, "#k="+string(key)) {
		t.Fatal(link, err)
	}
	out, _, err = h.run(t.Context(), "queue", "list", "--json")
	var listed []uploadqueue.Job
	if err != nil || json.Unmarshal([]byte(out), &listed) != nil || listed[0].State != "complete" || strings.Contains(out, string(key)) {
		t.Fatal(out, err)
	}
	for _, action := range []string{"cancel", "retry"} {
		if out, _, err := h.run(t.Context(), "queue", action, job.ID); err == nil || out != "" {
			t.Fatal(out, err)
		}
	}
}

func TestQueueCommandEmptyMalformedMissingAndCanceledInputs(t *testing.T) {
	h := newCommandHarness(t)
	out, _, err := h.run(t.Context(), "queue", "list", "--json")
	if err != nil || out != "[]\n" {
		t.Fatal(out, err)
	}
	for _, action := range []string{"retry", "cancel"} {
		if out, _, err := h.run(t.Context(), "queue", action, "../private"); err == nil || out != "" {
			t.Fatal(out, err)
		}
		if out, _, err := h.run(t.Context(), "queue", action, strings.Repeat("a", 32)); err == nil || out != "" {
			t.Fatal(out, err)
		}
	}
	id := strings.Repeat("a", 32)
	dir := filepath.Join(h.data, "queue", id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "job.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, _, err := h.run(t.Context(), "queue", "list", "--json"); err == nil || out != "" {
		t.Fatal(out, err)
	}
	data, _ := json.Marshal(uploadqueue.Job{Version: 2, ID: id, Owner: 7, State: "pending"})
	if err := os.WriteFile(filepath.Join(dir, "job.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if out, _, err := h.run(ctx, "queue", "run"); !errors.Is(err, context.Canceled) || out != "" {
		t.Fatal(out, err)
	}
	jobs, err := (uploadqueue.Store{Root: h.data}).List()
	if err != nil || jobs[0].State != "pending" {
		t.Fatal(jobs, err)
	}
}
