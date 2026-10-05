package github

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/sealed"
	"github.com/termbacktime/termbacktime/internal/sharing"
)

const ShareFormat = "tbt-repo-share-v1"
const ShareFilename = "share.json"

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var gitSHA = regexp.MustCompile(`^[a-f0-9]{40}$`)
var partName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type RepoIdentity struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Private  bool   `json:"private"`
	Archived bool   `json:"archived"`
	Branch   string `json:"default_branch"`
	Owner    struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	} `json:"owner"`
}
type ShareFile struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type RepoShare struct {
	Format    string               `json:"format"`
	ID        string               `json:"id"`
	Created   int64                `json:"created"`
	Title     string               `json:"title"`
	Encrypted bool                 `json:"encrypted"`
	Duration  *int64               `json:"duration_ms,omitempty"`
	Files     map[string]ShareFile `json:"files"`
}
type RepoRequest struct {
	Repository RepoIdentity      `json:"repository"`
	Share      RepoShare         `json:"share"`
	Files      map[string][]byte `json:"files"`
}
type repoTree struct {
	SHA       string      `json:"sha"`
	Truncated bool        `json:"truncated"`
	Tree      []treeEntry `json:"tree"`
}
type treeEntry struct {
	Path string  `json:"path"`
	Mode string  `json:"mode"`
	Type string  `json:"type"`
	SHA  *string `json:"sha"`
}

