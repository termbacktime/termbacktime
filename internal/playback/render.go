package playback

import (
	"context"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/termui"
)

// Render replays without keyboard input. As in interactive playback, only the
// emulator's normalized screen reaches the output, never recorded mode changes.
func Render(ctx context.Context, out io.Writer, r *recording.Recording, speed float64, idle time.Duration, start float64, width, height int) error {
	m := New(ctx, r, speed, idle, start)
	defer m.Close()
	if m.initialErr != nil {
		return m.initialErr
	}
	paint := func() error {
		frame, _ := m.screen.Frame(true)
		_, err := fmt.Fprint(out, "\x1b[H\x1b[2J", termui.Fit(frame.Screen, width, height), "\x1b[0m")
		return err
	}
	if err := paint(); err != nil {
		return err
	}
	previous := recording.MapTime(m.position, m.original, m.presented)
	for i := m.index; i < r.Count(); i++ {
		next := float64(m.presented[i+1])
		delay := math.Max(0, (next-previous)/speed)
		previous = next
		timer := time.NewTimer(time.Duration(delay * float64(time.Millisecond)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		event, err := r.Event(i)
		if err != nil {
			return err
		}
		if err := m.applyEvent(event); err != nil {
			return err
		}
		if err := paint(); err != nil {
			return err
		}
	}
	return nil
}
