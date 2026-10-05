//go:build unix

package review

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/termbacktime/termbacktime/internal/uitest"
)

func TestStorageChooserConfirmationCancellationAndRestoration(t *testing.T) {
	for _, mode := range []string{"confirm", "escape", "context"} {
		t.Run(mode, func(t *testing.T) {
			tty := uitest.Open(t, 80, 24)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			type outcome struct {
				value string
				err   error
			}
			done := make(chan outcome, 1)
			go func() {
				value, err := ChooseStorage(ctx, tty.Slave, tty.Slave, "repo")
				done <- outcome{value, err}
			}()
			tty.Wait(ctx, "Where should this recording be uploaded?")
			switch mode {
			case "confirm":
				tty.Send("j")
				tty.Wait(ctx, "> GitHub Gist")
				tty.Send("\r")
			case "escape":
				tty.Send("\x1b")
			case "context":
				cancel()
			}
			select {
			case result := <-done:
				if mode == "confirm" && (result.err != nil || result.value != "gist") {
					t.Fatal(result)
				}
				if mode == "escape" && (!errors.Is(result.err, ErrCanceled) || result.value != "") {
					t.Fatal(result)
				}
				if mode == "context" && (!errors.Is(result.err, context.Canceled) || result.value != "") {
					t.Fatal(result)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("storage chooser did not finish")
			}
			tty.Restored()
		})
	}
}
