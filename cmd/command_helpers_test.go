package cmd

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/updates"
)

type commandHarness struct {
	t            *testing.T
	data, config string
}

func newCommandHarness(t *testing.T) *commandHarness {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("TERMBACKTIME_TOKEN", "")
	t.Setenv("TERMBACKTIME_DATA_DIR", "")
	return &commandHarness{t: t, data: filepath.Join(dir, "data"), config: filepath.Join(dir, "config.json")}
}

func (h *commandHarness) command(args ...string) *cobra.Command {
	h.t.Helper()
	// Even release builds must keep command tests independent of the network.
	checker := &updates.Checker{Endpoint: "https://releases.example", Now: time.Now, HTTP: &http.Client{Transport: authTransport(func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `[{"tag_name":"v1.0.0"}]`), nil
	})}}
	c := newRootWithUpdates(checker)
	c.SetIn(strings.NewReader(""))
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	c.SetArgs(append([]string{"--data-dir", h.data, "--config", h.config, "--endpoint", "https://play.example"}, args...))
	return c
}

func (h *commandHarness) run(ctx context.Context, args ...string) (string, string, error) {
	h.t.Helper()
	c := h.command(args...)
	var out, diagnostic bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&diagnostic)
	err := c.ExecuteContext(ctx)
	return out.String(), diagnostic.String(), err
}

func (h *commandHarness) recording(r *recording.Recording) (string, *library.Entry) {
	h.t.Helper()
	path := filepath.Join(h.data, "recordings", recording.NewID()+".tbt")
	if err := recording.Save(path, r); err != nil {
		h.t.Fatal(err)
	}
	entry, err := (library.Library{Root: h.data}).Register(path)
	if err != nil {
		h.t.Fatal(err)
	}
	return path, entry
}

func setCommandFlags(t *testing.T, c *cobra.Command, values map[string]string) {
	t.Helper()
	for name, value := range values {
		if err := c.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
}

type commandWriteFailure struct{ err error }

func (w commandWriteFailure) Write([]byte) (int, error) { return 0, w.err }
