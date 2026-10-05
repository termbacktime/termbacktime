package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/termbacktime/termbacktime/internal/config"
	gh "github.com/termbacktime/termbacktime/internal/github"
)

func TestRepositoryAuthCommandSavesOnlyValidatedAccessAndKeepsGistLogin(t *testing.T) {
	for _, scenario := range []string{"success", "missing repository", "missing installation", "unauthorized"} {
		t.Run(scenario, func(t *testing.T) {
			h := newCommandHarness(t)
			cfg := config.Config{}
			cfg.Set("token", "independent-gist-token")
			cfg["github_repo_app"], _ = json.Marshal(gh.RepoApp{ID: 3, ClientID: "fixture-client", Slug: "fixture-app"})
			if err := cfg.Save(h.config); err != nil {
				t.Fatal(err)
			}
			previous := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = previous })
			repository := `{"id":21,"name":"TBT-Recordings","owner":{"id":7,"login":"fixture-user"},"default_branch":"main","private":false}`
			http.DefaultTransport = authTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "Bearer repository-app-token" {
					t.Error("wrong authorization", r.Header.Get("Authorization"))
				}
				switch r.URL.Path {
				case "/user":
					if scenario == "unauthorized" {
						return jsonResponse(401, `{}`), nil
					}
					return jsonResponse(200, `{"id":7,"login":"fixture-user","type":"User"}`), nil
				case "/repos/fixture-user/TBT-Recordings":
					if scenario == "missing repository" {
						return jsonResponse(404, `{}`), nil
					}
					return jsonResponse(200, repository), nil
				case "/user/installations":
					if scenario == "missing installation" {
						return jsonResponse(200, `{"total_count":0,"installations":[]}`), nil
					}
					return jsonResponse(200, `{"total_count":1,"installations":[{"id":2,"app_id":3,"account":{"id":7},"repository_selection":"selected","permissions":{"contents":"write","metadata":"read"}}]}`), nil
				case "/user/installations/2/repositories":
					return jsonResponse(200, `{"total_count":1,"repositories":[`+repository+`]}`), nil
				default:
					t.Error("unexpected request", r.URL)
					return nil, errors.New("unexpected request")
				}
			})
			out, diagnostic, err := h.run(t.Context(), "auth", "--storage", "repo", "--set-token", "repository-app-token")
			if strings.Contains(out+diagnostic, "repository-app-token") || strings.Contains(out+diagnostic, "independent-gist-token") {
				t.Fatal("auth exposed a credential")
			}
			saved, loadErr := config.Load(h.config)
			if loadErr != nil || saved.String("token") != "independent-gist-token" {
				t.Fatal(saved, loadErr)
			}
			if scenario == "success" {
				var credential gh.RepoCredential
				if err != nil || json.Unmarshal(saved["repo_auth"], &credential) != nil || credential.AccessToken != "repository-app-token" || credential.App.ID != 3 || !strings.Contains(out, "fixture-user/TBT-Recordings only") {
					t.Fatal(out, err, credential.App)
				}
			} else {
				if err == nil || len(saved["repo_auth"]) != 0 {
					t.Fatal(out, err)
				}
				if scenario == "missing repository" && !strings.Contains(out, "https://github.com/new?name=TBT-Recordings") {
					t.Fatal(out)
				}
				if scenario == "missing installation" && !strings.Contains(out, "Only select repositories") {
					t.Fatal(out)
				}
			}
		})
	}
}

func TestAuthCommandInvalidStdinDoesNotReplaceSavedCredentials(t *testing.T) {
	h := newCommandHarness(t)
	cfg := config.Config{}
	cfg.Set("token", "existing-gist-token")
	if err := cfg.Save(h.config); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(h.config)
	for _, input := range []string{"short", "valid-first-token\nsecond-token", strings.Repeat("x", 4097)} {
		c := h.command("auth", "--storage", "gist", "--token-stdin")
		c.SetIn(strings.NewReader(input))
		var out bytes.Buffer
		c.SetOut(&out)
		if err := c.ExecuteContext(t.Context()); err == nil || out.Len() != 0 {
			t.Fatal(out.String(), err)
		}
		after, _ := os.ReadFile(h.config)
		if !bytes.Equal(before, after) {
			t.Fatal("invalid input replaced saved credentials")
		}
	}
}
