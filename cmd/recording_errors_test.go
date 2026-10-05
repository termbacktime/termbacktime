package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestRecordingSyncErrorsReachStderrOrDashboardAndStopCleanly(t *testing.T) {
	for _, dashboard := range []bool{false, true} {
		t.Run(map[bool]string{false: "stderr", true: "dashboard"}[dashboard], func(t *testing.T) {
			c := &cobra.Command{}
			var diagnostic bytes.Buffer
			c.SetErr(&diagnostic)
			failures := make(chan error, 1)
			failures <- errors.New("disk unavailable")
			var stop func()
			if dashboard {
				reported := make(chan string, 1)
				stop = watchSyncErrors(c, failures, func(message string) { reported <- message })
				select {
				case message := <-reported:
					if message != "Recording sync failed; journal retained: disk unavailable" {
						t.Fatal(message)
					}
				case <-time.After(time.Second):
					t.Fatal("dashboard did not receive sync failure")
				}
				stop()
				if diagnostic.Len() != 0 {
					t.Fatal(diagnostic.String())
				}
			} else {
				stop = watchSyncErrors(c, failures)
				// Receiving from the channel must precede stopping the watcher.
				deadline := time.Now().Add(time.Second)
				for len(failures) != 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				stop()
				if !strings.Contains(diagnostic.String(), "journal retained: disk unavailable") {
					t.Fatal(diagnostic.String())
				}
			}
		})
	}
	c := &cobra.Command{}
	var diagnostic bytes.Buffer
	c.SetErr(&diagnostic)
	stop := watchSyncErrors(c, make(chan error))
	stop()
	if diagnostic.Len() != 0 {
		t.Fatal(diagnostic.String())
	}
}
