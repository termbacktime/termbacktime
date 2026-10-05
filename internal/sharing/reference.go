// Package sharing identifies published recordings independently of their storage.
package sharing

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const Gist = "gist"
const Repo = "repo"
const Repository = "TBT-Recordings"

var identifier = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)
var username = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,37}[a-zA-Z0-9])?$`)

type Reference struct {
	Storage string `json:"storage"`
	Owner   string `json:"owner,omitempty"`
	ID      string `json:"id"`
}

func ValidOwner(owner string) bool {
	return username.MatchString(owner) && !strings.Contains(owner, "--")
}
func (r Reference) String() string {
	if r.Storage == Repo {
		return r.Owner + "/" + r.ID
	}
	return r.ID
}
func (r Reference) Path() string { return "/p/" + r.String() }
func (r Reference) Link(origin, key string) string {
	link := strings.TrimRight(origin, "/") + r.Path()
	if key != "" {
		link += "#k=" + key
	}
	return link
}

func Parse(input string) (Reference, error) {
	value := strings.TrimSpace(input)
	if identifier.MatchString(value) {
		return Reference{Storage: Gist, ID: strings.ToLower(value)}, nil
	}
	parts := strings.Split(value, "/")
	if u, err := url.Parse(value); err == nil && u.Host != "" {
		if u.User != nil || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
			return Reference{}, fmt.Errorf("invalid recording link")
		}
		parts = strings.Split(strings.Trim(u.Path, "/"), "/")
		if u.Hostname() == "gist.github.com" && len(parts) >= 1 && len(parts) <= 2 {
			return Parse(parts[len(parts)-1])
		}
		if len(parts) < 2 || (parts[0] != "p" && parts[0] != "embed") {
			return Reference{}, fmt.Errorf("invalid recording link")
		}
		parts = parts[1:]
		if len(parts) == 1 {
			return Parse(parts[0])
		}
	}
	if len(parts) == 2 && ValidOwner(parts[0]) && identifier.MatchString(parts[1]) {
		return Reference{Storage: Repo, Owner: strings.ToLower(parts[0]), ID: strings.ToLower(parts[1])}, nil
	}
	return Reference{}, fmt.Errorf("expected a Gist ID or recording playback link")
}
