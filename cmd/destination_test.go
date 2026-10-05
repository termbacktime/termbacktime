package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/termbacktime/termbacktime/internal/config"
	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/review"
	"net/http"
	"path/filepath"
	"testing"
)

func TestUploadDestinationDefaultsAndConfirmedPreference(t *testing.T) {
	o := &options{configPath: filepath.Join(t.TempDir(), "config.json")}
	if got, err := o.uploadStorage(""); err != nil || got != "repo" {
		t.Fatal(got, err)
	}
	r := &recording.Recording{Sizes: []int{80, 24}, Metadata: &recording.Metadata{Version: 1}}
	if _, err := o.applyMetadataReview(r, review.Result{Storage: "gist", RememberStorage: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := o.uploadStorage(""); got != "gist" {
		t.Fatal(got)
	}
	if got, _ := o.uploadStorage("repo"); got != "repo" {
		t.Fatal(got)
	}
	cfg, _ := config.Load(o.configPath)
	if cfg.String("upload_storage") != "gist" {
		t.Fatal("override changed preference")
	}
	if _, err := o.uploadStorage("any-repo"); err == nil {
		t.Fatal("accepted arbitrary destination")
	}
}
func TestExplicitSecretRepoFailsWithoutRememberingChoice(t *testing.T) {
	c := newUpload(&options{})
	if err := c.Flags().Set("public", "false"); err != nil {
		t.Fatal(err)
	}
	if err := validateRepoVisibility(c, "repo"); err == nil {
		t.Fatal("accepted secret repo")
	}
	if err := validateRepoVisibility(c, "gist"); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryTokenOverrideDiscoveryNeverPersistsToken(t *testing.T) {
	o := &options{configPath: filepath.Join(t.TempDir(), "config.json"), token: "runtime-app-token", endpoint: "https://play.example"}
	cfg := config.Config{}
	cfg.Set("token", "saved-gist-token")
	if err := cfg.Save(o.configPath); err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = authTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/config" {
			return jsonResponse(200, `{"githubRepoApp":{"id":3,"clientId":"client","slug":"app"}}`), nil
		}
		if r.URL.Path != "/user" || r.Header.Get("Authorization") != "Bearer runtime-app-token" {
			t.Fatal("unexpected request", r)
		}
		return jsonResponse(401, `{}`), nil
	})
	c := gh.New("")
	o.configureBackends(c)
	if _, err := c.Backend(t.Context(), "repo"); err == nil {
		t.Fatal("accepted invalid override")
	}
	cfg, _ = config.Load(o.configPath)
	if cfg.String("token") != "saved-gist-token" || len(cfg["repo_auth"]) != 0 {
		t.Fatal("persisted runtime credential")
	}
	if len(cfg["github_repo_app"]) == 0 {
		t.Fatal("did not cache public identifiers")
	}
}

type cancelDevicePrompt struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelDevicePrompt) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	if strings.Contains(w.String(), "enter code") {
		w.cancel()
	}
	return n, err
}
func TestRepositoryAuthRefreshesRejectedClientOnce(t *testing.T) {
	for _, tc := range []struct {
		name, fresh                         string
		refresh, failDiscovery, rejectFresh bool
		wantDevices                         int
	}{
		{name: "stale cached app", fresh: "new-client", wantDevices: 2},
		{name: "unchanged config", fresh: "old-client", wantDevices: 1},
		{name: "failed discovery", fresh: "new-client", failDiscovery: true, wantDevices: 1},
		{name: "explicit refresh", fresh: "new-client", refresh: true, wantDevices: 1},
		{name: "replacement also rejected", fresh: "new-client", rejectFresh: true, wantDevices: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := &options{configPath: filepath.Join(t.TempDir(), "config.json"), endpoint: "https://play.example"}
			cfg := config.Config{}
			cfg.Set("token", "separate-gist-token")
			cfg["github_repo_app"], _ = json.Marshal(gh.RepoApp{ID: 1, ClientID: "old-client", Slug: "app"})
			if err := cfg.Save(o.configPath); err != nil {
				t.Fatal(err)
			}
			previous := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = previous })
			devices, discoveries := 0, 0
			http.DefaultTransport = authTransport(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/api/v1/config":
					discoveries++
					if r.Header.Get("Cache-Control") != "no-cache" {
						t.Fatal("refresh allowed stale response")
					}
					if tc.failDiscovery {
						return jsonResponse(500, `{}`), nil
					}
					body, _ := json.Marshal(map[string]any{"githubRepoApp": gh.RepoApp{ID: 2, ClientID: tc.fresh, Slug: "app"}})
					return jsonResponse(200, string(body)), nil
				case "/login/device/code":
					devices++
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if body["client_id"] == "old-client" || tc.rejectFresh {
						return jsonResponse(404, `{}`), nil
					}
					if body["client_id"] != tc.fresh {
						t.Fatal(body)
					}
					return jsonResponse(200, `{"device_code":"private-device-code","user_code":"USER-CODE","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`), nil
				default:
					t.Fatalf("unexpected authentication request %s", r.URL)
					return nil, nil
				}
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			out := &cancelDevicePrompt{cancel: cancel}
			c := newAuth(o)
			c.SetContext(ctx)
			c.SetOut(out)
			c.SetErr(&bytes.Buffer{})
			args := []string{"--storage", "repo"}
			if tc.refresh {
				args = append(args, "--refresh-client-id")
			}
			c.SetArgs(args)
			err := c.Execute()
			if tc.fresh != "old-client" && !tc.failDiscovery && !tc.rejectFresh {
				if !errors.Is(err, context.Canceled) || !strings.Contains(out.String(), "enter code") {
					t.Fatal("did not reach device prompt", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "Client ID is unavailable") {
				t.Fatal("missing actionable error", err)
			}
			if devices != tc.wantDevices || discoveries != 1 {
				t.Fatal("unexpected retries", devices, discoveries)
			}
			saved, _ := config.Load(o.configPath)
			if saved.String("token") != "separate-gist-token" || len(saved["repo_auth"]) != 0 {
				t.Fatal("changed credentials")
			}
			var app gh.RepoApp
			json.Unmarshal(saved["github_repo_app"], &app)
			want := tc.fresh
			if tc.failDiscovery {
				want = "old-client"
			}
			if app.ClientID != want {
				t.Fatal("wrong cached identifier", app)
			}
		})
	}
}
