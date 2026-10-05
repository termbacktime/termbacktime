package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExpandPath expands only the current user's home prefix
func ExpandPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return filepath.Abs(path)
}

func DataDir(explicit string) (string, error) {
	if explicit == "" {
		explicit = os.Getenv("TERMBACKTIME_DATA_DIR")
	}
	if explicit == "" {
		explicit = "~/termbacktime"
	}
	return ExpandPath(explicit)
}

func EnsurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", path)
	}
	return os.Chmod(path, 0700)
}

// Migrate never replaces an existing destination, including concurrent migrations
func Migrate(destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	legacy := filepath.Join(home, ".termbacktime.json")
	if legacy == destination {
		return nil
	}
	if _, err := os.Lstat(legacy); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := Load(legacy); err != nil {
		return err
	}
	data, err := os.ReadFile(legacy)
	if err != nil {
		return err
	}
	if err := EnsurePrivateDir(filepath.Dir(destination)); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".config-migrate-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	err = directory.Sync()
	directory.Close()
	if err != nil {
		return err
	}
	check, err := os.ReadFile(destination)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, check) {
		return fmt.Errorf("configuration migration verification failed")
	}
	// Leave a changed legacy file alone rather than deleting another process's update
	latest, err := os.ReadFile(legacy)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(data, latest) {
		return fmt.Errorf("legacy configuration changed during migration; both files retained")
	}
	return os.Remove(legacy)
}
