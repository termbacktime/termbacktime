package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/termbacktime/termbacktime/internal/config"
)

type authTransport func(*http.Request) (*http.Response, error)

func (transport authTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestGitHubClientIDCacheRefreshAndRemoval(t *testing.T) {
	t.Setenv("TERMBACKTIME_GITHUB_CLIENT_ID", "")
	previousTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	status, body := http.StatusOK, `{"githubClientId":"first-app"}`
	requests := 0
	http.DefaultTransport = authTransport(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.String() != "https://worker.example/api/v1/config" {
			t.Fatalf("unexpected discovery URL: %s", request.URL)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	path := filepath.Join(t.TempDir(), ".termbacktime.json")
	initial := config.Config{}
	initial.Set("token", "existing-token-fixture")
	initial.Set("legacy-setting", "preserved")
	if err := initial.Save(path); err != nil {
		t.Fatal(err)
	}
	fetch := func(refresh bool) (string, error) {
		cfg, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		o := options{configPath: path, endpoint: "https://worker.example"}
		return o.githubClientID(context.Background(), cfg, refresh)
	}
	check := func(refresh bool, want string, count int) {
		t.Helper()
		id, err := fetch(refresh)
		if err != nil || id != want || requests != count {
			t.Fatalf("ID=%q, error=%v, lookups=%d; want %q and %d lookups", id, err, requests, want, count)
		}
	}
	check(false, "first-app", 1)
	body = `{"githubClientId":"updated-app"}`
	check(false, "first-app", 1)
	check(true, "updated-app", 2)

	// Failed refreshes preserve the last valid ID and all existing configuration
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []struct {
		status int
		body   string
	}{
		{http.StatusBadGateway, `{}`},
		{http.StatusOK, `not JSON`},
		{http.StatusOK, `{"githubClientId":""}`},
		{http.StatusOK, `{"githubClientId":"invalid app"}`},
		{http.StatusOK, `{"githubClientId":"` + strings.Repeat("a", 129) + `"}`},
		{http.StatusOK, strings.Repeat("x", 4097)},
	} {
		status, body = failure.status, failure.body
		if _, err := fetch(true); err == nil {
			t.Fatal("accepted invalid discovery response")
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("failed refresh changed the saved configuration", err)
		}
	}
	cfg, err := config.Load(path)
	if err != nil || cfg.String("token") != "existing-token-fixture" || cfg.String("legacy-setting") != "preserved" {
		t.Fatal("cache update lost existing configuration", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("configuration permissions are not private", err)
	}
	status, body = http.StatusOK, `{"githubClientId":"after-removal"}`
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	check(false, "after-removal", requests+1)
}

// Retry failed authorization across commands to verify durable caching and explicit refresh
func TestAuthReusesCacheUntilExplicitRefresh(t *testing.T) {
	t.Setenv("TERMBACKTIME_GITHUB_CLIENT_ID", "")
	t.Setenv("TERMBACKTIME_TOKEN", "runtime-token-fixture")
	previousTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	path := filepath.Join(t.TempDir(), ".termbacktime.json")
	workerID, expectedID := "initial-app", "initial-app"
	discoveryCalls, deviceCalls := 0, 0
	expectRefresh := false
	http.DefaultTransport = authTransport(func(request *http.Request) (*http.Response, error) {
		response := &http.Response{Header: make(http.Header)}
		switch request.URL.String() {
		case "https://worker.example/api/v1/config":
			discoveryCalls++
			if (request.Header.Get("Cache-Control") == "no-cache") != expectRefresh {
				t.Fatal("explicit refresh did not request fresh Worker configuration")
			}
			response.StatusCode = http.StatusOK
			response.Body = io.NopCloser(strings.NewReader(`{"githubClientId":"` + workerID + `"}`))
		case "https://github.com/login/device/code":
			deviceCalls++
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["client_id"] != expectedID {
				t.Fatalf("device authorization used %q, want %q", body["client_id"], expectedID)
			}
			response.StatusCode = http.StatusBadRequest
			response.Body = io.NopCloser(strings.NewReader(`{}`))
		default:
			t.Fatalf("unexpected request: %s", request.URL)
		}
		return response, nil
	})
	run := func(refresh bool, expectedLookups, expectedDevices int) {
		t.Helper()
		expectRefresh = refresh
		args := []string{"--config", path, "--endpoint", "https://worker.example", "auth", "--storage", "gist"}
		if refresh {
			args = append(args, "--refresh-client-id")
		}
		root := NewRoot()
		root.SetArgs(args)
		if err := root.Execute(); err == nil || err.Error() != "GitHub returned HTTP 400" {
			t.Fatalf("expected intercepted authorization, got %v", err)
		}
		if discoveryCalls != expectedLookups || deviceCalls != expectedDevices {
			t.Fatalf("discovery/device calls: %d/%d, want %d/%d", discoveryCalls, deviceCalls, expectedLookups, expectedDevices)
		}
		cfg, err := config.Load(path)
		if err != nil || cfg.String("github_client_id") != expectedID {
			t.Fatal("client ID was not cached before the failed authorization", err)
		}
		if cfg.String("token") != "" {
			t.Fatal("discovery persisted a runtime token override")
		}
	}
	run(false, 1, 1)
	workerID = "updated-app"
	run(false, 1, 2)
	// Refresh selects the Worker app even when a runtime override is present
	t.Setenv("TERMBACKTIME_GITHUB_CLIENT_ID", "override-app")
	expectedID = workerID
	run(true, 2, 3)
	t.Setenv("TERMBACKTIME_GITHUB_CLIENT_ID", "")
	run(false, 2, 4)
}

func TestAuthTokenAndLogoutPreserveClientIDWithoutDiscovery(t *testing.T) {
	previousTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	http.DefaultTransport = authTransport(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("token operations must not contact %s", request.URL)
		return nil, nil
	})
	path := filepath.Join(t.TempDir(), ".termbacktime.json")
	cfg := config.Config{}
	cfg.Set("github_client_id", "cached-app")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--token-stdin", "--logout"} {
		root := NewRoot()
		root.SetArgs([]string{"--config", path, "auth", "--storage", "gist", flag})
		root.SetIn(strings.NewReader("token-fixture\n"))
		root.SetOut(io.Discard)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(path)
		if err != nil || cfg.String("github_client_id") != "cached-app" {
			t.Fatal("token operation changed cached client ID", err)
		}
		if (flag == "--logout" && cfg.String("token") != "") || (flag == "--token-stdin" && cfg.String("token") != "token-fixture") {
			t.Fatal("token operation did not update credentials")
		}
	}
}

// Intercept device-code requests to verify app selection without authorizing or saving credentials
func TestAuthDiscoversPublicClientIDFromWorker(t *testing.T) {
	previousTransport := http.DefaultTransport
	t.Cleanup(func() {
		http.DefaultTransport = previousTransport
	})
	for _, scenario := range []struct {
		name, runtime, worker, expected string
	}{
		{name: "Worker discovery", worker: "worker-app", expected: "worker-app"},
		{name: "updated Worker app", worker: "updated-worker-app", expected: "updated-worker-app"},
		{name: "unconfigured Worker"},
		{name: "explicit runtime override", runtime: "runtime-app", expected: "runtime-app"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Setenv("TERMBACKTIME_GITHUB_CLIENT_ID", scenario.runtime)
			t.Setenv("GITHUB_CLIENT_ID", "unrelated-shell-app")
			t.Setenv("TERMBACKTIME_TOKEN", "")
			var discoveryCalls, deviceCalls int
			http.DefaultTransport = authTransport(func(request *http.Request) (*http.Response, error) {
				response := &http.Response{Header: make(http.Header)}
				switch request.URL.String() {
				case "https://worker.example/api/v1/config":
					discoveryCalls++
					response.StatusCode = http.StatusOK
					response.Body = io.NopCloser(strings.NewReader(`{"githubClientId":"` + scenario.worker + `"}`))
				case "https://github.com/login/device/code":
					deviceCalls++
					var body map[string]string
					if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if body["client_id"] != scenario.expected || body["scope"] != "gist" {
						t.Fatalf("unexpected device request: %v", body)
					}
					response.StatusCode = http.StatusBadRequest
					response.Body = io.NopCloser(strings.NewReader(`{}`))
				default:
					t.Fatalf("unexpected request: %s", request.URL)
				}
				return response, nil
			})
			endpoint := ""
			if scenario.runtime == "" {
				endpoint = "https://worker.example"
			}
			root := NewRoot()
			root.SetArgs([]string{"--config", filepath.Join(t.TempDir(), "config.json"), "--endpoint", endpoint, "auth", "--storage", "gist"})
			expectedError := "GitHub returned HTTP 400"
			if scenario.expected == "" {
				expectedError = "GitHub device authorization is not configured; configure GITHUB_CLIENT_ID on the Worker or use auth --token-stdin"
			}
			if err := root.Execute(); err == nil || err.Error() != expectedError {
				t.Fatalf("expected the intercepted device request, got %v", err)
			}
			expectedDeviceCalls := 1
			if scenario.expected == "" {
				expectedDeviceCalls = 0
			}
			if deviceCalls != expectedDeviceCalls || (scenario.runtime != "" && discoveryCalls != 0) || (scenario.runtime == "" && discoveryCalls != 1) {
				t.Fatalf("unexpected discovery/device request counts: %d/%d", discoveryCalls, deviceCalls)
			}
		})
	}
}
