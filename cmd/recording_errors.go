package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/termbacktime/termbacktime/internal/recording"
)

func watchRecordingErrors(c *cobra.Command, w *recording.Writer, report ...func(string)) func() {
	return watchSyncErrors(c, w.SyncErrors(), report...)
}

func watchSyncErrors(c *cobra.Command, failures <-chan error, report ...func(string)) func() {
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		select {
		case err := <-failures:
			message := fmt.Sprint("Recording sync failed; journal retained: ", err)
			if len(report) > 0 {
				report[0](message)
			} else {
				fmt.Fprintln(c.ErrOrStderr(), message)
			}
		case <-stop:
		}
	}()
	return func() { close(stop); <-done }
}
