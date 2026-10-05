package recording

import (
	"io"
	"sync"
	"time"

	"github.com/termbacktime/termbacktime/internal/terminal"
)

// Capture timestamps the same ordered PTY events used by the live screen observer
type Capture struct {
	mu       sync.Mutex
	writer   *Writer
	last     time.Time
	failure  error
	finished bool
}

func NewCapture(writer *Writer) *Capture { return &Capture{writer: writer, last: time.Now()} }
func (r *Capture) Event(e terminal.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failure != nil {
		return r.failure
	}
	if r.finished {
		return io.ErrClosedPipe
	}
	if e.Text == "" && e.Cols == 0 {
		return nil
	}
	now := time.Now()
	event := Event{Time: now.Sub(r.last).Milliseconds()}
	r.last = now
	if e.Cols > 0 {
		event.Command = "s"
		event.Sizes = []int{e.Cols, e.Rows}
	} else {
		event.Lines = []string{e.Text}
	}
	r.failure = r.writer.Append(event)
	return r.failure
}
func (r *Capture) Finish() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finished = true
	if r.failure != nil {
		_ = r.writer.Close()
		return r.failure
	}
	return r.writer.Finish()
}
