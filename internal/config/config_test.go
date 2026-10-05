package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyConfigPreservedAndProtected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"login":"legacy","version-check":false,"token":"old"}`), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Set("token", "new-secret")
	if err = c.Save(path); err != nil {
		t.Fatal(err)
	}
	c, err = Load(path)
	if err != nil || c.String("login") != "legacy" || string(c["version-check"]) != "false" {
		t.Fatal("lost legacy preferences", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("credentials are not protected")
	}
	link := filepath.Join(filepath.Dir(path), "link")
	_ = os.Symlink(path, link)
	if err = c.Save(link); err == nil {
		t.Fatal("followed symlink")
	}
}
