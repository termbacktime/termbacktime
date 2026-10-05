//go:build unix

package manage

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/termui"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/uitest"
	"golang.org/x/term"
)

func TestManagerPTYPlaybackReturnQuitAndCancellationRestoreTerminal(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			tty := uitest.Open(t, 110, 30)
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()
			var loads atomic.Int32
			play := func(ctx context.Context, item Item) (*recording.Recording, error) {
				n := loads.Add(1)
				if n == 5 {
					return nil, fmt.Errorf("fixture load failure")
				}
				if n == 6 {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				duration := int64(10000)
				if n == 4 {
					duration = 200
				}
				return &recording.Recording{Title: "PLAYBACK ONLY", Sizes: []int{80, 24}, Lines: []recording.Event{
					{Lines: []string{"original\r\n\x1b[?1049h\x1b[?1h\x1b[?25l\x1b[?1003h\x1b[?1006h\x1b[?2004h\x1b[2;10r\x1b[?6h\x1b[3;1HPLAYBACK CONTENT\x1b[8;55;160t\x1b]52;c;c2VjcmV0\a"}},
					{Time: 20, Command: "s", Sizes: []int{60, 15}},
					{Time: duration, Lines: []string{"\x1b[?1049lend"}},
				}, Callouts: []recording.Callout{{ID: recording.NewID(), At: 0, Text: "DO NOT SHOW", Duration: 5000, Pause: true}}}, nil
			}
			done := make(chan error, 1)
			go func() { done <- Run(ctx, tty.Slave, tty.Slave, &fakeStore{items: sampleItems()}, false, play) }()
			tty.Wait(ctx, "Which recordings")
			tty.Send("1")
			tty.Wait(ctx, "Alpha")
			tty.Send("j")
			tty.Wait(ctx, "Metadata for Beta")
			tty.Send("/Beta\r")
			tty.Wait(ctx, "Filter: Beta")
			for _, quit := range []string{"q", "\x1b", "\x03"} {
				tty.Send("p")
				tty.Wait(ctx, "PLAYBACK ONLY", "Recording manager", "DO NOT SHOW")
				cols, rows, err := term.GetSize(int(tty.Slave.Fd()))
				if err != nil || cols != 110 || rows != 30 {
					t.Fatal("recording resized host terminal", cols, rows, err)
				}
				tty.Send(quit)
				tty.Wait(ctx, "Playback finished", "PLAYBACK ONLY", "PLAYBACK CONTENT")
				if !strings.Contains(tty.Screen(), "Filter: Beta") {
					t.Fatal("filter was lost")
				}
			}
			tty.Send("p")
			tty.Wait(ctx, "PLAYBACK ONLY")
			tty.Wait(ctx, "Playback finished", "PLAYBACK ONLY")
			tty.Send("p")
			tty.Wait(ctx, "fixture load failure")
			tty.Send("p")
			tty.Wait(ctx, "Loading playback")
			tty.Send("\x1b")
			tty.Wait(ctx, "Playback canceled")
			tty.Send("?")
			tty.Wait(ctx, "Shortcuts", "Metadata for Beta")
			tty.Send("\x1b")
			tty.Wait(ctx, "Recording manager", "Shortcuts")
			tty.Send("s")
			tty.Wait(ctx, "Complete")
			tty.Send("2")
			tty.Wait(ctx, "[2 Gists]", "Filter: Beta")
			tty.Send("1")
			tty.Wait(ctx, "[1 Local]")
			if cancelled {
				cancel()
			} else {
				tty.Send("q")
			}
			select {
			case err := <-done:
				if !cancelled && err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("manager did not quit")
			}
			tty.Restored()
			for _, unsafe := range []string{"\x1b[8;55;160t", "\x1b[2;10r", "\x1b[?1003h", "\x1b]52;c;c2VjcmV0"} {
				if strings.Contains(tty.Raw(), unsafe) {
					t.Fatalf("recording control leaked: %q", unsafe)
				}
			}
		})
	}
}

