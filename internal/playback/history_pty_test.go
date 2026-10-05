//go:build unix

package playback

import (
	"context"
	"github.com/termbacktime/termbacktime/internal/history"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/uitest"
	"strings"
	"testing"
	"time"
)

func TestPlaybackPTYResumeRestartAndOptOut(t *testing.T) {
	store := history.Store{Root: t.TempDir()}
	id := strings.Repeat("e", 64)
	if err := store.Enable(true); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(id, 5000, 20000); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"r", "s", "opt-out"} {
		tty := uitest.Open(t, 100, 25)
		ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
		options := HistoryOptions{Store: &store}
		if action == "opt-out" {
			options.Store = nil
		}
		r := &recording.Recording{HistoryID: id, Title: "History playback fixture", Sizes: []int{80, 24}, Lines: []recording.Event{{Lines: []string{"first"}}, {Time: 10000, Lines: []string{"middle"}}, {Time: 10000, Lines: []string{"end"}}}}
		done := make(chan error, 1)
		go func() { done <- RunWithHistory(ctx, tty.Slave, tty.Slave, r, 1, 0, 0, options) }()
		if action != "opt-out" {
			tty.Wait(ctx, "Resume playback")
			tty.Send(action)
		}
		tty.Wait(ctx, "History playback fixture", "Resume playback")
		tty.Send(" q")
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("playback did not exit")
		}
		tty.Restored()
		cancel()
		state, err := store.State()
		if err != nil {
			t.Fatal(err)
		}
		if action == "r" && state.Entries[id].Position < 5000 {
			t.Fatal("resume position not persisted")
		}
		if action == "s" && state.Entries[id].Position >= 5000 {
			t.Fatal("start over kept old position")
		}
	}
}
