package cmd

import (
	"context"
	"github.com/creack/pty"
	"github.com/termbacktime/termbacktime/internal/recording"
	"golang.org/x/term"
	"io"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestInteractivePlaybackRestoresTerminal(t *testing.T) {
	for _, cancelPlayback := range []bool{false, true} {
		t.Run(map[bool]string{false: "quit", true: "cancel"}[cancelPlayback], func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("TERMBACKTIME_DATA_DIR", filepath.Join(dir, "data"))
			path := filepath.Join(dir, "recording.json")
			if err := recording.Save(path, &recording.Recording{Sizes: []int{80, 24}, Lines: []recording.Event{{Lines: []string{"Unicode 世界"}}, {Time: 10000, Lines: []string{"done"}}}}); err != nil {
				t.Fatal(err)
			}
			master, slave, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer master.Close()
			defer slave.Close()
			go io.Copy(io.Discard, master)
			before, err := term.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			root := NewRoot()
			root.SetIn(slave)
			root.SetOut(slave)
			root.SetErr(io.Discard)
			root.SetArgs([]string{"play", path})
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- root.ExecuteContext(ctx) }()
			deadline := time.After(time.Second)
			for {
				now, err := term.GetState(int(slave.Fd()))
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, now) {
					break
				}
				select {
				case <-deadline:
					t.Fatal("interactive mode never started")
				case <-time.After(time.Millisecond):
				}
			}
			if cancelPlayback {
				cancel()
			} else {
				if _, err := master.Write([]byte(" ,.+-iolq")); err != nil {
					t.Fatal(err)
				}
			}
			err = <-done
			if !cancelPlayback && err != nil {
				t.Fatal(err)
			}
			after, err := term.GetState(int(slave.Fd()))
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("terminal state was not restored", err)
			}
		})
	}
}