func statusIs(err error, status int) bool {
	var e *HTTPError
	return errors.As(err, &e) && e.Status == status
}
func hashBytes(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func repoAPI(owner string) string {
	return "https://api.github.com/repos/" + url.PathEscape(owner) + "/" + sharing.Repository
}
func (c *Client) jsonRequest(ctx context.Context, method, target string, body, result any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	b, err := c.request(ctx, method, target, data)
	if err != nil {
		return err
	}
	if result != nil {
		return json.Unmarshal(b, result)
	}
	return nil
}
func (c *Client) Repository(ctx context.Context, owner string) (RepoIdentity, error) {
	var repo RepoIdentity
	if !sharing.ValidOwner(owner) {
		return repo, fmt.Errorf("invalid repository owner")
	}
	err := c.jsonRequest(ctx, "GET", repoAPI(owner), nil, &repo)
	if err != nil {
		return repo, err
	}
	if repo.ID <= 0 || !strings.EqualFold(repo.Name, sharing.Repository) || !strings.EqualFold(repo.Owner.Login, owner) || repo.Private || repo.Archived {
		return repo, fmt.Errorf("TBT-Recordings must be a public, active repository owned by this account")
	}
	return repo, nil
}
func (c *Client) RepoHead(ctx context.Context, repo RepoIdentity) (string, string, error) {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err := c.jsonRequest(ctx, "GET", repoAPI(repo.Owner.Login)+"/git/ref/heads/"+url.PathEscape(repo.Branch), nil, &ref)
	if err != nil {
		return "", "", err
	}
	var commit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if !gitSHA.MatchString(ref.Object.SHA) {
		return "", "", fmt.Errorf("invalid repository revision")
	}
	err = c.jsonRequest(ctx, "GET", repoAPI(repo.Owner.Login)+"/git/commits/"+ref.Object.SHA, nil, &commit)
	if err == nil && !gitSHA.MatchString(commit.Tree.SHA) {
		err = fmt.Errorf("invalid repository tree")
	}
	return ref.Object.SHA, commit.Tree.SHA, err
}
func (s RepoShare) Validate(id string) error {
	if s.Format != ShareFormat || s.ID != id || !recording.ValidID(id) || s.Created <= 0 || len(s.Title) > 4096 || len(s.Files) < 1 || len(s.Files) > 128 {
		return fmt.Errorf("invalid repository recording descriptor")
	}
	if s.Duration != nil && (*s.Duration < 0 || *s.Duration > 604800000) {
		return fmt.Errorf("invalid repository recording duration")
	}
	var total int64
	for name, f := range s.Files {
		if !partName.MatchString(name) || name == ShareFilename || f.Size < 0 || f.Size > recording.MaxBytes || !digestPattern.MatchString(f.SHA256) {
			return fmt.Errorf("invalid repository recording file")
		}
		total += f.Size
	}
	if total > 3*recording.MaxBytes {
		return fmt.Errorf("repository recording exceeds sharing limit")
	}
	_, plain := s.Files["recording.tbt"]
	_, encrypted := s.Files[sealed.Filename]
	if s.Encrypted != encrypted || plain == encrypted {
		return fmt.Errorf("ambiguous repository recording payload")
	}
	return nil
}
func (c *Client) rawRepoFile(ctx context.Context, owner, commit, id, name string, limit int64) ([]byte, error) {
	if !sharing.ValidOwner(owner) || !gitSHA.MatchString(commit) || !recording.ValidID(id) || !partName.MatchString(name) {
		return nil, fmt.Errorf("invalid repository download")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://raw.githubusercontent.com/"+owner+"/"+sharing.Repository+"/"+commit+"/recordings/"+id+"/"+name, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.send(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return recording.ReadBounded(res.Body, limit)
}
func (c *Client) ReadRepoShare(ctx context.Context, ref sharing.Reference, commit string) (RepoShare, error) {
	var s RepoShare
	b, err := c.rawRepoFile(ctx, ref.Owner, commit, ref.ID, ShareFilename, 65536)
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	return s, s.Validate(ref.ID)
}
func (c *Client) LoadRepository(ctx context.Context, ref sharing.Reference, input string) (*recording.Recording, error) {
	// Public playback does not borrow the independent Gist credential.
	anonymous := *c
	anonymous.Token = ""
	c = &anonymous
	repo, err := c.Repository(ctx, ref.Owner)
	if err != nil {
		return nil, err
	}
	commit, _, err := c.RepoHead(ctx, repo)
	if err != nil {
		return nil, err
	}
	s, err := c.ReadRepoShare(ctx, ref, commit)
	if err != nil {
		return nil, err
	}
	read := func(name string) ([]byte, error) {
		f, ok := s.Files[name]
		if !ok {
			return nil, fmt.Errorf("missing repository payload")
		}
		data, e := c.rawRepoFile(ctx, ref.Owner, commit, ref.ID, name, f.Size)
		if e == nil && (int64(len(data)) != f.Size || hashBytes(data) != f.SHA256) {
			e = fmt.Errorf("repository payload checksum mismatch")
		}
		return data, e
	}
	var data []byte
	if s.Encrypted {
		u, _ := url.Parse(input)
		key := ""
		if u != nil {
			q, _ := url.ParseQuery(u.Fragment)
			key = q.Get("k")
		}
		if key == "" {
			return nil, fmt.Errorf("encrypted recording requires its complete sharing link")
		}
		b, e := read(sealed.Filename)
		if e != nil {
			return nil, e
		}
		var m sealed.Manifest
		if e = json.Unmarshal(b, &m); e != nil {
			return nil, e
		}
		data, err = sealed.Decrypt(ctx, m, key, read)
	} else {
		data, err = read("recording.tbt")
	}
	if err != nil {
		return nil, err
	}
	defer clear(data)
	return recording.Decode(bytes.NewReader(data))
}

func (c *Client) CheckRepositorySupport(ctx context.Context) error {
	if c.SiteURL == "" {
		return fmt.Errorf("repository uploads require a playback endpoint")
	}
	var cfg struct {
		Sources []string `json:"recordingSources"`
	}
	if err := c.jsonRequest(ctx, "GET", strings.TrimRight(c.SiteURL, "/")+"/api/v1/config", nil, &cfg); err != nil {
		return fmt.Errorf("check repository playback support: %w", err)
	}
	for _, v := range cfg.Sources {
		if v == sharing.Repo {
			return nil
		}
	}
	return fmt.Errorf("update the website before uploading repository recordings")
}
func (c *Client) PrepareRepository(ctx context.Context, path, dir, id, title string, encrypted bool, options UploadOptions) (string, error) {
	repo, err := c.ValidateRepoAccess(ctx)
	if err != nil {
		return "", err
	}
	if err = c.CheckRepositorySupport(ctx); err != nil {
		return "", err
	}
	if encrypted {
		if err = c.CheckEncryption(ctx); err != nil {
			return "", err
		}
	}
	p := RepoRequest{Repository: repo, Share: RepoShare{Format: ShareFormat, ID: id, Created: time.Now().Unix(), Title: title, Encrypted: encrypted, Files: map[string]ShareFile{}}, Files: map[string][]byte{}}
	key := ""
	if encrypted {
		_, k, files, e := sealed.EncryptFile(ctx, path, dir)
		if e != nil {
			return "", e
		}
		key = k
		p.Share.Title = "Encrypted terminal recording"
		for name, path := range files {
			p.Files[name], err = os.ReadFile(path)
			if err != nil {
				return "", err
			}
		}
	} else {
		p.Files["recording.tbt"], err = os.ReadFile(path)
		if err != nil {
			return "", err
		}
		// Derive public list metadata from the exact bytes being published.
		source, e := recording.OpenReader(bytes.NewReader(p.Files["recording.tbt"]), int64(len(p.Files["recording.tbt"])))
		if e != nil {
			return "", e
		}
		duration := source.Manifest.Duration
		p.Share.Duration = &duration
		source.Close()
	}
	ref := sharing.Reference{Storage: sharing.Repo, Owner: repo.Owner.Login, ID: id}
	readme := "# Terminal recording\n\n[Play recording](" + ref.Link(c.SiteURL, "") + ")\n"
	if encrypted {
		readme += "\nOpen the complete private sharing link to decrypt this recording. The key is not stored here.\n"
	}
	if options.Metadata != "" {
		readme += "\n" + options.Metadata + "\n"
	}
	p.Files[PlaybackFilename] = []byte(readme)
	for name, data := range p.Files {
		p.Share.Files[name] = ShareFile{Size: int64(len(data)), SHA256: hashBytes(data)}
	}
	if err = p.Share.Validate(id); err != nil {
		return "", err
	}
	p.Files[ShareFilename], err = json.Marshal(p.Share)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return key, recording.Publish(filepath.Join(dir, "request.json"), func(w io.Writer) error { _, err := w.Write(data); return err })
}
func ReadRepoRequest(path string) (RepoRequest, error) {
	var p RepoRequest
	f, e := os.Open(path)
	if e != nil {
		return p, e
	}
	defer f.Close()
	b, e := recording.ReadBounded(f, 6*recording.MaxBytes)
	if e != nil {
		return p, e
	}
	if e = json.Unmarshal(b, &p); e != nil {
		return p, e
	}
	if e = p.Share.Validate(p.Share.ID); e != nil {
		return p, e
	}
	if len(p.Files) != len(p.Share.Files)+1 {
		return p, fmt.Errorf("prepared repository file set changed")
	}
	for n, f := range p.Share.Files {
		b, ok := p.Files[n]
		if !ok || int64(len(b)) != f.Size || hashBytes(b) != f.SHA256 {
			return p, fmt.Errorf("prepared repository file changed")
		}
	}
	var s RepoShare
	encoded, _ := json.Marshal(p.Share)
	if !bytes.Equal(encoded, p.Files[ShareFilename]) || json.Unmarshal(encoded, &s) != nil {
		return p, fmt.Errorf("prepared repository descriptor changed")
	}
	return p, nil
}
func (c *Client) repoTree(ctx context.Context, owner, sha string, recursive bool) (repoTree, error) {
	var t repoTree
	target := repoAPI(owner) + "/git/trees/" + sha
	if recursive {
		target += "?recursive=1"
	}
	err := c.jsonRequest(ctx, "GET", target, nil, &t)
	if err == nil && t.Truncated {
		err = fmt.Errorf("repository listing truncated; reduce directory size before continuing")
	}
	return t, err
}
func (c *Client) writeRepoTree(ctx context.Context, repo RepoIdentity, head, base, message string, entries []treeEntry) error {
	var tree, commit struct {
		SHA string `json:"sha"`
	}
	if err := c.jsonRequest(ctx, "POST", repoAPI(repo.Owner.Login)+"/git/trees", map[string]any{"base_tree": base, "tree": entries}, &tree); err != nil {
		return err
	}
	if !gitSHA.MatchString(tree.SHA) {
		return fmt.Errorf("invalid tree response")
	}
	if err := c.jsonRequest(ctx, "POST", repoAPI(repo.Owner.Login)+"/git/commits", map[string]any{"message": message, "tree": tree.SHA, "parents": []string{head}}, &commit); err != nil {
		return err
	}
	if !gitSHA.MatchString(commit.SHA) {
		return fmt.Errorf("invalid commit response")
	}
	return c.jsonRequest(ctx, "PATCH", repoAPI(repo.Owner.Login)+"/git/refs/heads/"+url.PathEscape(repo.Branch), map[string]any{"sha": commit.SHA, "force": false}, nil)
}

// ErrPublicationAbsent means a snapshot conclusively contains no publication.
var ErrPublicationAbsent = errors.New("repository publication is absent")

func (c *Client) SendRepository(ctx context.Context, path string, progress func(int64)) (string, error) {
	return c.sendRepository(ctx, path, progress, true)
}
func (c *Client) ReconcileRepository(ctx context.Context, path string) (string, error) {
	return c.sendRepository(ctx, path, nil, false)
}
func (c *Client) sendRepository(ctx context.Context, path string, progress func(int64), allowWrite bool) (string, error) {
	p, err := ReadRepoRequest(path)
	if err != nil {
		return "", err
	}
	repo, err := c.ValidateRepoAccess(ctx)
	if err != nil {
		return "", err
	}
	if repo.ID != p.Repository.ID || repo.Owner.ID != p.Repository.Owner.ID {
		return "", fmt.Errorf("repository or account changed since upload preparation")
	}
	ref := sharing.Reference{Storage: sharing.Repo, Owner: repo.Owner.Login, ID: p.Share.ID}
	entries := []treeEntry{}
	names := []string{}
	for n := range p.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	for attempt := 0; attempt < 3; attempt++ {
		head, base, e := c.RepoHead(ctx, repo)
		if statusIs(e, 409) && !allowWrite {
			return "", ErrPublicationAbsent
		}
		if statusIs(e, 409) { // Contents API is required to initialize an empty Git database.
			var result any
			e = c.jsonRequest(ctx, "PUT", repoAPI(repo.Owner.Login)+"/contents/README.md", map[string]any{"message": "Initialize TermBackTime recordings", "content": []byte("# TBT-Recordings\n\nTerminal recordings shared with TermBackTime.\n")}, &result)
			if e == nil {
				repo, e = c.Repository(ctx, repo.Owner.Login)
			}
			if e == nil {
				head, base, e = c.RepoHead(ctx, repo)
			}
		}
		if e != nil {
			return "", e
		}
		// Check the tree, rather than a raw 404, before concluding that this ID is absent.
		tree, e := c.repoTree(ctx, repo.Owner.Login, base, true)
		if e != nil {
			return "", e
		}
		prefix := "recordings/" + ref.ID + "/"
		existing := map[string]string{}
		for _, entry := range tree.Tree {
			if entry.Path == strings.TrimSuffix(prefix, "/") && entry.Type != "tree" {
				return "", fmt.Errorf("publication path conflicts with an existing file")
			}
			if strings.HasPrefix(entry.Path, prefix) && (entry.Type != "blob" || entry.Mode != "100644" || entry.SHA == nil) {
				return "", fmt.Errorf("publication ID conflicts with existing files")
			}
			if strings.HasPrefix(entry.Path, prefix) && entry.Type == "blob" && entry.SHA != nil {
				existing[strings.TrimPrefix(entry.Path, prefix)] = *entry.SHA
			}
			if entry.Path == "recordings" && entry.Type != "tree" {
				return "", fmt.Errorf("recordings path conflicts with an existing file")
			}
		}
		if len(existing) > 0 {
			if len(existing) != len(p.Files) {
				return "", fmt.Errorf("publication ID conflicts with existing files")
			}
			for n, data := range p.Files {
				b, e := c.rawRepoFile(ctx, ref.Owner, head, ref.ID, n, int64(len(data)))
				if e != nil {
					return "", e
				}
				if !bytes.Equal(b, data) {
					return "", fmt.Errorf("publication ID conflicts with existing contents")
				}
			}
			return ref.String(), nil
		}
		if !allowWrite {
			return "", ErrPublicationAbsent
		}
		if len(entries) == 0 {
			var sent int64
			for _, n := range names {
				var blob struct {
					SHA string `json:"sha"`
				}
				e = c.jsonRequest(ctx, "POST", repoAPI(repo.Owner.Login)+"/git/blobs", map[string]any{"encoding": "base64", "content": p.Files[n]}, &blob)
				if e != nil {
					return "", e
				}
				if !gitSHA.MatchString(blob.SHA) {
					return "", fmt.Errorf("invalid blob response")
				}
				sha := blob.SHA
				entries = append(entries, treeEntry{Path: prefix + n, Mode: "100644", Type: "blob", SHA: &sha})
				sent += int64(len(p.Files[n]))
				if progress != nil {
					progress(sent)
				}
			}
		}
		err = c.writeRepoTree(ctx, repo, head, base, "Publish terminal recording "+ref.ID, entries)
		if err == nil {
			return ref.String(), nil
		}
		if !statusIs(err, 409) && !statusIs(err, 422) {
			return "", err
		}
	}
	return "", fmt.Errorf("repository changed concurrently; run the queue again to reconcile")
}

func (c *Client) ListRepository(ctx context.Context, page int) ([]GistSummary, int, error) {
	repo, err := c.ValidateRepoAccess(ctx)
	if statusIs(err, 404) {
		return []GistSummary{}, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if page < 1 {
		return nil, 0, fmt.Errorf("invalid page")
	}
	head, base, err := c.RepoHead(ctx, repo)
	if statusIs(err, 409) {
		return []GistSummary{}, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	tree, err := c.repoTree(ctx, repo.Owner.Login, base, true)
	if err != nil {
		return nil, 0, err
	}
	ids := []string{}
	for _, e := range tree.Tree {
		parts := strings.Split(e.Path, "/")
		if len(parts) == 3 && parts[0] == "recordings" && recording.ValidID(parts[1]) && parts[2] == ShareFilename && e.Type == "blob" && e.Mode == "100644" {
			ids = append(ids, parts[1])
		}
	}
	sort.Strings(ids)
	start := min((page-1)*30, len(ids))
	end := min(start+30, len(ids))
	out := []GistSummary{}
	for _, id := range ids[start:end] {
		s, e := c.ReadRepoShare(ctx, sharing.Reference{Storage: sharing.Repo, Owner: repo.Owner.Login, ID: id}, head)
		if e != nil {
			return nil, 0, e
		}
		g := GistSummary{ID: id, Storage: sharing.Repo, Revision: head, RepositoryID: repo.ID, Description: s.Title, Public: true, Created: time.Unix(s.Created, 0), Updated: time.Unix(s.Created, 0)}
		g.Owner = repo.Owner
		g.Files = map[string]struct {
			Size int64 `json:"size"`
		}{}
		for n, f := range s.Files {
			g.Files[n] = struct {
				Size int64 `json:"size"`
			}{f.Size}
		}
		out = append(out, g)
	}
	next := 0
	if end < len(ids) {
		next = page + 1
	}
	return out, next, nil
}
func (c *Client) DeleteRepository(ctx context.Context, g GistSummary) error {
	repo, err := c.ValidateRepoAccess(ctx)
	if err != nil {
		return err
	}
	if repo.ID != g.RepositoryID || repo.Owner.ID != g.Owner.ID {
		return fmt.Errorf("repository ownership changed")
	}
	head, base, err := c.RepoHead(ctx, repo)
	if err != nil {
		return err
	}
	if !gitSHA.MatchString(g.Revision) || !recording.ValidID(g.ID) {
		return fmt.Errorf("invalid recording snapshot")
	}
	var oldCommit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err = c.jsonRequest(ctx, "GET", repoAPI(repo.Owner.Login)+"/git/commits/"+g.Revision, nil, &oldCommit); err != nil {
		return err
	}
	if !gitSHA.MatchString(oldCommit.Tree.SHA) {
		return fmt.Errorf("invalid recording snapshot tree")
	}
	old, err := c.repoTree(ctx, repo.Owner.Login, oldCommit.Tree.SHA, true)
	if err != nil {
		return err
	}
	current, err := c.repoTree(ctx, repo.Owner.Login, base, true)
	if err != nil {
		return err
	}
	prefix := "recordings/" + g.ID + "/"
	snapshot := func(t repoTree) map[string]string {
		m := map[string]string{}
		for _, e := range t.Tree {
			if strings.HasPrefix(e.Path, prefix) && e.Type == "blob" && e.SHA != nil {
				m[e.Path] = e.Mode + ":" + *e.SHA
			}
		}
		return m
	}
	a, b := snapshot(old), snapshot(current)
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	if len(a) == 0 || !bytes.Equal(x, y) {
		return fmt.Errorf("recording changed; refresh before deleting")
	}
	entries := []treeEntry{}
	for p := range b {
		entries = append(entries, treeEntry{Path: p, Mode: "100644", Type: "blob", SHA: nil})
	}
	return c.writeRepoTree(ctx, repo, head, base, "Remove terminal recording "+g.ID, entries)
}
