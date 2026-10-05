package github

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/termbacktime/termbacktime/internal/config"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/sharing"
)

// A small Git object store makes publication/retry/delete tests observe real
// tree changes, instead of responding with unconditional upload success.
type repoFixture struct {
	t                        *testing.T
	repo                     RepoIdentity
	grant                    map[string]any
	selected                 []RepoIdentity
	head                     string
	trees                    map[string][]treeEntry
	commits                  map[string]string
	blobs                    map[string][]byte
	count, writes, conflicts int
	loseResponse, empty      bool
}

func repoJSON(status int, value any) (*http.Response, error) {
	b, _ := json.Marshal(value)
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
}
func newRepoFixture(t *testing.T) (*repoFixture, *Client) {
	f := &repoFixture{t: t, head: strings.Repeat("a", 40), trees: map[string][]treeEntry{}, commits: map[string]string{}, blobs: map[string][]byte{}}
	f.repo.ID = 21
	f.repo.Name = sharing.Repository
	f.repo.Branch = "main"
	f.repo.Owner.ID = 7
	f.repo.Owner.Login = "alice"
	f.selected = []RepoIdentity{f.repo}
	f.grant = map[string]any{"id": 2, "app_id": 3, "account": map[string]any{"id": 7}, "repository_selection": "selected", "permissions": map[string]string{"contents": "write", "metadata": "read"}}
	tree := strings.Repeat("b", 40)
	f.commits[f.head] = tree
	f.trees[tree] = []treeEntry{}
	c := New("app-token")
	c.AppID = 3
	c.Storage = sharing.Repo
	c.SiteURL = "https://play.example"
	c.HTTP.Transport = uploadTransport(f.roundTrip)
	return f, c
}
func (f *repoFixture) sha() string { f.count++; return fmt.Sprintf("%040x", f.count) }
func (f *repoFixture) roundTrip(r *http.Request) (*http.Response, error) {
	p := r.URL.Path
	if r.URL.Host == "raw.githubusercontent.com" {
		if r.Header.Get("Authorization") != "" {
			f.t.Fatal("credential leaked to raw host")
		}
		a := strings.Split(strings.TrimPrefix(p, "/"), "/")
		path := strings.Join(a[3:], "/")
		for _, e := range f.trees[f.commits[a[2]]] {
			if e.Path == path && e.SHA != nil {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(f.blobs[*e.SHA])))}, nil
			}
		}
		return repoJSON(404, nil)
	}
	switch p {
	case "/user":
		return repoJSON(200, map[string]any{"id": 7, "login": "alice", "type": "User"})
	case "/user/installations":
		return repoJSON(200, map[string]any{"total_count": 1, "installations": []any{f.grant}})
	case "/user/installations/2/repositories":
		return repoJSON(200, map[string]any{"total_count": len(f.selected), "repositories": f.selected})
	case "/repos/alice/TBT-Recordings":
		return repoJSON(200, f.repo)
	case "/api/v1/config":
		return repoJSON(200, map[string]any{"recordingSources": []string{"gist", "repo"}, "recordingFormats": []string{"tbt-recording-v1", "tbt-encrypted-v1"}})
	}
	p = strings.TrimPrefix(p, "/repos/alice/TBT-Recordings")
	if r.Method == "GET" {
		if p == "/git/ref/heads/main" {
			if f.empty {
				return repoJSON(409, nil)
			}
			return repoJSON(200, map[string]any{"object": map[string]string{"sha": f.head}})
		}
		if strings.HasPrefix(p, "/git/commits/") {
			return repoJSON(200, map[string]any{"tree": map[string]string{"sha": f.commits[strings.TrimPrefix(p, "/git/commits/")]}})
		}
		if strings.HasPrefix(p, "/git/trees/") {
			sha := strings.TrimPrefix(p, "/git/trees/")
			return repoJSON(200, repoTree{SHA: sha, Tree: f.trees[sha]})
		}
	}
	f.writes++
	if p == "/contents/README.md" && r.Method == "PUT" {
		f.empty = false
		return repoJSON(201, map[string]any{})
	}
	if p == "/git/blobs" {
		var b struct{ Content []byte }
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			f.t.Fatal(err)
		}
		h := sha1.Sum(b.Content)
		sha := hex.EncodeToString(h[:])
		f.blobs[sha] = b.Content
		return repoJSON(201, map[string]string{"sha": sha})
	}
	if p == "/git/trees" {
		var b struct {
			Base string `json:"base_tree"`
			Tree []treeEntry
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			f.t.Fatal(err)
		}
		m := map[string]treeEntry{}
		for _, e := range f.trees[b.Base] {
			m[e.Path] = e
		}
		for _, e := range b.Tree {
			if e.SHA == nil {
				delete(m, e.Path)
			} else {
				m[e.Path] = e
			}
		}
		entries := []treeEntry{}
		for _, e := range m {
			entries = append(entries, e)
		}
		sha := f.sha()
		f.trees[sha] = entries
		return repoJSON(201, map[string]string{"sha": sha})
	}
	if p == "/git/commits" {
		var b struct {
			Tree    string
			Parents []string
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			f.t.Fatal(err)
		}
		if len(b.Parents) != 1 || b.Parents[0] != f.head {
			f.t.Fatal("wrong commit parent", b)
		}
		sha := f.sha()
		f.commits[sha] = b.Tree
		return repoJSON(201, map[string]string{"sha": sha})
	}
	if p == "/git/refs/heads/main" {
		var b struct {
			SHA   string
			Force bool
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			f.t.Fatal(err)
		}
		if b.Force {
			f.t.Fatal("forced update")
		}
		if f.conflicts > 0 {
			f.conflicts--
			return repoJSON(422, nil)
		}
		f.head = b.SHA
		if f.loseResponse {
			f.loseResponse = false
			return nil, errors.New("response lost")
		}
		return repoJSON(200, map[string]any{})
	}
	f.t.Fatal("unexpected request", r.Method, r.URL)
	return nil, errors.New("unexpected request")
}
func prepareRepoFixture(t *testing.T, c *Client, encrypted bool) (string, string, string) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recording.tbt")
	id := recording.NewID()
	if err := recording.Save(path, &recording.Recording{ID: recording.NewID(), Title: "Approved title", Started: 100, Sizes: []int{80, 24}, Lines: []recording.Event{{Lines: []string{"hello"}}}}); err != nil {
		t.Fatal(err)
	}
	key, err := c.PrepareRepository(t.Context(), path, dir, id, "Approved title", encrypted, UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "request.json"), id, key
}
func TestRepositoryPublishReconcilePlaybackAndDelete(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		t.Run(fmt.Sprint(encrypted), func(t *testing.T) {
			f, c := newRepoFixture(t)
			f.empty = true
			path, id, key := prepareRepoFixture(t, c, encrypted)
			p, err := ReadRepoRequest(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(p.Files[PlaybackFilename]), "#k=") {
				t.Fatal("published encryption key")
			}
			if encrypted && p.Share.Title == "Approved title" {
				t.Fatal("unapproved public title")
			}
			if encrypted && p.Share.Duration != nil {
				t.Fatal("encrypted recording exposed its duration")
			}
			if !encrypted && (p.Share.Duration == nil || *p.Share.Duration != 0) {
				t.Fatal("zero-duration public recording omitted its duration")
			}
			f.conflicts = 1
			f.loseResponse = true
			if _, err = c.SendRepository(t.Context(), path, nil); err == nil {
				t.Fatal("expected uncertain write")
			}
			writes := f.writes
			ref, err := c.ReconcileRepository(t.Context(), path)
			if err != nil || ref != "alice/"+id || writes != f.writes {
				t.Fatal("reconciliation wrote duplicate", ref, err, writes, f.writes)
			}
			link := "https://play.example/p/" + ref
			if key != "" {
				link += "#k=" + key
			}
			r, err := c.Load(t.Context(), link)
			if err != nil || r.Title != "Approved title" {
				t.Fatal(r, err)
			}
			list, next, err := c.ListRepository(t.Context(), 1)
			if err != nil || len(list) != 1 || next != 0 {
				t.Fatal(list, next, err)
			}
			if err = c.DeleteRepository(t.Context(), list[0]); err != nil {
				t.Fatal(err)
			}
			if _, err = c.Load(t.Context(), link); err == nil {
				t.Fatal("deleted recording still visible")
			}
			writes = f.writes
			if _, err := c.ReconcileRepository(t.Context(), path); !errors.Is(err, ErrPublicationAbsent) || writes != f.writes {
				t.Fatal("reconciliation recreated a deleted publication", err, writes, f.writes)
			}
			if len(f.blobs) == 0 {
				t.Fatal("delete removed history")
			}
		})
	}
}

