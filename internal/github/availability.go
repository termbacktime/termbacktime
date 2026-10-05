package github

import (
	"context"
	"errors"
	"fmt"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/sharing"
	"net/http"
)

// CheckAvailability uses HEAD so an explicit check never downloads Gist contents.
func (c *Client) CheckAvailability(ctx context.Context, id string) (string, error) {
	if ref, e := sharing.Parse(id); e == nil && ref.Storage == sharing.Repo {
		anonymous := *c
		anonymous.Token = ""
		c = &anonymous
		repo, err := c.Repository(ctx, ref.Owner)
		if statusIs(err, 404) {
			return "Inaccessible or missing", nil
		}
		if err != nil {
			return "Repository unavailable", err
		}
		head, _, err := c.RepoHead(ctx, repo)
		if err != nil {
			return "Repository unavailable", err
		}
		_, err = c.ReadRepoShare(ctx, ref, head)
		if statusIs(err, 404) {
			return "Inaccessible or missing", nil
		}
		if err != nil {
			return "Repository unavailable", err
		}
		return "Available", nil
	}
	if !recording.ValidID(id) {
		return "", fmt.Errorf("invalid Gist ID")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://api.github.com/gists/"+id, nil)
	if err != nil {
		return "", err
	}
	response, err := c.send(request)
	status := 0
	if err != nil {
		var failure *HTTPError
		if !errors.As(err, &failure) {
			return "Network failure", err
		}
		status = failure.Status
	} else {
		defer response.Body.Close()
		status = response.StatusCode
	}
	switch status {
	case 200:
		return "Available", nil
	case 404:
		return "Inaccessible or missing", nil
	case 401:
		return "Authentication failed", nil
	case 403, 429:
		return "Rate limited or access denied", nil
	default:
		return fmt.Sprintf("Availability unknown (HTTP %d)", status), nil
	}
}
