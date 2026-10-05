package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/sealed"
)

func TestUploadVisibilityEncryptionAndMetadata(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		for _, public := range []bool{false, true} {
			t.Run(fmtBool(encrypted)+"-"+fmtBool(public), func(t *testing.T) {
				dir := t.TempDir()
				t.Setenv("HOME", dir)
				t.Setenv("TERMBACKTIME_DATA_DIR", filepath.Join(dir, "data"))
				t.Setenv("TERMBACKTIME_TOKEN", "test-token")
				path := filepath.Join(dir, "original.json")
				original := &recording.Recording{ID: recording.NewID(), Title: "original", Info: recording.CurrentInfo(), Sizes: []int{80, 24}, Metadata: &recording.Metadata{Version: 1, CaptureSystem: &recording.SystemInfo{CPUModel: "PRIVATE CAPTURE CPU", RAMBytes: 32 << 30}}, Lines: []recording.Event{{Lines: []string{"private terminal output"}}}}
				if err := recording.Save(path, original); err != nil {
					t.Fatal(err)
				}
				before, _ := os.ReadFile(path)
				old := http.DefaultTransport
				t.Cleanup(func() { http.DefaultTransport = old })
				var payload struct {
					Public bool `json:"public"`
					Files  map[string]struct {
						Content string `json:"content"`
					} `json:"files"`
				}
				calls := 0
				var readme string
				http.DefaultTransport = recordTransport(func(req *http.Request) (*http.Response, error) {
					if req.Method == http.MethodPatch {
						var patch struct {
							Files map[string]struct{ Content string }
						}
						if err := json.NewDecoder(req.Body).Decode(&patch); err != nil {
							t.Fatal(err)
						}
						if len(patch.Files) != 1 {
							t.Fatal("expected one combined README", patch.Files)
						}
						readme = patch.Files["README.md"].Content
						return jsonResponse(200, `{}`), nil
					}
					body := `{"recordingFormats":["tbt-recording-v1","tbt-encrypted-v1"]}`
					if req.URL.Host == "api.github.com" {
						calls++
						if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
							t.Fatal(err)
						}
						body = `{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
				})
				root := NewRoot()
				var out bytes.Buffer
				root.SetOut(&out)
				root.SetErr(io.Discard)
				args := []string{"--endpoint", "https://site.example", "upload", "--storage", "gist", path, "--description", "Approved description", "--title", "Approved title"}
				if encrypted {
					args = append(args, "--encrypt")
				}
				if public {
					args = append(args, "--public")
				}
				root.SetArgs(args)
				if err := root.ExecuteContext(t.Context()); err != nil {
					t.Fatal(err)
				}
				if calls != 1 || payload.Public != public {
					t.Fatal("wrong Gist visibility or request count")
				}
				markdown := payload.Files["README.md"].Content
				if _, exists := payload.Files["metadata.md"]; exists {
					t.Fatal("separate metadata companion uploaded")
				}
				if !strings.Contains(markdown, "Approved description") || !strings.Contains(markdown, "Approved title") {
					t.Fatal("missing companion")
				}
				if !strings.HasPrefix(readme, "# Approved title\n\n[Play recording](https://site.example/p/") || !strings.Contains(readme, "Approved description") || !strings.Contains(readme, "## Recording") {
					t.Fatal("README update lost metadata or playback", readme)
				}
				var data []byte
				if encrypted {
					var m sealed.Manifest
					if err := json.Unmarshal([]byte(payload.Files[sealed.Filename].Content), &m); err != nil {
						t.Fatal(err)
					}
					link, _ := url.Parse(strings.TrimSpace(out.String()))
					fragment, _ := url.ParseQuery(link.Fragment)
					var err error
					data, err = sealed.Decrypt(t.Context(), m, fragment.Get("k"), func(name string) ([]byte, error) { return []byte(payload.Files[name].Content), nil })
					if err != nil {
						t.Fatal(err)
					}
					encoded, _ := json.Marshal(payload)
					if strings.Contains(string(encoded), "private terminal output") || strings.Contains(markdown, fragment.Get("k")) || strings.Contains(readme, fragment.Get("k")) {
						t.Fatal("encrypted data or key leaked")
					}
				} else {
					data = []byte(payload.Files["terminal-recording.tbt.json"].Content)
					if strings.Contains(out.String(), "#k=") {
						t.Fatal("plaintext link contains a key")
					}
				}
				decode := recording.ReadWrapped
				if encrypted {
					decode = recording.Decode
				}
				uploaded, err := decode(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				if uploaded.Title != "Approved title" || uploaded.Metadata.Description != "Approved description" || uploaded.Metadata.CaptureSystem != nil {
					t.Fatal("noninteractive upload attached hardware or lost metadata")
				}
				encoded, _ := json.Marshal(payload)
				if strings.Contains(string(encoded), "PRIVATE CAPTURE CPU") {
					t.Fatal("unapproved capture metadata leaked")
				}
				after, _ := os.ReadFile(path)
				if !bytes.Equal(before, after) {
					t.Fatal("source modified")
				}
			})
		}
	}
}
func fmtBool(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
func TestMetadataOmissionAndExplicitFile(t *testing.T) {
	dir := t.TempDir()
	o := &options{configPath: filepath.Join(dir, "config.json")}
	file := filepath.Join(dir, "metadata.json")
	os.WriteFile(file, []byte(`{"version":1,"title":"file title","description":"file description","upload_system":{"cpu_model":"Explicit CPU"}}`), 0600)
	for _, test := range []struct {
		name  string
		flags []string
		want  bool
	}{{"automatic", nil, false}, {"omitted", []string{"--no-metadata"}, false}, {"explicit", []string{"--metadata-file", file, "--description", "flag description"}, true}} {
		t.Run(test.name, func(t *testing.T) {
			r := &recording.Recording{Title: "recorded", Metadata: &recording.Metadata{Version: 1, CaptureSystem: &recording.SystemInfo{CPUModel: "capture"}}}
			c := newUpload(o)
			c.SetOut(io.Discard)
			if err := c.ParseFlags(test.flags); err != nil {
				t.Fatal(err)
			}
			u := uploadFlags{}
			u.noMetadata, _ = c.Flags().GetBool("no-metadata")
			u.description, _ = c.Flags().GetString("description")
			u.metadataFile, _ = c.Flags().GetString("metadata-file")
			if err := o.prepareMetadata(c, r, filepath.Join(dir, "recording.json"), "", u); err != nil {
				t.Fatal(err)
			}
			if (r.Metadata != nil) != test.want {
				t.Fatal("metadata omission policy")
			}
			if test.want && (r.Metadata.CaptureSystem != nil || r.Metadata.UploadSystem.CPUModel != "Explicit CPU" || r.Metadata.Description != "flag description" || r.Title != "file title") {
				t.Fatalf("wrong precedence/provenance: %+v", r)
			}
		})
	}
}
func TestUploadFlagConflicts(t *testing.T) {
	for _, flags := range [][]string{{"--encrypt", "--no-encrypt"}, {"--no-metadata", "--description", "x"}, {"--no-metadata", "--metadata-file", "x"}} {
		u := uploadFlags{}
		c := newUpload(&options{})
		c.SetOut(io.Discard)
		c.SetErr(io.Discard)
		if err := c.ParseFlags(flags); err != nil {
			t.Fatal(err)
		}
		u.noMetadata, _ = c.Flags().GetBool("no-metadata")
		u.metadataFile, _ = c.Flags().GetString("metadata-file")
		if err := u.validate(c); err == nil {
			t.Fatal("accepted conflict", flags)
		}
	}
}

func TestMetadataFileRejectsUnknownCategories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	os.WriteFile(path, []byte(`{"version":1,"hostname":"not-allowed"}`), 0600)
	o := &options{configPath: filepath.Join(dir, "config.json")}
	c := newUpload(o)
	c.SetOut(io.Discard)
	c.ParseFlags([]string{"--metadata-file", path})
	r := &recording.Recording{}
	if o.prepareMetadata(c, r, path, "", uploadFlags{metadataFile: path}) == nil {
		t.Fatal("accepted unapproved metadata category")
	}
}
