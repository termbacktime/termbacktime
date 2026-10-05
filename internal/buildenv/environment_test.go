package buildenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndependentOriginsAndValidation(t *testing.T) {
	for _, test := range []struct {
		name, environment, value, want string
		bad                            bool
	}{
		{"production", "prod", "https://app.example/", "https://app.example", false},
		{"development", "dev", "https://dev.example/", "https://dev.example", false},
		{"missing", "dev", "", "", false},
		{"invalid environment", "typo", "", "", true},
		{"remote http", "prod", "http://remote.example", "", true},
		{"credentials", "dev", "https://user:pass@example.com", "", true},
		{"path", "prod", "https://example.com/path", "", true},
		{"fragment", "dev", "https://example.com/#key", "", true},
		{"query", "prod", "https://example.com/?key=x", "", true},
		{"localhost", "dev", "http://localhost:8787", "http://localhost:8787", false},
		{"loopback", "prod", "http://127.0.0.1:8787", "http://127.0.0.1:8787", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			values := map[string]string{"SITE_URL": "https://production.example"}
			key := "SITE_URL"
			if test.environment == "dev" {
				key += "_DEV"
			}
			values[key] = test.value
			got, err := SiteURL(t.TempDir(), test.environment, func(key string) (string, bool) { v, ok := values[key]; return v, ok })
			if (err != nil) != test.bad || got != test.want {
				t.Fatalf("got %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestDotenvAndShellPrecedence(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".env")
	data := "# comment\nexport SITE_URL = 'https://file.example/' # trailing\nSITE_URL_DEV=\"http://localhost:8787\"\nOTHER=\x60multi\nline\x60\nUNQUOTED= value # comment\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	values, err := readSettings(path)
	if err != nil || values["OTHER"] != "multi\nline" || values["UNQUOTED"] != "value" {
		t.Fatal(values, err)
	}
	for _, test := range []struct {
		present     bool
		value, want string
	}{
		{false, "", "https://file.example"},
		{true, " https://shell.example/ ", "https://shell.example"},
		{true, "", ""},
	} {
		got, err := SiteURL(root, "prod", func(string) (string, bool) { return test.value, test.present })
		if err != nil || got != test.want {
			t.Fatal(got, err)
		}
	}
	got, err := SiteURL(root, "dev", func(string) (string, bool) { return "", false })
	if err != nil || got != "http://localhost:8787" {
		t.Fatal(got, err)
	}
	if err := os.WriteFile(path, []byte("SITE_URL='unterminated\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSettings(path); err == nil {
		t.Fatal("accepted unterminated quote")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 1<<20+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSettings(path); err == nil {
		t.Fatal("accepted oversized line")
	}
	if _, err := readSettings(root); err == nil {
		t.Fatal("accepted directory as dotenv")
	}
}
