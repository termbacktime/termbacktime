package cmd

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureHelperDispatch(t *testing.T) {
	for _, args := range [][]string{nil, {"record"}, {"--version"}} {
		if handled, err := runCaptureHelper(args, strings.NewReader("")); handled || err != nil {
			t.Fatal("ordinary command used capture path", args, err)
		}
	}
	if handled, err := runCaptureHelper([]string{"__capture-event"}, strings.NewReader("")); !handled || err == nil {
		t.Fatal("malformed capture invocation was not rejected", handled, err)
	}
}

// Compare the private helper's new dispatch with the previous full CLI path.
// Both send the same event to the same local socket and await its acknowledgement.
func BenchmarkCaptureHelperDispatch(b *testing.B) {
	dir, err := os.MkdirTemp("/tmp", "tbt-helper-bench-")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "events.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		b.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			var data [1024]byte
			_, _ = c.Read(data[:])
			_, _ = c.Write([]byte("ok\n"))
			_ = c.Close()
		}
	}()
	b.Cleanup(func() { _ = listener.Close(); <-done })
	b.Setenv("TBT_CAPTURE_SOCKET", socket)
	b.Setenv("TBT_CAPTURE_TOKEN", "benchmark")
	args := []string{"__capture-event", "ready", "1"}
	for _, fullCLI := range []bool{true, false} {
		name := "FastPath"
		if fullCLI {
			name = "FullCLI"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				input := strings.NewReader("")
				if fullCLI {
					root := NewRoot()
					root.SetArgs(args)
					root.SetIn(input)
					root.SetOut(io.Discard)
					root.SetErr(io.Discard)
					if err := root.ExecuteContext(context.Background()); err != nil {
						b.Fatal(err)
					}
				} else if handled, err := runCaptureHelper(args, input); !handled || err != nil {
					b.Fatal(handled, err)
				}
			}
		})
	}
}
