package cmd

import (
	"bytes"
	"fmt"
	"github.com/spf13/cobra"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/termbacktime/termbacktime/internal/buildinfo"
	"github.com/termbacktime/termbacktime/internal/updates"
)

func TestCLIUpdateChecks(t *testing.T) {
	previous := buildinfo.Version
	buildinfo.Version = "v1.0.0-rc.9"
	t.Cleanup(func() { buildinfo.Version = previous })
	calls := 0
	status := 200
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(status)
		fmt.Fprint(w, `[{"tag_name":"v1.0.0-rc.10"}]`)
	}))
	defer s.Close()
	dir := filepath.Join(t.TempDir(), "data")
	now := time.Now()
	c := &updates.Checker{HTTP: s.Client(), Endpoint: s.URL, Now: func() time.Time { return now }}
	run := func(args ...string) (string, string, error) {
		root := newRootWithUpdates(c)
		var out, errOut bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs(append([]string{"--data-dir", dir, "--config", filepath.Join(t.TempDir(), "config.json")}, args...))
		err := root.Execute()
		return out.String(), errOut.String(), err
	}
	for _, args := range [][]string{{"--help"}, {"--version"}, {"completion", "bash"}, {}} {
		if _, _, err := run(args...); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 0 {
		t.Fatal("metadata command accessed network")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("metadata command created data directory")
	}
	for i := 0; i < 2; i++ {
		out, notification, err := run("list", "--json")
		if err != nil || strings.Contains(notification, "Update available") != (i == 0) || strings.Contains(out, "Update available") {
			t.Fatalf("%s %s %v", out, notification, err)
		}
	}
	if calls != 1 {
		t.Fatal("automatic checks were not cached")
	}
	now = now.Add(24*time.Hour - time.Second)
	if _, notice, err := run("list", "--json"); err != nil || notice != "" || calls != 1 {
		t.Fatal("notice before 24 hours", notice, err, calls)
	}
	now = now.Add(time.Second)
	if _, notice, err := run("list", "--json"); err != nil || !strings.Contains(notice, "Update available") || calls != 2 {
		t.Fatal("missing next-day notice", notice, err, calls)
	}
	out, _, err := run("--check-update")
	if err != nil || !strings.Contains(out, "rc.10") || calls != 3 {
		t.Fatalf("forced check: %s %v %d", out, err, calls)
	}
	status = 403
	if _, _, err := run("--check-update"); err == nil {
		t.Fatal("explicit check hid network failure")
	}
	if _, _, err := run("list", "--json"); err != nil || calls != 4 {
		t.Fatal("cached failure interrupted command or retried")
	}
}

func TestConcurrentAutomaticCommandsShowOneNotice(t *testing.T) {
	previous := buildinfo.Version
	buildinfo.Version = "v1.0.0"
	t.Cleanup(func() { buildinfo.Version = previous })
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		fmt.Fprint(w, `[{"tag_name":"v1.0.1"}]`)
	}))
	defer server.Close()
	checker := &updates.Checker{HTTP: server.Client(), Endpoint: server.URL, Now: func() time.Time { return time.Unix(100000, 0) }}
	o := &options{dataDir: t.TempDir(), updates: checker}
	run := func() string {
		c := &cobra.Command{}
		c.SetContext(t.Context())
		var out bytes.Buffer
		c.SetErr(&out)
		if err := o.checkUpdates(c, false); err != nil {
			return err.Error()
		}
		return out.String()
	}
	first := make(chan string, 1)
	go func() { first <- run() }()
	<-entered
	if output := run(); output != "" {
		t.Error("parallel command showed a notice", output)
	}
	close(release)
	if output := <-first; !strings.Contains(output, "Update available") {
		t.Fatal("fresh check did not notify", output)
	}
	if output := run(); output != "" {
		t.Fatal("cached check repeated notice", output)
	}
}
