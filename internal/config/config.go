package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// EmbeddedSiteURL is set from deployment environment fields by release tooling
var EmbeddedSiteURL string

func SiteURL() string {
	for _, key := range []string{"TERMBACKTIME_SITE_URL", "SITE_URL"} {
		if value := os.Getenv(key); value != "" {
			return strings.TrimRight(value, "/")
		}
	}
	return EmbeddedSiteURL
}

// ValidateSiteURL accepts HTTPS origins and explicit local development origins
func ValidateSiteURL(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("set SITE_URL or pass --endpoint with your website origin")
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"
	if (u.Scheme != "https" && !(u.Scheme == "http" && local)) || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("website URL must be an HTTPS origin or localhost")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

type Config map[string]json.RawMessage

func DefaultPath() (string, error) {
	h, err := DataDir("")
	return filepath.Join(h, "termbacktime.json"), err
}

func Load(path string) (Config, error) {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, fmt.Errorf("invalid configuration file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := Config{}
	if err = json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if c == nil {
		c = Config{}
	}
	return c, nil
}

func (c Config) String(key string) string {
	var value string
	_ = json.Unmarshal(c[key], &value)
	return value
}

func (c Config) Set(key, value string) { c[key], _ = json.Marshal(value) }

// Save atomically replaces credentials with a private file and preserves unknown keys
func (c Config) Save(path string) error {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("config destination is not a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".termbacktime-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
