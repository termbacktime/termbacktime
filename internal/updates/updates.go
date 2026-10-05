// Package updates discovers public releases without accessing credentials or recordings.
package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/termbacktime/termbacktime/internal/config"
	"golang.org/x/sys/unix"
)

const (
	Endpoint     = "https://api.github.com/repos/termbacktime/termbacktime/releases"
	ReleasesURL  = "https://github.com/termbacktime/termbacktime/releases"
	Interval     = 24 * time.Hour
	maxPages     = 20
	maxPageBytes = 1 << 20
)

type Checker struct {
	HTTP     *http.Client
	Endpoint string
	Now      func() time.Time
}

type Result struct {
	Tag    string
	Cached bool
}

type cache struct {
	Version   int       `json:"version"`
	CheckedAt time.Time `json:"checked_at"`
	Tag       string    `json:"tag,omitempty"`
	Error     string    `json:"error,omitempty"`
}

func New() *Checker {
	return &Checker{HTTP: &http.Client{Timeout: 5 * time.Second}, Endpoint: Endpoint, Now: time.Now}
}

// Check serializes local checks and caches attempts, including failures, for a day.
func (c *Checker) Check(ctx context.Context, directory string, force bool) (Result, error) {
	if err := config.EnsurePrivateDir(directory); err != nil {
		return Result{}, err
	}
	fd, err := unix.Open(filepath.Join(directory, "update-check.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return Result{}, err
	}
	lock := os.NewFile(uintptr(fd), "update-check.lock")
	defer lock.Close()
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return Result{}, errors.New("another update check is already running")
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	if err := lock.Chmod(0600); err != nil {
		return Result{}, err
	}
	path := filepath.Join(directory, "update-check.json")
	previous := readCache(path)
	now := c.Now()
	age := now.Sub(previous.CheckedAt)
	if !force && previous.Version == 1 && age >= 0 && age < Interval {
		result := Result{Tag: previous.Tag, Cached: true}
		if previous.Error != "" {
			return result, errors.New(previous.Error)
		}
		return result, nil
	}
	// Persist the attempt before networking so a killed process does not cause a retry storm.
	next := cache{Version: 1, CheckedAt: now, Tag: previous.Tag, Error: "update check did not complete"}
	if err := writeCache(path, next); err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tag, checkErr := c.latest(ctx)
	if checkErr != nil {
		next.Error = checkErr.Error()
	} else {
		next.Tag, next.Error = tag, ""
	}
	if err := writeCache(path, next); err != nil {
		return Result{Tag: tag}, fmt.Errorf("save update check: %w", err)
	}
	return Result{Tag: tag}, checkErr
}

func (c *Checker) latest(ctx context.Context) (string, error) {
	for page := 1; page <= maxPages; page++ {
		endpoint := fmt.Sprintf("%s?per_page=1&page=%d", c.Endpoint, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return "", errors.New("invalid release endpoint")
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
		req.Header.Set("User-Agent", "termbacktime-update-check")
		res, err := c.HTTP.Do(req)
		if err != nil {
			return "", errors.New("GitHub release check failed; check your connection and retry with --check-update")
		}
		data, readErr := io.ReadAll(io.LimitReader(res.Body, maxPageBytes+1))
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return "", fmt.Errorf("GitHub release check returned HTTP %d; retry later with --check-update", res.StatusCode)
		}
		if readErr != nil || len(data) > maxPageBytes {
			return "", errors.New("GitHub release response exceeded the 1 MiB limit or could not be read")
		}
		var releases []struct {
			Tag   string `json:"tag_name"`
			Draft bool   `json:"draft"`
		}
		if err := json.Unmarshal(data, &releases); err != nil || releases == nil || len(releases) > 1 {
			return "", errors.New("invalid GitHub release response")
		}
		for _, release := range releases {
			if !release.Draft && ValidVersion(release.Tag) {
				return release.Tag, nil
			}
		}
		if len(releases) == 0 || !hasNextPage(res.Header.Get("Link"), c.Endpoint, page+1) {
			return "", errors.New("no published semantic-version release found")
		}
	}
	return "", errors.New("no supported release found within 20 pages")
}

var nextRelation = regexp.MustCompile(`(?:^|;)\s*rel\s*=\s*"[^"]*\bnext\b[^"]*"`)

// Only accept the next numbered page of this endpoint, never arbitrary Link URLs.
func hasNextPage(header, endpoint string, page int) bool {
	for _, link := range strings.Split(header, ",") {
		parts := strings.SplitN(strings.TrimSpace(link), ">", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "<") || !nextRelation.MatchString(parts[1]) {
			continue
		}
		u, err := url.Parse(strings.TrimPrefix(parts[0], "<"))
		if err != nil {
			continue
		}
		query := u.Query()
		u.RawQuery = ""
		if u.String() == endpoint && query.Get("page") == strconv.Itoa(page) && query.Get("per_page") == "1" {
			return true
		}
	}
	return false
}

func readCache(path string) cache {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return cache{}
	}
	b, err := os.ReadFile(path)
	var result cache
	if err != nil || json.Unmarshal(b, &result) != nil || (result.Tag != "" && !ValidVersion(result.Tag)) {
		return cache{}
	}
	return result
}

func writeCache(path string, value cache) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".update-check-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := json.NewEncoder(f).Encode(value); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
