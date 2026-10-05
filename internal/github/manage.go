package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/sealed"
	"github.com/termbacktime/termbacktime/internal/sharing"
)

// GistSummary deliberately excludes file contents and private playback links.
type GistSummary struct {
	Storage      string    `json:"storage,omitempty"`
	Revision     string    `json:"revision,omitempty"`
	RepositoryID int64     `json:"repository_id,omitempty"`
	ID           string    `json:"id"`
	Description  string    `json:"description"`
	Public       bool      `json:"public"`
	Created      time.Time `json:"created_at"`
	Updated      time.Time `json:"updated_at"`
	Files        map[string]struct {
		Size int64 `json:"size"`
	} `json:"files"`
	Owner struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	} `json:"owner"`
}

func (g GistSummary) Reference() sharing.Reference {
	s := g.Storage
	if s == "" {
		s = sharing.Gist
	}
	return sharing.Reference{Storage: s, Owner: g.Owner.Login, ID: g.ID}
}

func (g GistSummary) Encrypted() bool   { _, ok := g.Files[sealed.Filename]; return ok }
func (g GistSummary) IsRecording() bool { _, ok := g.Files[Filename]; return ok || g.Encrypted() }

// ListRecordings reads one small page of the authenticated user's Gists. Empty
// filtered pages can still have a next page: unrelated Gists are never displayed.
func (c *Client) ListRecordings(ctx context.Context, page int) ([]GistSummary, int, error) {
	if c.Token == "" {
		return nil, 0, fmt.Errorf("GitHub recordings require authentication; run termbacktime auth")
	}
	if page < 1 {
		return nil, 0, fmt.Errorf("invalid Gist page")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://api.github.com/gists?per_page=30&page=%d", page), nil)
	if err != nil {
		return nil, 0, err
	}
	res, err := c.send(req)
	if err != nil {
		return nil, 0, fmt.Errorf("list Gists (check auth and GitHub rate limits): %w", err)
	}
	defer res.Body.Close()
	b, err := recording.ReadBounded(res.Body, 2<<20)
	if err != nil {
		return nil, 0, err
	}
	var all []GistSummary
	if err = json.Unmarshal(b, &all); err != nil {
		return nil, 0, err
	}
	next := 0
	for _, link := range strings.Split(res.Header.Get("Link"), ",") {
		if strings.Contains(link, `rel="next"`) {
			next = page + 1
		}
	}
	entries := []GistSummary{}
	for _, g := range all {
		if idPattern.MatchString(g.ID) && g.IsRecording() {
			entries = append(entries, g)
		}
	}
	return entries, next, nil
}

// DeleteRecording rechecks ownership and recording files before deleting the
// entire Gist, including its companion files. No retries hide an uncertain delete.
func (c *Client) DeleteRecording(ctx context.Context, id string) error {
	if c.Token == "" {
		return fmt.Errorf("deletion requires authentication; run termbacktime auth")
	}
	if !idPattern.MatchString(id) {
		return fmt.Errorf("invalid Gist ID")
	}
	b, err := c.request(ctx, "GET", "https://api.github.com/user", nil)
	if err != nil {
		return err
	}
	var user struct {
		ID int64 `json:"id"`
	}
	if json.Unmarshal(b, &user) != nil || user.ID == 0 {
		return fmt.Errorf("could not verify GitHub identity")
	}
	b, err = c.request(ctx, "GET", "https://api.github.com/gists/"+id, nil)
	if err != nil {
		return err
	}
	var gist GistSummary
	if json.Unmarshal(b, &gist) != nil || gist.ID != id || !gist.IsRecording() {
		return fmt.Errorf("Gist is no longer a TermBackTime recording; refresh the list")
	}
	if gist.Owner.ID != user.ID {
		return fmt.Errorf("cannot delete another user's Gist")
	}
	req, err := http.NewRequestWithContext(ctx, "DELETE", "https://api.github.com/gists/"+id, nil)
	if err != nil {
		return err
	}
	res, err := c.send(req)
	if err != nil {
		return fmt.Errorf("delete Gist (refresh to verify its state before retrying): %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected deletion status; refresh to verify the Gist")
	}
	return nil
}
