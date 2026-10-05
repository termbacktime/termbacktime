package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/termbacktime/termbacktime/internal/dashboard"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/live"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/terminal"
)

func TestLiveCommandWiresSessionOptionsAndKeepsCaptureAfterSessionFailure(t *testing.T) {
	for _, mode := range []string{"unsaved", "record", "output", "session failure", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			h := newCommandHarness(t)
			sentinel := errors.New("live transport interrupted")
			var out, diagnostic bytes.Buffer
			c := liveCommand(&options{dataDir: h.data, endpoint: "https://live.example"}, func(ctx context.Context, o live.Options) error {
				if o.Endpoint != "https://live.example" || o.Shell != "/bin/sh" || !reflect.DeepEqual(o.Args, []string{"-c", "echo hello"}) || o.Title != "Live demo" || o.TTL != time.Hour || o.Presentation != nil || o.Started <= 0 {
					t.Fatal(o)
				}
				o.OnReady("https://live.example/l/viewer#k=viewer-key")
				o.OnHostReady("https://live.example/host#h=host-key")
				o.OnStatus("Sharing paused")
				if err := o.OnEvent(terminal.Event{Text: "captured output"}); err != nil {
					t.Fatal(err)
				}
				if mode == "session failure" {
					return sentinel
				}
				if mode == "canceled" {
					return context.Canceled
				}
				return nil
			})
			c.SetIn(strings.NewReader(""))
			c.SetOut(&out)
			c.SetErr(&diagnostic)
			args := []string{"--no-dashboard", "--no-metadata", "--title", "Live demo", "--ttl", "1h", "--shell", "/unused/shell"}
			if mode == "output" {
				args = append(args, "--output", filepath.Join(h.data, "custom", "live.tbt"))
			} else if mode != "unsaved" {
				args = append(args, "--record")
			}
			c.SetArgs(append(args, "--", "/bin/sh", "-c", "echo hello"))
			err := c.ExecuteContext(t.Context())
			if mode == "session failure" {
				if !errors.Is(err, sentinel) {
					t.Fatal(err)
				}
			} else if mode == "canceled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "Read-only live link:") || !strings.Contains(out.String(), "Private host controls:") || !strings.Contains(diagnostic.String(), "Sharing paused") {
				t.Fatal(out.String(), diagnostic.String())
			}
			if mode == "unsaved" {
				if _, err := os.Stat(h.data); !os.IsNotExist(err) {
					t.Fatal("unsaved live created storage", err)
				}
				return
			}
			entries, err := (library.Library{Root: h.data}).List()
			if err != nil || len(entries) != 1 || entries[0].Status != "ready" || !strings.Contains(diagnostic.String(), "Saved ") {
				t.Fatal(entries, diagnostic.String(), err)
			}
			r, err := recording.Load(entries[0].Path)
			if err != nil || r.Title != "Live demo" || r.Metadata != nil || r.Lines[0].Lines[0] != "captured output" {
				t.Fatal(r, err)
			}
			if mode == "output" && filepath.Dir(entries[0].Path) != filepath.Join(h.data, "custom") {
				t.Fatal(entries[0])
			}
		})
	}
}

func TestLiveCommandInvalidOptionsAndFailedFinalizationPreserveJournal(t *testing.T) {
	h := newCommandHarness(t)
	for _, args := range [][]string{{"--ttl", "0s"}, {"--ttl", "999ms"}, {"--sync-interval", "-1s"}} {
		c := liveCommand(&options{dataDir: h.data}, func(context.Context, live.Options) error { t.Fatal("started invalid session"); return nil })
		c.SetOut(io.Discard)
		c.SetErr(io.Discard)
		c.SetArgs(args)
		if err := c.ExecuteContext(t.Context()); err == nil {
			t.Fatal(args)
		}
	}
	path := filepath.Join(h.data, "recordings", "failed.tbt")
	sentinel := errors.New("transport failed")
	c := liveCommand(&options{dataDir: h.data}, func(ctx context.Context, o live.Options) error {
		if err := o.OnEvent(terminal.Event{Cols: 501, Rows: 24}); err != nil {
			t.Fatal(err)
		}
		return sentinel
	})
	c.SetIn(strings.NewReader(""))
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	c.SetArgs([]string{"--no-metadata", "--output", path})
	if err := c.ExecuteContext(t.Context()); !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "recording failed; recover") {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("published invalid capture", err)
	}
	if _, err := os.Stat(path + ".partial"); err != nil {
		t.Fatal("lost recovery journal", err)
	}
}

func TestLiveDashboardReportersShowPausedPendingStaleAndDiagnostics(t *testing.T) {
	dash := dashboard.New(os.Stdout, "", dashboard.LiveMode, nil)
	report := roomReporter(dash)
	report(live.RoomStatus{Viewers: 3, Paused: true, Revision: 2, Acknowledged: 1, Expires: 1700000000000}, nil)
	var state dashboard.State
	dash.Set(func(s *dashboard.State) { state = *s })
	if !strings.Contains(state.Room, "3 viewers · paused") || !strings.Contains(state.Room, "pending publisher acknowledgement") {
		t.Fatal(state.Room)
	}
	report(live.RoomStatus{}, errors.New("network failure"))
	dash.Set(func(s *dashboard.State) { state = *s })
	if state.Room != "Room status unavailable (stale)" {
		t.Fatal(state.Room)
	}
	diagnostic := live.Diagnostics{State: "reconnecting", ReconnectAttempts: 2, BufferedBytes: 4096}
	diagnosticsReporter(dash)(diagnostic)
	dash.Set(func(s *dashboard.State) { state = *s })
	if state.Diagnostics != diagnostic {
		t.Fatal(state.Diagnostics)
	}
	if roomReporter(nil) != nil || diagnosticsReporter(nil) != nil {
		t.Fatal("plain sessions acquired dashboard callbacks")
	}
}
