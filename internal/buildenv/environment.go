// Package buildenv selects build origins independently of the runtime environment.
package buildenv

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/termbacktime/termbacktime/internal/config"
)

// SiteURL reads the optional .env file without overriding shell values.
// Missing settings are allowed for release packaging, but not for builds.
func SiteURL(root, environment string, lookup func(string) (string, bool)) (string, error) {
	field := "SITE_URL"
	switch environment {
	case "dev":
		field += "_DEV"
	case "prod":
	default:
		return "", fmt.Errorf("choose dev or prod")
	}
	settings, err := readSettings(filepath.Join(root, ".env"))
	if err != nil {
		return "", err
	}
	value, present := lookup(field)
	if !present {
		value = settings[field]
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	url, err := config.ValidateSiteURL(value)
	if err != nil {
		return "", fmt.Errorf("%s must be an HTTPS origin or localhost: %w", field, err)
	}
	return url, nil
}

func readSettings(path string) (map[string]string, error) {
	values := make(map[string]string)
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" {
			continue
		}
		if len(value) > 0 && (value[0] == '\'' || value[0] == '"' || value[0] == '`') {
			quote := value[0]
			value = value[1:]
			for !strings.ContainsRune(value, rune(quote)) && scanner.Scan() {
				value += "\n" + scanner.Text()
			}
			end := strings.IndexByte(value, quote)
			if end < 0 {
				return nil, fmt.Errorf("unterminated quoted .env value for %s", key)
			}
			value = value[:end]
		} else {
			value, _, _ = strings.Cut(value, "#")
			value = strings.TrimSpace(value)
		}
		values[key] = value
	}
	return values, scanner.Err()
}
