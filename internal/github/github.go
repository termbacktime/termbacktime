package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/sealed"
	"github.com/termbacktime/termbacktime/internal/sharing"
)

const Filename = "terminal-recording.tbt.json"

var idPattern = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)

type Client struct {
	HTTP     *http.Client
	Token    string
	SiteURL  string
	Storage  string
	AppID    int64
	Select   func(context.Context, string) (*Client, error)
	pollWait func(context.Context, time.Duration) error
}

func (c *Client) waitForPoll(ctx context.Context, interval time.Duration) error {
	if c.pollWait != nil {
		return c.pollWait(ctx, interval)
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) Backend(ctx context.Context, storage string) (*Client, error) {
	if storage == "" {
		storage = sharing.Gist
	}
	if storage != sharing.Gist && storage != sharing.Repo {
		return nil, fmt.Errorf("unknown recording storage")
	}
	if c.Select != nil {
		return c.Select(ctx, storage)
	}
	current := c.Storage
	if current == "" {
		current = sharing.Gist
	}
	if storage != current {
		return nil, fmt.Errorf("%s authentication required; run termbacktime auth --storage %s", storage, storage)
	}
	return c, nil
}

func New(token string) *Client {
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}, Token: token}
}

// request checks status codes and bounds responses before decoding remote data
func (c *Client) request(ctx context.Context, method, target string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	res, err := c.send(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return recording.ReadBounded(res.Body, recording.MaxBytes)
}

func GistID(input string) (string, error) {
	if idPattern.MatchString(input) {
		return strings.ToLower(input), nil
	}
	u, err := url.Parse(input)
	if err == nil && (u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) && (u.Host == "gist.github.com" || strings.HasPrefix(u.Path, "/p/")) {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		id := parts[len(parts)-1]
		if idPattern.MatchString(id) {
			return strings.ToLower(id), nil
		}
	}
	return "", fmt.Errorf("expected a local file or GitHub Gist ID/link")
}

func (c *Client) Load(ctx context.Context, input string) (*recording.Recording, error) {
	ref, err := sharing.Parse(input)
	if err != nil {
		return nil, err
	}
	if ref.Storage == sharing.Repo {
		return c.LoadRepository(ctx, ref, input)
	}
	id := ref.ID
	b, err := c.request(ctx, "GET", "https://api.github.com/gists/"+id, nil)
	if err != nil {
		return nil, err
	}
	var gist struct {
		Files map[string]gistFile `json:"files"`
	}
	if err = json.Unmarshal(b, &gist); err != nil {
		return nil, err
	}
	if _, ok := gist.Files[sealed.Filename]; ok {
		return c.loadEncrypted(ctx, input, gist.Files)
	}
	f, ok := gist.Files[Filename]
	if !ok {
		return nil, fmt.Errorf("Gist does not contain %s", Filename)
	}
	if f.Truncated {
		u, err := url.Parse(f.RawURL)
		if err != nil || u.Scheme != "https" || u.Host != "gist.githubusercontent.com" {
			return nil, fmt.Errorf("invalid raw Gist URL")
		}
		b, err = c.request(ctx, "GET", u.String(), nil)
		if err != nil {
			return nil, err
		}
		return recording.ReadWrapped(bytes.NewReader(b))
	}
	return recording.ReadWrapped(strings.NewReader(f.Content))
}

// Authorize polls GitHub device flow with cancellation and server-directed backoff
func (c *Client) Authorize(ctx context.Context, clientID string, display func(string, string)) (string, error) {
	if clientID == "" {
		return "", fmt.Errorf("GitHub device authorization is not configured; configure GITHUB_CLIENT_ID on the Worker or use auth --token-stdin")
	}
	b, _ := json.Marshal(map[string]string{"client_id": clientID, "scope": "gist"})
	b, err := c.request(ctx, "POST", "https://github.com/login/device/code", b)
	if err != nil {
		return "", err
	}
	var d struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		URI        string `json:"verification_uri"`
		Expires    int    `json:"expires_in"`
		Interval   int    `json:"interval"`
	}
	if err = json.Unmarshal(b, &d); err != nil {
		return "", err
	}
	if d.DeviceCode == "" || d.URI != "https://github.com/login/device" || d.Expires <= 0 {
		return "", fmt.Errorf("invalid authorization response")
	}
	display(d.URI, d.UserCode)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(min(d.Expires, 1800))*time.Second)
	defer cancel()
	interval := time.Duration(max(d.Interval, 5)) * time.Second
	for {
		if err := c.waitForPoll(ctx, interval); err != nil {
			return "", err
		}
		payload, _ := json.Marshal(map[string]string{
			"client_id":   clientID,
			"device_code": d.DeviceCode,
			"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		})
		b, err = c.request(ctx, "POST", "https://github.com/login/oauth/access_token", payload)
		if err != nil {
			return "", err
		}
		var p struct {
			Token string `json:"access_token"`
			Error string `json:"error"`
		}
		if err = json.Unmarshal(b, &p); err != nil {
			return "", err
		}
		switch p.Error {
		case "":
			if p.Token == "" {
				return "", fmt.Errorf("empty authorization token")
			}
			return p.Token, nil
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "expired_token", "access_denied":
			return "", fmt.Errorf("authorization %s", p.Error)
		default:
			return "", fmt.Errorf("authorization failed")
		}
	}
}
