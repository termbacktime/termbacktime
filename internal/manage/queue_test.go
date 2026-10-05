package manage

import (
	"context"
	"fmt"
	"github.com/termbacktime/termbacktime/internal/uitest"
	"github.com/termbacktime/termbacktime/internal/uploadqueue"
	"strings"
	"sync"
	"testing"
	"time"
)

type queueBackendFixture struct {
	fakeStore
	mu     sync.Mutex
	job    uploadqueue.Job
	exited chan struct{}
}

func (b *queueBackendFixture) Jobs(context.Context) ([]uploadqueue.Job, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return []uploadqueue.Job{b.job}, nil
}
func (b *queueBackendFixture) RunQueue(ctx context.Context, report func(uploadqueue.Job)) error {
	defer close(b.exited)
	update := func(state string, sent int64) {
		b.mu.Lock()
		b.job.State = state
		b.job.Sent = sent
		job := b.job
		b.mu.Unlock()
		report(job)
	}
	update("sending", 50)
	<-ctx.Done()
	update("needs attention", 50)
	return ctx.Err()
}
func (b *queueBackendFixture) RetryJob(context.Context, string) error  { return nil }
func (b *queueBackendFixture) CancelJob(context.Context, string) error { return nil }
func TestManagerPTYQueueRunsBehindOtherScreensAndJoinsOnExit(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprint(stop), func(t *testing.T) {
			tty := uitest.Open(t, 110, 30)
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			backend := &queueBackendFixture{fakeStore: fakeStore{items: sampleItems()}, job: uploadqueue.Job{ID: strings.Repeat("c", 32), Source: "fixture.tbt", State: "pending", Total: 100}, exited: make(chan struct{})}
			done := make(chan error, 1)
			go func() { done <- Run(ctx, tty.Slave, tty.Slave, backend, false, nil) }()
			tty.Wait(ctx, "Which recordings")
			tty.Send("1")
			tty.Wait(ctx, "Recordings 1/2")
			tty.Send("U")
			tty.Wait(ctx, "sending")
			tty.Wait(ctx, "50/100 bytes")
			tty.Send("\x1b")
			tty.Wait(ctx, "Queue running (J)")
			tty.Send("j")
			tty.Wait(ctx, "Recordings 2/2")
			tty.Send("f")
			tty.Wait(ctx, "Sort and filter")
			tty.Send("\x1b")
			tty.Wait(ctx, "Recordings 2/2")
			if stop {
				tty.Send("J")
				tty.Wait(ctx, "Upload queue")
				tty.Send("X")
				tty.Wait(ctx, "Queue stopped")
				tty.Wait(ctx, "needs attention")
				tty.Send("\x1b")
				tty.Wait(ctx, "Recording manager")
			}
			tty.Send("q")
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("manager did not quit")
			}
			select {
			case <-backend.exited:
			default:
				t.Fatal("manager did not join queue runner")
			}
			tty.Restored()
		})
	}
}

func TestQueueScreenOmitsSavedPlaybackLinks(t *testing.T) {
	m, _ := readyModel(t)
	m.queue = &queueView{jobs: []uploadqueue.Job{{ID: strings.Repeat("a", 32), State: "complete", Result: "https://site.test/p/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa#k=private", Source: "fixture.tbt"}}}
	m.mode = "queue"
	text := m.View().Content
	if strings.Contains(text, "https://") || strings.Contains(text, "#k=") {
		t.Fatal("queue exposed a playback link without an explicit receipt action")
	}
}
