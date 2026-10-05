package updates

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPaginationAndDailyCache(t *testing.T) {
	var calls atomic.Int32
	var endpoint string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "" || r.URL.Query().Get("per_page") != "1" {
			t.Error("credentials sent or page size not bounded")
		}
		if r.URL.Query().Get("page") == "1" {
			w.Header().Set("Link", fmt.Sprintf(`<%s?page=2&per_page=1>; rel="next"`, endpoint))
			fmt.Fprint(w, `[{"tag_name":"not-a-version","draft":false}]`)
		} else {
			fmt.Fprint(w, `[{"tag_name":"v1.0.0-rc.10","draft":false,"prerelease":true}]`)
		}
	}))
	defer server.Close()
	endpoint = server.URL
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	c := &Checker{HTTP: server.Client(), Endpoint: endpoint, Now: func() time.Time { return now }}
	dir := filepath.Join(t.TempDir(), "data")
	check := func(force bool, wantCalls int32, cached bool) {
		t.Helper()
		result, err := c.Check(context.Background(), dir, force)
		if err != nil || result.Tag != "v1.0.0-rc.10" || result.Cached != cached || calls.Load() != wantCalls {
			t.Fatalf("result=%+v error=%v calls=%d", result, err, calls.Load())
		}
	}
	check(false, 2, false)
	now = now.Add(23 * time.Hour)
	check(false, 2, true)
	check(true, 4, false)
	now = now.Add(24 * time.Hour)
	check(false, 6, false)
	for _, name := range []string{"update-check.json", "update-check.lock"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("cache permissions: %v %v", info, err)
		}
	}
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0700 {
		t.Fatal("data directory is not private")
	}
}

func TestFailuresAndEmptyResultsAreCached(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"rate limit", `{}`, 403}, {"unavailable", `{}`, 503}, {"empty", `[]`, 200},
		{"object", `{}`, 200}, {"null", `null`, 200}, {"invalid", `not JSON`, 200},
		{"oversized", strings.Repeat(" ", maxPageBytes+1), 200},
		{"draft", `[{"tag_name":"v2.0.0","draft":true}]`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer s.Close()
			c := &Checker{HTTP: s.Client(), Endpoint: s.URL, Now: time.Now}
			dir := t.TempDir()
			for i := 0; i < 2; i++ {
				if _, err := c.Check(context.Background(), dir, false); err == nil {
					t.Fatal("expected failure")
				}
			}
			if calls.Load() != 1 {
				t.Fatal("failure was not throttled")
			}
			if _, err := c.Check(context.Background(), dir, true); err == nil || calls.Load() != 2 {
				t.Fatal("force did not retry")
			}
		})
	}
}

func TestConcurrentChecksAndCancellation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		fmt.Fprint(w, `[{"tag_name":"v1.0.0"}]`)
	}))
	defer s.Close()
	c := &Checker{HTTP: s.Client(), Endpoint: s.URL, Now: time.Now}
	dir := t.TempDir()
	done := make(chan error, 1)
	go func() { _, err := c.Check(context.Background(), dir, false); done <- err }()
	<-entered
	_, err := c.Check(context.Background(), dir, false)
	close(release)
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("concurrent check: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Check(ctx, t.TempDir(), true); err == nil {
		t.Fatal("cancelled check succeeded")
	}
}

func TestPageLimitAndUntrustedLinks(t *testing.T) {
	var calls atomic.Int32
	var endpoint string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := calls.Add(1)
		w.Header().Set("Link", fmt.Sprintf(`<%s?per_page=1&page=%d>; rel="next"`, endpoint, page+1))
		fmt.Fprint(w, `[{"tag_name":"invalid"}]`)
	}))
	defer s.Close()
	endpoint = s.URL
	c := &Checker{HTTP: s.Client(), Endpoint: s.URL, Now: time.Now}
	if _, err := c.Check(context.Background(), t.TempDir(), false); err == nil || calls.Load() != maxPages {
		t.Fatalf("page limit error=%v calls=%d", err, calls.Load())
	}
	for _, link := range []string{
		`<https://other.example/?per_page=1&page=2>; rel="next"`,
		`<` + endpoint + `?per_page=100&page=2>; rel="next"`,
		`<` + endpoint + `?per_page=1&page=1>; rel="next"`,
	} {
		if hasNextPage(link, endpoint, 2) {
			t.Fatal("unsafe link accepted")
		}
	}
}

func TestCorruptAndFutureCache(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `[{"tag_name":"v1.0.0"}]`) }))
	defer s.Close()
	c := &Checker{HTTP: s.Client(), Endpoint: s.URL, Now: time.Now}
	for _, value := range []string{`broken`, `{"version":1,"checked_at":"2999-01-01T00:00:00Z","tag":"v1.0.0"}`} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "update-check.json"), []byte(value), 0600)
		result, err := c.Check(context.Background(), dir, false)
		if err != nil || result.Cached {
			t.Fatalf("%+v %v", result, err)
		}
	}
}

func TestVersionPrecedence(t *testing.T) {
	for _, pair := range [][2]string{{"v1.0.0-rc.10", "v1.0.0-rc.9"}, {"v1.0.0", "v1.0.0-rc.10"}, {"v1.10.0", "v1.9.0"}, {"v1.0.0-rc.1", "v1.0.0-beta.1"}} {
		if !Newer(pair[0], pair[1]) || Newer(pair[1], pair[0]) {
			t.Fatal(pair)
		}
	}
	if Newer("v1.0.0+new", "v1.0.0+old") {
		t.Fatal("build metadata changes precedence")
	}
	for _, tag := range []string{"dev", "v1.0", "v01.0.0", "v1.0.0-rc.01", "v1.0.0-", "v1.0.0\n"} {
		if ValidVersion(tag) {
			t.Fatal(tag)
		}
	}
}
