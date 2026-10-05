package uploadqueue

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/recording"
)

func TestCancelRetryAndRunnerLockPreserveJob(t *testing.T) {
	store := Store{Root: t.TempDir()}
	source := filepath.Join(t.TempDir(), "source.tbt")
	if err := recording.Save(source, &recording.Recording{Sizes: []int{80, 24}}); err != nil {
		t.Fatal(err)
	}
	client := gh.New("token")
	client.SiteURL = "https://example.com"
	client.HTTP.Transport = transport(func(*http.Request) (*http.Response, error) { return response(map[string]any{"id": 42}), nil })
	job, err := store.Enqueue(t.Context(), client, source, source, false, gh.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := store.Cancel(job.ID); err != nil {
			t.Fatal(err)
		}
	}
	current, err := store.load(job.ID)
	if err != nil || current.State != "canceled" {
		t.Fatal(current, err)
	}
	if err := store.Retry(job.ID); err != nil {
		t.Fatal(err)
	}
	current, err = store.load(job.ID)
	if err != nil || current.State != "pending" || store.canceled(job.ID) {
		t.Fatal(current, err)
	}
	lock, err := store.lock()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	unlock(lock)
	if !store.canceled(job.ID) {
		t.Fatal("locked runner did not receive cancellation")
	}
	current.State = "complete"
	if err := store.save(current); err != nil {
		t.Fatal(err)
	}
	if err := store.Cancel(job.ID); err == nil {
		t.Fatal("complete job canceled")
	}
	if err := store.Cancel("missing"); err == nil {
		t.Fatal("missing job canceled")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("source removed", err)
	}
}
