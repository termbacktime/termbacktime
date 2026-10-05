package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfigurationAndMalformedFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TERMBACKTIME_DATA_DIR", root)
	path, err := DefaultPath()
	if err != nil || path != filepath.Join(root, "termbacktime.json") {
		t.Fatal(path, err)
	}
	if err := (Config{"custom": json.RawMessage(`"preserved"`)}).Save(""); err != nil {
		t.Fatal(err)
	}
	got, err := Load("")
	if err != nil || got.String("custom") != "preserved" {
		t.Fatal(got, err)
	}
	for _, test := range []struct {
		name, data string
		invalid    bool
	}{
		{"null", "null", false},
		{"object", `{"unknown":[1,2],"number":42}`, false},
		{"malformed", "{", true},
		{"array", "[]", true},
		{"oversized", strings.Repeat(" ", 1<<20+1), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if (err != nil) != test.invalid || (!test.invalid && cfg == nil) {
				t.Fatal(cfg, err)
			}
		})
	}
	if _, err := Load(root); err == nil {
		t.Fatal("accepted directory")
	}
}

func TestFailedSaveAndMigrationPreserveSource(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	legacy := filepath.Join(root, ".termbacktime.json")
	original := []byte(`{"token":"private","unknown":{"retained":true}}`)
	if err := os.WriteFile(legacy, original, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{"broken": json.RawMessage("{")}
	if err := cfg.Save(legacy); err == nil {
		t.Fatal("saved invalid raw JSON")
	}
	assertSource := func() {
		t.Helper()
		data, err := os.ReadFile(legacy)
		if err != nil || !bytes.Equal(data, original) {
			t.Fatal("source changed", string(data), err)
		}
	}
	assertSource()
	if err := Migrate(legacy); err != nil {
		t.Fatal(err)
	}
	assertSource()
	blocked := filepath.Join(root, "file")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(filepath.Join(blocked, "config.json")); err == nil {
		t.Fatal("migration accepted a non-directory parent")
	}
	assertSource()
	destination := filepath.Join(root, "new", "config.json")
	if err := os.WriteFile(legacy, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(destination); err == nil {
		t.Fatal("migrated invalid configuration")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("failed migration published a destination", err)
	}
}

func TestReadOnlyConfigurationFailureLeavesNoTemporaryFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses filesystem permission checks")
	}
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	original := []byte(`{"token":"original"}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(root, 0700)
	if err := (Config{}).Save(path); err == nil {
		t.Fatal("saved in read-only directory")
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("failed write changed credentials", string(data), err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary file leaked", entries, err)
	}
}
