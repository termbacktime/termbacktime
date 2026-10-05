package cmd

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/creack/pty"
	"github.com/termbacktime/termbacktime/internal/live"
)

func TestLiveDashboardDefaultsAndRedirection(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	for _, tc := range []struct {
		name           string
		input          io.Reader
		output         io.Writer
		disabled, want bool
	}{
		{"interactive default", slave, slave, false, true}, {"explicit plain", slave, slave, true, false}, {"redirected input", bytes.NewReader(nil), slave, false, false}, {"redirected output", slave, io.Discard, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			c := liveCommand(&options{}, func(ctx context.Context, o live.Options) error {
				called = true
				if (o.Presentation != nil) != tc.want {
					t.Fatal("wrong presentation")
				}
				return nil
			})
			c.SetContext(t.Context())
			c.SetIn(tc.input)
			c.SetOut(tc.output)
			if tc.disabled {
				c.Flags().Set("no-dashboard", "true")
			}
			if err := c.RunE(c, nil); err != nil {
				t.Fatal(err)
			}
			if !called {
				t.Fatal("session did not start")
			}
		})
	}
}