func TestManagerPTYLocalDeletionSurvivesRestart(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		t.Run(fmt.Sprintf("pinned=%t", pinned), func(t *testing.T) {
			lib := library.Library{Root: t.TempDir()}
			var firstPath, firstID string
			for n, title := range []string{"Alpha", "Beta", "Gamma"} {
				path, err := lib.Output(filepath.Join(lib.Root, "recordings", title+".tbt"))
				if err != nil {
					t.Fatal(err)
				}
				if err = recording.Save(path, &recording.Recording{Title: title, Started: int64(3 - n), Sizes: []int{80, 24}, Lines: []recording.Event{{Lines: []string{"fixture"}}}}); err != nil {
					t.Fatal(err)
				}
				entry, err := lib.Register(path)
				if err != nil {
					t.Fatal(err)
				}
				if n == 0 {
					firstPath, firstID = path, entry.ID
				}
			}
			if pinned {
				if err := lib.SetPinned(firstID, true); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()
			for restart := 0; restart < 2; restart++ {
				tty := uitest.Open(t, 110, 30)
				done := make(chan error, 1)
				runCtx, stop := context.WithCancel(ctx)
				go func() {
					done <- Run(runCtx, tty.Slave, tty.Slave, &Store{Library: lib}, false, nil)
					close(done)
				}()
				t.Cleanup(func() {
					stop()
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						t.Error("manager did not stop before terminal cleanup")
					}
				})
				tty.Wait(ctx, "Which recordings")
				tty.Send("1")
				if restart == 0 {
					tty.Wait(ctx, "Recordings 1/3")
					tty.Wait(ctx, "3 recordings loaded")
					// Start deletion while metadata has focus, as well as while it may still be loading.
					tty.Send("\td")
					tty.Wait(ctx, "Confirm deletion")
					tty.Send("delete\r")
				}
				if pinned {
					if restart == 0 {
						tty.Wait(ctx, "recording is pinned")
					}
					tty.Wait(ctx, "Recordings 1/3")
					tty.Send("j")
					tty.Wait(ctx, "Recordings 2/3")
				} else {
					tty.Wait(ctx, "Recordings 1/2", "Alpha")
					tty.Wait(ctx, "2 recordings loaded", "Alpha")
					tty.Send("j")
					tty.Wait(ctx, "Recordings 2/2", "Alpha")
				}
				tty.Send("?")
				tty.Wait(ctx, "Shortcuts")
				tty.Send("\x1b")
				tty.Wait(ctx, "Recording manager", "Shortcuts")
				tty.Send("k")
				if pinned {
					tty.Wait(ctx, "Recordings 1/3")
				} else {
					tty.Wait(ctx, "Recordings 1/2")
				}
				tty.Send("q")
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("manager did not quit", ctx.Err())
				}
				tty.Restored()
				_, err := os.Stat(firstPath)
				if pinned && err != nil || !pinned && !os.IsNotExist(err) {
					t.Fatal("unexpected deletion result", err)
				}
				entries, err := lib.List()
				want := 2
				if pinned {
					want = 3
				}
				if err != nil || len(entries) != want {
					t.Fatal("library did not persist deletion result", entries, err)
				}
			}
		})
	}
}

func TestManagerPTYResizeAndPaneTransitionsErasePreviousScreen(t *testing.T) {
	tty := uitest.Open(t, 100, 30)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	m := newModel(ctx, &fakeStore{items: sampleItems()}, false, nil)
	program := tea.NewProgram(m, tea.WithInput(tty.Slave), tea.WithOutput(tty.Slave), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() { _, err := termui.Run(ctx, program); done <- err }()
	tty.Wait(ctx, "Metadata for Alpha")
	tty.Resize(50, 20)
	program.Send(tea.WindowSizeMsg{Width: 50, Height: 20})
	tty.Wait(ctx, "Recordings 1/2", "Metadata for Alpha")
	tty.Send("\t")
	tty.Wait(ctx, "Metadata for Alpha", "Recordings 1/2")
	tty.Send("?")
	tty.Wait(ctx, "Shortcuts", "Metadata for Alpha")
	tty.Send("\x1b")
	tty.Wait(ctx, "Metadata for Alpha", "Shortcuts")
	tty.Send("i")
	tty.Wait(ctx, "Import a local recording", "Metadata for Alpha")
	tty.Send("\x1b")
	tty.Wait(ctx, "Metadata for Alpha", "Import a local recording")
	tty.Resize(110, 32)
	program.Send(tea.WindowSizeMsg{Width: 110, Height: 32})
	tty.Wait(ctx, "Recordings 1/2")
	tty.Send("q")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	tty.Restored()
}

func TestManagerPTYPendingDeleteCanBeCanceled(t *testing.T) {
	tty := uitest.Open(t, 100, 30)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	backend := &canceledDeleteStore{fakeStore: fakeStore{items: sampleItems()}, started: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, tty.Slave, tty.Slave, backend, false, nil) }()
	tty.Wait(ctx, "Which recordings")
	tty.Send("1")
	tty.Wait(ctx, "Recordings 1/2")
	tty.Send("d")
	tty.Wait(ctx, "Confirm deletion")
	tty.Send("delete\r")
	tty.Wait(ctx, "Deleting recording", "Recordings 1/2")
	tty.Wait(ctx, "Esc cancels")
	tty.Send("\x1b")
	tty.Wait(ctx, "Action canceled", "Deleting recording")
	tty.Send("j")
	tty.Wait(ctx, "Recordings 2/2")
	tty.Send("q")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("manager did not quit", ctx.Err())
	}
	tty.Restored()
}
