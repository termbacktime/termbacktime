//go:build unix

package review

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/termbacktime/termbacktime/internal/recording"
	"golang.org/x/term"
)

func TestReviewSelectionAndCancellation(t *testing.T) {
	for _, cancelReview := range []bool{false, true} {
		t.Run(map[bool]string{false: "select", true: "cancel"}[cancelReview], func(t *testing.T) {
			master, slave, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer master.Close()
			defer slave.Close()
			pty.Setsize(slave, &pty.Winsize{Cols: 100, Rows: 40})
			before, _ := term.GetState(int(slave.Fd()))
			ready := make(chan struct{})
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				var all strings.Builder
				b := make([]byte, 32768)
				sent := false
				for {
					n, e := master.Read(b)
					if n > 0 {
						all.Write(b[:n])
						if !sent && strings.Contains(all.String(), "Review upload") {
							sent = true
							close(ready)
						}
					}
					if e != nil {
						return
					}
				}
			}()
			ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
			defer stop()
			type outcome struct {
				result Result
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				r, e := Run(ctx, slave, slave, &recording.Recording{Title: "Original", Sizes: []int{80, 24}, Metadata: &recording.Metadata{Version: 1, Description: "Approved description", UploadSystem: &recording.SystemInfo{CPUModel: "Upload chip"}}}, recording.AllMetadataFields(), false, true)
				done <- outcome{r, e}
			}()
			select {
			case <-ready:
			case <-ctx.Done():
				t.Fatal("form not displayed")
			}
			if cancelReview {
				io.WriteString(master, "\x1b")
			} else {
				io.WriteString(master, "\t\t \t \t \t \t\t\r")
			}
			select {
			case got := <-done:
				if cancelReview {
					if !errors.Is(got.err, ErrCanceled) {
						t.Fatal(got.err)
					}
				} else {
					if got.err != nil {
						t.Fatal(got.err)
					}
					if got.result.Fields != (recording.MetadataFields{}) || got.result.Title != "Original" || got.result.Description != "Approved description" {
						t.Fatalf("review result: %+v", got.result)
					}
				}
			case <-ctx.Done():
				t.Fatal("review did not finish")
			}
			after, _ := term.GetState(int(slave.Fd()))
			if *before != *after {
				t.Fatal("review did not restore terminal modes")
			}
			slave.Close()
			master.Close()
			<-finished
		})
	}
}
