package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/termbacktime/termbacktime/internal/config"
)

func TestGistAndRepositoryDeviceAuthorizationOutcomes(t *testing.T) {
	for _, repository := range []bool{false, true} {
		for _, scenario := range []string{"success", "denied", "expired", "empty", "malformed", "network", "cancel"} {
			t.Run(strings.Join([]string{map[bool]string{false: "gist", true: "repo"}[repository], scenario}, "/"), func(t *testing.T) {
				client := New("")
				var intervals []time.Duration
				client.pollWait = func(ctx context.Context, d time.Duration) error {
					intervals = append(intervals, d)
					if scenario == "cancel" {
						return context.Canceled
					}
					return ctx.Err()
				}
				polls, displays := 0, 0
				client.HTTP.Transport = uploadTransport(func(req *http.Request) (*http.Response, error) {
					var payload map[string]string
					if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if req.URL.Path == "/login/device/code" {
						if payload["client_id"] != "client" || (payload["scope"] == "gist") == repository {
							t.Fatal(payload)
						}
						return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"device_code":"private-code","user_code":"ABCD","verification_uri":"https://github.com/login/device","expires_in":60,"interval":1}`))}, nil
					}
					if payload["device_code"] != "private-code" || payload["grant_type"] != "urn:ietf:params:oauth:grant-type:device_code" {
						t.Fatal(payload)
					}
					polls++
					body := `{"error":"authorization_pending"}`
					if polls == 2 {
						body = `{"error":"slow_down"}`
					}
					if polls == 3 {
						switch scenario {
						case "success":
							body = `{"access_token":"access","refresh_token":"refresh","expires_in":3600,"refresh_token_expires_in":7200}`
						case "denied":
							body = `{"error":"access_denied"}`
						case "expired":
							body = `{"error":"expired_token"}`
						case "empty":
							body = `{}`
						case "malformed":
							body = "not JSON"
						case "network":
							return nil, errors.New("offline")
						}
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
				})
				display := func(uri, code string) {
					displays++
					if uri != "https://github.com/login/device" || code != "ABCD" {
						t.Fatal(uri, code)
					}
				}
				var err error
				if repository {
					var result RepoCredential
					result, err = client.AuthorizeRepo(t.Context(), RepoApp{ID: 1, ClientID: "client", Slug: "app"}, display)
					if scenario == "success" && (result.AccessToken != "access" || result.RefreshToken != "refresh" || result.ExpiresAt <= time.Now().Unix()) {
						t.Fatal(result)
					}
				} else {
					var token string
					token, err = client.Authorize(t.Context(), "client", display)
					if scenario == "success" && token != "access" {
						t.Fatal(token)
					}
				}
				if (err != nil) != (scenario != "success") || displays != 1 {
					t.Fatal(err, displays)
				}
				want := []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second}
				if scenario == "cancel" {
					want = want[:1]
					if polls != 0 || !errors.Is(err, context.Canceled) {
						t.Fatal(err, polls)
					}
				}
				if !reflect.DeepEqual(intervals, want) {
					t.Fatal(intervals)
				}
			})
		}
	}
}

func TestDeviceAuthorizationValidationAndCancellation(t *testing.T) {
	client := New("")
	if _, err := client.Authorize(t.Context(), "", func(string, string) {}); err == nil {
		t.Fatal("missing client accepted")
	}
	if _, err := client.AuthorizeRepo(t.Context(), RepoApp{}, func(string, string) {}); err == nil {
		t.Fatal("missing app accepted")
	}
	for _, body := range []string{"broken", `{}`, `{"device_code":"x","verification_uri":"https://evil.example","expires_in":1}`} {
		client.HTTP.Transport = uploadTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		if _, err := client.Authorize(t.Context(), "client", func(string, string) { t.Fatal("displayed invalid URI") }); err == nil {
			t.Fatal(body)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := client.waitForPoll(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := client.waitForPoll(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryLogoutAndExpiredCredentialsPreserveGist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Config{}
	cfg.Set("token", "gist-token")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	app := RepoApp{ID: 1, ClientID: "client", Slug: "app"}
	for _, credential := range []RepoCredential{
		{App: app, AccessToken: "expired", ExpiresAt: 1},
		{App: app, AccessToken: "expired", RefreshToken: "refresh", ExpiresAt: 1, RefreshExpiresAt: 1},
	} {
		if err := SaveRepoCredential(path, credential); err != nil {
			t.Fatal(err)
		}
		if _, err := New("").RepoCredentials(t.Context(), path); err == nil {
			t.Fatal("expired credential accepted")
		}
	}
	if err := RemoveRepositoryLogin(path); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil || cfg.String("token") != "gist-token" || cfg["repo_auth"] != nil {
		t.Fatal(cfg, err)
	}
	if !strings.Contains(app.InstallURL(42, 7), "repository_ids%5B%5D=7") {
		t.Fatal(app.InstallURL(42, 7))
	}
}
