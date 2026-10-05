package manage

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/review"
	"github.com/termbacktime/termbacktime/internal/uitest"
	"github.com/termbacktime/termbacktime/internal/upload"
)

type uploadStore struct {
	fakeStore
	calls    atomic.Int32
	reviewed chan review.Result
	opened   chan string
}

func (s *uploadStore) PrepareUpload(ctx context.Context, item Item) (UploadDraft, error) {
	return UploadDraft{Recording: &recording.Recording{Title: item.Title(), Sizes: []int{80, 24}, Metadata: &recording.Metadata{Version: 1, Description: "Review this description"}}, Fields: recording.AllMetadataFields()}, nil
}
func (s *uploadStore) Upload(ctx context.Context, item Item, draft UploadDraft, r review.Result, progress func(upload.Progress)) (upload.Result, error) {
	n := s.calls.Add(1)
	s.reviewed <- r
	if item.Title() != "Beta" {
		return upload.Result{}, fmt.Errorf("selection changed")
	}
	if n == 1 {
		progress(upload.Progress{Stage: "Uploading", Sent: 50, Total: 100})
		<-ctx.Done()
		return upload.Result{}, ctx.Err()
	}
	if n == 2 {
		return upload.Result{}, fmt.Errorf("authentication required")
	}
	if r.Queue {
		return upload.Result{QueueID: "queued-selected-recording"}, nil
	}
	return upload.Result{Link: "https://site.example/p/selected"}, nil
}
func (s *uploadStore) OpenLink(link string) error { s.opened <- link; return nil }
func TestManagedUploadPTYReviewCancellationErrorsAndResult(t *testing.T) {
	tty := uitest.Open(t, 100, 30)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	backend := &uploadStore{fakeStore: fakeStore{items: sampleItems()}, reviewed: make(chan review.Result, 8), opened: make(chan string, 1)}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, tty.Slave, tty.Slave, backend, false, nil) }()
	tty.Wait(ctx, "Which recordings")
	tty.Send("1")
	tty.Wait(ctx, "Alpha")
	tty.Send("j")
	tty.Wait(ctx, "Metadata for Beta")
	tty.Send("u")
	tty.Wait(ctx, "Review upload", "Recording manager")
	tty.Send("\x1b")
	tty.Wait(ctx, "Upload canceled", "Review upload")
	if backend.calls.Load() != 0 {
		t.Fatal("canceling review uploaded")
	}
	tty.Send("u")
	tty.Wait(ctx, "Review upload")
	tty.Send(strings.Repeat("\t", 7) + " \t \t\r")
	tty.Wait(ctx, "Uploading · 50%", "Review upload")
	tty.Send("\x1b")
	tty.Wait(ctx, "Upload canceled", "Uploading · 50%")
	reviewed := <-backend.reviewed
	if !reviewed.Public || !reviewed.Encrypted || reviewed.Queue {
		t.Fatal("options lost", reviewed)
	}
	tty.Send("u")
	tty.Wait(ctx, "Review upload")
	tty.Send(strings.Repeat("\t", 9) + "\r")
	tty.Wait(ctx, "authentication required", "Review upload")
	tty.Send("u")
	tty.Wait(ctx, "Review upload")
	tty.Send(strings.Repeat("\t", 10) + "\r")
	tty.Wait(ctx, "queued-selected-recording", "Review upload")
	tty.Send("u")
	tty.Wait(ctx, "Review upload")
	tty.Send(strings.Repeat("\t", 9) + "\r")
	tty.Wait(ctx, "https://site.example/p/selected", "Review upload")
	tty.Send("o")
	select {
	case link := <-backend.opened:
		if link != "https://site.example/p/selected" {
			t.Fatal(link)
		}
	case <-ctx.Done():
		t.Fatal("open did not run")
	}
	tty.Send("\r")
	tty.Wait(ctx, "Recording manager", "https://site.example/p/selected")
	tty.Send("?")
	tty.Wait(ctx, "Shortcuts")
	tty.Send("\x1b")
	tty.Wait(ctx, "Recording manager", "Shortcuts")
	tty.Send("q")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	tty.Restored()
	if backend.calls.Load() != 4 {
		t.Fatal("upload was repeated")
	}
}
