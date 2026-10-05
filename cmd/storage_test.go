package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorageCLIRequiresExplicitApplyAndPreservesJSON(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "exports", "example.html")
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, []byte("example"), 0600)
	run := func(args ...string) (string, error) {
		c := NewRoot()
		var out bytes.Buffer
		c.SetOut(&out)
		c.SetErr(io.Discard)
		c.SetArgs(append([]string{"--data-dir", root, "storage"}, args...))
		err := c.ExecuteContext(t.Context())
		return out.String(), err
	}
	text, err := run("usage", "--json")
	if err != nil || !json.Valid([]byte(text)) || !strings.Contains(text, "exports") {
		t.Fatal(text, err)
	}
	text, err = run("clean", "--kind", "exports", "--min-size", "1B", "--json")
	if err != nil || !json.Valid([]byte(text)) || !strings.Contains(text, "example.html") {
		t.Fatal(text, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("preview deleted")
	}
	if _, err := run("clean", "--apply"); err == nil {
		t.Fatal("unconfirmed noninteractive cleanup succeeded")
	}
	if _, err := run("clean", "--yes"); err == nil {
		t.Fatal("yes without apply succeeded")
	}
	text, err = run("clean", "--kind", "exports", "--apply", "--yes", "--json")
	if err != nil || !json.Valid([]byte(text)) || !strings.Contains(text, `"deleted":true`) {
		t.Fatal(text, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("apply did not delete")
	}
}
