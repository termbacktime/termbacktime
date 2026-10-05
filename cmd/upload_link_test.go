package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/termbacktime/termbacktime/internal/config"
	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/recording"
)

func TestUploadReadmeUsesEffectiveCLIEndpoint(t *testing.T) {
	for _, scenario := range []struct{ name, embedded, site, override, flag, want string }{
		{name: "release default", embedded: "https://release.example", want: "https://release.example"},
		{name: "environment", embedded: "https://release.example", site: "https://stage.example/", want: "https://stage.example"},
		{name: "prefixed environment", site: "https://stage.example", override: "https://custom.example", want: "https://custom.example"},
		{name: "endpoint flag", site: "https://stage.example", override: "https://custom.example", flag: "http://localhost:8787/", want: "http://localhost:8787"},
		{name: "unconfigured"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			previous := config.EmbeddedSiteURL
			config.EmbeddedSiteURL = scenario.embedded
			t.Cleanup(func() { config.EmbeddedSiteURL = previous })
			t.Setenv("SITE_URL", scenario.site)
			t.Setenv("TERMBACKTIME_SITE_URL", scenario.override)
			t.Setenv("TERMBACKTIME_TOKEN", "test-token")
			t.Setenv("TERMBACKTIME_DATA_DIR", t.TempDir())
			path := filepath.Join(t.TempDir(), "recording.json")
			if err := recording.Save(path, &recording.Recording{Sizes: []int{80, 24}, Lines: []recording.Event{{Lines: []string{"hello"}}}}); err != nil {
				t.Fatal(err)
			}
			previousTransport := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = previousTransport })
			id := strings.Repeat("a", 32)
			creates, patches := 0, 0
			var readme string
			http.DefaultTransport = recordTransport(func(req *http.Request) (*http.Response, error) {
				switch req.Method {
				case http.MethodPost:
					creates++
					io.Copy(io.Discard, req.Body)
					return jsonResponse(201, `{"id":"`+id+`"}`), nil
				case http.MethodPatch:
					patches++
					var patch struct {
						Files map[string]struct{ Content string }
					}
					if err := json.NewDecoder(req.Body).Decode(&patch); err != nil {
						t.Fatal(err)
					}
					readme = patch.Files[gh.PlaybackFilename].Content
					return jsonResponse(200, `{}`), nil
				default:
					t.Fatal("unexpected request", req)
					return nil, nil
				}
			})
			root := NewRoot()
			var out, diagnostics bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&diagnostics)
			args := []string{"upload", "--storage", "gist", path, "--no-metadata"}
			if scenario.flag != "" {
				args = append(args, "--endpoint", scenario.flag)
			}
			root.SetArgs(args)
			if err := root.ExecuteContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			if creates != 1 || diagnostics.Len() != 0 {
				t.Fatal("upload failed", creates, diagnostics.String())
			}
			if scenario.want == "" {
				if patches != 0 || strings.TrimSpace(out.String()) != "https://gist.github.com/"+id {
					t.Fatal("unconfigured endpoint invented", out.String(), readme)
				}
			} else if patches != 1 || !strings.Contains(readme, "[Play recording]("+scenario.want+"/p/"+id+")") || strings.TrimSpace(out.String()) != scenario.want+"/p/"+id {
				t.Fatal("effective endpoint not used", out.String(), readme)
			}
		})
	}
}
