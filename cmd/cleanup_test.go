package cmd

import (
	"bytes"
	"github.com/termbacktime/termbacktime/internal/recording"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemovedCommandsAndFlags(t *testing.T) {
	root := NewRoot()
	for _, name := range []string{"clip", "mark", "capture", "search", "callout"} {
		for _, c := range root.Commands() {
			if c.Name() == name {
				t.Fatalf("removed command %s is registered", name)
			}
		}
		root.SetArgs([]string{name})
		if root.Execute() == nil {
			t.Fatalf("removed command %s accepted", name)
		}
	}
	for _, name := range []string{"record", "live"} {
		c, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		if c.Flags().Lookup("shell-integration") != nil {
			t.Fatal("shell flag remains")
		}
		if (c.Flags().Lookup("dashboard") != nil) != (name == "record") {
			t.Fatal("dashboard flag")
		}
		if (c.Flags().Lookup("no-dashboard") != nil) != (name == "live") {
			t.Fatal("no-dashboard flag")
		}
	}
	for _, name := range []string{"scan", "redact", "queue", "export", "play", "record", "recover", "manage"} {
		c, _, err := root.Find([]string{name})
		if err != nil || c == root {
			t.Fatal("missing retained command", name)
		}
	}
	var completion bytes.Buffer
	root = NewRoot()
	root.SetOut(&completion)
	root.SetErr(&completion)
	root.SetArgs([]string{"__complete", ""})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(completion.String(), "\n") {
		name := strings.SplitN(line, "\t", 2)[0]
		for _, removed := range []string{"clip", "mark", "capture", "search", "callout"} {
			if name == removed {
				t.Fatal("completion contains removed command", name)
			}
		}
	}
}

func TestRetainedTranscriptExports(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.json")
	r := &recording.Recording{Sizes: []int{40, 8}, Lines: []recording.Event{{Lines: []string{"first line\r\n"}}, {Time: 10, Lines: []string{"second line"}}}}
	if err := recording.Save(path, r); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"", "txt", "md"} {
		destination := filepath.Join(dir, "transcript."+format)
		c := newExport(&options{dataDir: filepath.Join(dir, "data")})
		c.SetContext(t.Context())
		c.SetOut(io.Discard)
		if format != "" {
			c.Flags().Set("format", format)
		}
		c.Flags().Set("output", destination)
		if err := c.RunE(c, []string{path}); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(destination)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "first line") || !strings.Contains(string(data), "second line") {
			t.Fatal("transcript lost output")
		}
		if strings.Contains(string(data), "``` text") != (format == "md") {
			t.Fatal("wrong transcript format", string(data))
		}
	}
}

func TestRemovedHTMLExportAndPassphraseFlags(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "player.html")
	c := newExport(&options{dataDir: filepath.Join(dir, "data")})
	c.Flags().Set("format", "html")
	c.Flags().Set("output", destination)
	if err := c.RunE(c, []string{"missing.tbt"}); err == nil || err.Error() != "supported export formats: txt, md" {
		t.Fatalf("HTML export should fail before loading the recording: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("HTML export created an output file: %v", err)
	}
	for _, flag := range []string{"encrypt", "passphrase-stdin"} {
		c := newExport(&options{})
		if err := c.ParseFlags([]string{"--" + flag}); err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Fatalf("removed flag --%s accepted: %v", flag, err)
		}
	}
}