func TestRepositoryDurationMetadata(t *testing.T) {
	_, c := newRepoFixture(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "recording.tbt")
	if err := recording.Save(path, &recording.Recording{Sizes: []int{80, 24}, Lines: []recording.Event{{Time: 12500, Lines: []string{"hello"}}}}); err != nil {
		t.Fatal(err)
	}
	id := recording.NewID()
	if _, err := c.PrepareRepository(t.Context(), path, dir, id, "Title", false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	request, err := ReadRepoRequest(filepath.Join(dir, "request.json"))
	if err != nil || request.Share.Duration == nil || *request.Share.Duration != 12500 {
		t.Fatal("duration does not match prepared recording", request.Share, err)
	}
	var descriptor map[string]any
	if err := json.Unmarshal(request.Files[ShareFilename], &descriptor); err != nil || descriptor["duration_ms"] != float64(12500) {
		t.Fatal("public descriptor is missing duration", descriptor, err)
	}
	for _, duration := range []int64{-1, 604800001} {
		request.Share.Duration = &duration
		if request.Share.Validate(id) == nil {
			t.Fatal("invalid duration accepted", duration)
		}
	}
	request.Share.Duration = nil
	if err := request.Share.Validate(id); err != nil {
		t.Fatal("legacy descriptor rejected", err)
	}
}
func TestRepositoryRejectsBroadOrWrongGrants(t *testing.T) {
	for _, change := range []func(*repoFixture){func(f *repoFixture) { f.grant["repository_selection"] = "all" }, func(f *repoFixture) { f.selected = append(f.selected, f.repo) }, func(f *repoFixture) { f.grant["app_id"] = 4 }, func(f *repoFixture) {
		f.grant["permissions"] = map[string]string{"contents": "write", "metadata": "read", "issues": "write"}
	}, func(f *repoFixture) { f.repo.Private = true }, func(f *repoFixture) { f.repo.Owner.ID = 99 }} {
		f, c := newRepoFixture(t)
		change(f)
		if _, err := c.ValidateRepoAccess(t.Context()); err == nil {
			t.Fatal("accepted incorrect grant")
		}
		if f.writes != 0 {
			t.Fatal("wrote before validation")
		}
	}
}
func TestRepositoryPreparedTamperingAndChangedDeleteRefused(t *testing.T) {
	f, c := newRepoFixture(t)
	path, _, _ := prepareRepoFixture(t, c, false)
	if _, err := c.SendRepository(t.Context(), path, nil); err != nil {
		t.Fatal(err)
	}
	list, _, _ := c.ListRepository(t.Context(), 1)
	oldTree := f.commits[f.head]
	newTree := f.sha()
	f.trees[newTree] = append([]treeEntry{}, f.trees[oldTree]...)
	replacement := strings.Repeat("d", 40)
	f.trees[newTree][0].SHA = &replacement
	f.head = f.sha()
	f.commits[f.head] = newTree
	if err := c.DeleteRepository(t.Context(), list[0]); err == nil {
		t.Fatal("deleted changed recording")
	}
	p, _ := ReadRepoRequest(path)
	p.Files["recording.tbt"] = []byte("tampered")
	b, _ := json.Marshal(p)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRepoRequest(path); err == nil {
		t.Fatal("accepted tampering")
	}
}
func TestRepoRefreshSerializesRotationAndPreservesGist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Config{}
	cfg.Set("token", "independent-gist")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	credential := RepoCredential{App: RepoApp{ID: 3, ClientID: "client", Slug: "app"}, AccessToken: "expired", RefreshToken: "single-use", ExpiresAt: 1, RefreshExpiresAt: time.Now().Add(time.Hour).Unix()}
	if err := SaveRepoCredential(path, credential); err != nil {
		t.Fatal(err)
	}
	c := New("")
	var calls atomic.Int32
	c.HTTP.Transport = uploadTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["refresh_token"] != "single-use" || body["client_secret"] != "" || body["scope"] != "" {
			t.Error("bad refresh request", body)
		}
		return repoJSON(200, map[string]any{"access_token": "fresh", "refresh_token": "rotated", "expires_in": 3600, "refresh_token_expires_in": 86400})
	})
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			got, err := c.RepoCredentials(context.Background(), path)
			if err != nil || got.AccessToken != "fresh" || got.RefreshToken != "rotated" {
				t.Error(got, err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("reused single-use token", calls.Load())
	}
	cfg, _ = config.Load(path)
	if cfg.String("token") != "independent-gist" {
		t.Fatal("lost gist credential")
	}
	credential.ExpiresAt = 1
	SaveRepoCredential(path, credential)
	c.HTTP.Transport = uploadTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("lost refresh response") })
	if _, err := c.RepoCredentials(t.Context(), path); err == nil {
		t.Fatal("expected refresh failure")
	}
	cfg, _ = config.Load(path)
	if _, ok := cfg["repo_auth"]; ok {
		t.Fatal("retained possibly consumed refresh token")
	}
}

func TestStorageCredentialsNeverCrossDestinations(t *testing.T) {
	c := New("gist-token")
	if _, err := c.Backend(t.Context(), "repo"); err == nil {
		t.Fatal("used Gist credential for repository")
	}
	c.Storage = "repo"
	if _, err := c.Backend(t.Context(), ""); err == nil {
		t.Fatal("used repository credential for legacy Gist job")
	}
	if _, err := c.Backend(t.Context(), "unknown"); err == nil {
		t.Fatal("accepted unknown storage")
	}
}
