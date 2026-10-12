package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creack/pty"
	"github.com/termbacktime/termbacktime/internal/live"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/terminal"
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

func TestSavedLiveCommandCaptureAndOptOut(t *testing.T) {
	for _, test := range []struct{ save, optOut bool }{{false, false}, {true, false}, {true, true}} {
		t.Run(fmt.Sprintf("save=%v optout=%v", test.save, test.optOut), func(t *testing.T) {
			dir := t.TempDir()
			c := liveCommand(&options{dataDir: filepath.Join(dir, "data")}, func(ctx context.Context, o live.Options) error {
				if o.CommandCapture != (test.save && !test.optOut) {
					t.Fatal("wrong command capture policy")
				}
				if test.save {
					status := 0
					id := strings.Repeat("a", 32)
					if err := o.OnEvent(terminal.Event{Lifecycle: &terminal.CommandEvent{ID: id, Phase: "start", Text: "echo hello"}}); err != nil {
						return err
					}
					return o.OnEvent(terminal.Event{Lifecycle: &terminal.CommandEvent{ID: id, Phase: "end", ExitCode: &status}})
				}
				return nil
			})
			c.SetContext(t.Context())
			c.SetOut(io.Discard)
			c.SetErr(io.Discard)
			_ = c.Flags().Set("no-dashboard", "true")
			_ = c.Flags().Set("no-metadata", "true")
			if test.save {
				_ = c.Flags().Set("output", filepath.Join(dir, "live.tbt"))
			}
			if test.optOut {
				_ = c.Flags().Set("no-command-capture", "true")
			}
			if err := c.RunE(c, nil); err != nil {
				t.Fatal(err)
			}
			if test.save {
				r, err := recording.Load(filepath.Join(dir, "live.tbt"))
				if err != nil {
					t.Fatal(err)
				}
				if (r.Execution != nil) != !test.optOut {
					t.Fatal("execution context policy")
				}
			}
		})
	}
}
