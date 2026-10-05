package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDataDirectoryAndMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TERMBACKTIME_DATA_DIR", "")
	root, err := DataDir("")
	if err != nil || root != filepath.Join(home, "termbacktime") {
		t.Fatal(root, err)
	}
	t.Setenv("TERMBACKTIME_DATA_DIR", filepath.Join(home, "environment"))
	root, _ = DataDir("")
	if root != filepath.Join(home, "environment") {
		t.Fatal(root)
	}
	root, _ = DataDir("~/Documents/termbacktime")
	if root != filepath.Join(home, "Documents/termbacktime") {
		t.Fatal(root)
	}
	legacy := filepath.Join(home, ".termbacktime.json")
	original := []byte(`{"token":"private","unknown":{"preserved":true}}`)
	if err := os.WriteFile(legacy, original, 0600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "termbacktime.json")
	if err := Migrate(dest); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dest)
	if string(b) != string(original) {
		t.Fatal("changed legacy config")
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy file retained")
	}
	st, _ := os.Stat(dest)
	if st.Mode().Perm() != 0600 {
		t.Fatal("public config")
	}
	if err := os.WriteFile(legacy, []byte(`{"token":"different"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(dest); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(dest)
	if string(b) != string(original) {
		t.Fatal("overwrote destination")
	}
}
