// Package termui provides graceful cancellation for interactive Bubble Tea programs.
package termui

import (
	tea "charm.land/bubbletea/v2"
	"context"
)

// Run requests a normal quit on cancellation so Bubble Tea waits for its input
// reader before closing platform polling handles and restoring terminal modes.
func Run(ctx context.Context, program *tea.Program) (tea.Model, error) {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			program.Quit()
		case <-done:
		}
	}()
	model, err := program.Run()
	close(done)
	<-stopped
	if ctx.Err() != nil {
		return model, ctx.Err()
	}
	return model, err
}
