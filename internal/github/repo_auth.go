package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/termbacktime/termbacktime/internal/config"
	"github.com/termbacktime/termbacktime/internal/sharing"
	"golang.org/x/sys/unix"
)

type RepoApp struct {
	ID       int64  `json:"id"`
	ClientID string `json:"clientId"`
	Slug     string `json:"slug"`
}

func (a RepoApp) Valid() bool {
	return a.ID > 0 && a.ClientID != "" && len(a.ClientID) <= 128 && !strings.ContainsAny(a.ClientID, " \r\n\t") && sharing.ValidOwner(a.Slug)
}
func (a RepoApp) InstallURL(owner, repo int64) string {
	return fmt.Sprintf("https://github.com/apps/%s/installations/new/permissions?suggested_target_id=%d&repository_ids%%5B%%5D=%d", url.PathEscape(a.Slug), owner, repo)
}

type RepoCredential struct {
	App              RepoApp `json:"app"`
	AccessToken      string  `json:"access_token"`
	RefreshToken     string  `json:"refresh_token,omitempty"`
	ExpiresAt        int64   `json:"expires_at,omitempty"`
	RefreshExpiresAt int64   `json:"refresh_expires_at,omitempty"`
}
type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshExpiresIn int64  `json:"refresh_token_expires_in"`
	Error            string `json:"error"`
}

// DeviceAuthorizationError identifies failures before a device code is issued.
// Only a rejected client ID permits rediscovery; failures after the user sees a
// code must never restart authorization automatically.
type DeviceAuthorizationError struct {
	Err           error
	InvalidClient bool
}

func (e *DeviceAuthorizationError) Error() string {
	message := "start GitHub App device authorization: " + e.Err.Error()
	if e.InvalidClient {
		message += "; the configured Client ID is unavailable. Run auth --storage repo --refresh-client-id; if it still fails, update the website's GitHub App identifiers"
	}
	return message
}
func (e *DeviceAuthorizationError) Unwrap() error { return e.Err }

func repoCredential(app RepoApp, t tokenResponse) (RepoCredential, error) {
	if t.AccessToken == "" || t.RefreshToken == "" || t.ExpiresIn <= 0 || t.RefreshExpiresIn <= 0 {
		return RepoCredential{}, fmt.Errorf("GitHub App must enable expiring user tokens")
	}
	now := time.Now().Unix()
	return RepoCredential{App: app, AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, ExpiresAt: now + t.ExpiresIn, RefreshExpiresAt: now + t.RefreshExpiresIn}, nil
}
func (c *Client) AuthorizeRepo(ctx context.Context, app RepoApp, display func(string, string)) (RepoCredential, error) {
	if !app.Valid() {
		return RepoCredential{}, fmt.Errorf("repository GitHub App is not configured on this website")
	}
	var d struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		URI        string `json:"verification_uri"`
		Expires    int    `json:"expires_in"`
		Interval   int    `json:"interval"`
		Error      string `json:"error"`
	}
	if err := c.jsonRequest(ctx, "POST", "https://github.com/login/device/code", map[string]string{"client_id": app.ClientID}, &d); err != nil {
		return RepoCredential{}, &DeviceAuthorizationError{Err: err, InvalidClient: statusIs(err, 404)}
	}
	if d.Error != "" {
		message := "GitHub rejected the device authorization request"
		switch d.Error {
		case "incorrect_client_credentials":
			message = "GitHub rejected the configured Client ID"
		case "device_flow_disabled":
			message = "enable Device Flow in the GitHub App settings"
		}
		return RepoCredential{}, &DeviceAuthorizationError{Err: fmt.Errorf("%s", message), InvalidClient: d.Error == "incorrect_client_credentials"}
	}
	if d.DeviceCode == "" || d.URI != "https://github.com/login/device" || d.Expires <= 0 {
		return RepoCredential{}, &DeviceAuthorizationError{Err: fmt.Errorf("invalid device authorization response")}
	}
	display(d.URI, d.UserCode)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(min(d.Expires, 1800))*time.Second)
	defer cancel()
	interval := time.Duration(max(d.Interval, 5)) * time.Second
	for {
		if err := c.waitForPoll(ctx, interval); err != nil {
			return RepoCredential{}, err
		}
		var t tokenResponse
		if err := c.jsonRequest(ctx, "POST", "https://github.com/login/oauth/access_token", map[string]string{"client_id": app.ClientID, "device_code": d.DeviceCode, "grant_type": "urn:ietf:params:oauth:grant-type:device_code"}, &t); err != nil {
			return RepoCredential{}, fmt.Errorf("complete GitHub App device authorization: %w", err)
		}
		switch t.Error {
		case "":
			return repoCredential(app, t)
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied", "expired_token":
			return RepoCredential{}, fmt.Errorf("repository authorization %s", t.Error)
		default:
			return RepoCredential{}, fmt.Errorf("repository authorization failed")
		}
	}
}
func (c *Client) User(ctx context.Context) (int64, string, error) {
	var u struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Type  string `json:"type"`
	}
	err := c.jsonRequest(ctx, "GET", "https://api.github.com/user", nil, &u)
	if err != nil {
		return 0, "", err
	}
	if u.ID <= 0 || !sharing.ValidOwner(u.Login) || (u.Type != "" && u.Type != "User") {
		return 0, "", fmt.Errorf("invalid GitHub user identity")
	}
	return u.ID, u.Login, nil
}

// ValidateRepoAccess refuses broader installations, rather than merely promising
// to avoid using their permissions. No OAuth repo/public_repo token is requested.
func (c *Client) ValidateRepoAccess(ctx context.Context) (RepoIdentity, error) {
	var empty RepoIdentity
	if c.Token == "" || c.AppID <= 0 {
		return empty, fmt.Errorf("repository authentication required; run termbacktime auth --storage repo")
	}
	owner, login, err := c.User(ctx)
	if err != nil {
		return empty, err
	}
	var grants struct {
		Total         int `json:"total_count"`
		Installations []struct {
			ID      int64 `json:"id"`
			AppID   int64 `json:"app_id"`
			Account struct {
				ID int64 `json:"id"`
			} `json:"account"`
			Selection   string            `json:"repository_selection"`
			SuspendedAt *string           `json:"suspended_at"`
			Permissions map[string]string `json:"permissions"`
		} `json:"installations"`
	}
	if err = c.jsonRequest(ctx, "GET", "https://api.github.com/user/installations?per_page=100", nil, &grants); err != nil {
		return empty, fmt.Errorf("a user token from the configured GitHub App is required: %w", err)
	}
	if grants.Total != 1 || len(grants.Installations) != 1 {
		return empty, fmt.Errorf("install the GitHub App on your account for only TBT-Recordings; additional installations are not supported")
	}
	g := grants.Installations[0]
	if g.AppID != c.AppID || g.Account.ID != owner || g.Selection != "selected" || g.SuspendedAt != nil || g.Permissions["contents"] != "write" || g.Permissions["metadata"] != "read" {
		return empty, fmt.Errorf("GitHub App must grant Contents read/write and Metadata read for only TBT-Recordings")
	}
	for name, value := range g.Permissions {
		if name != "contents" && name != "metadata" && value != "" && value != "none" {
			return empty, fmt.Errorf("GitHub App requests additional permissions; use only Contents write and Metadata read")
		}
	}
	var repositories struct {
		Total        int            `json:"total_count"`
		Repositories []RepoIdentity `json:"repositories"`
	}
	if err = c.jsonRequest(ctx, "GET", fmt.Sprintf("https://api.github.com/user/installations/%d/repositories?per_page=100", g.ID), nil, &repositories); err != nil {
		return empty, err
	}
	if repositories.Total != 1 || len(repositories.Repositories) != 1 {
		return empty, fmt.Errorf("select only TBT-Recordings in the GitHub App installation settings")
	}
	selected := repositories.Repositories[0]
	if selected.Owner.ID != owner || !strings.EqualFold(selected.Name, sharing.Repository) {
		return empty, fmt.Errorf("the selected repository must be your TBT-Recordings")
	}
	repo, err := c.Repository(ctx, login)
	if err != nil {
		return empty, err
	}
	if repo.ID != selected.ID || repo.Owner.ID != owner {
		return empty, fmt.Errorf("repository ownership changed")
	}
	return repo, nil
}

// RepoCredentials refreshes under a filesystem lock, then atomically rotates both
// tokens. Device-flow refresh needs no app secret or hosted authentication broker.
func (c *Client) RepoCredentials(ctx context.Context, path string) (RepoCredential, error) {
	var credential RepoCredential
	unlock, err := lockRepoCredentials(ctx, path)
	if err != nil {
		return credential, err
	}
	defer unlock()
	cfg, err := config.Load(path)
	if err != nil {
		return credential, err
	}
	if json.Unmarshal(cfg["repo_auth"], &credential) != nil || !credential.App.Valid() || credential.AccessToken == "" {
		return credential, fmt.Errorf("run termbacktime auth --storage repo to authorize TBT-Recordings")
	}
	if credential.ExpiresAt == 0 && credential.RefreshToken == "" {
		return credential, nil
	}
	if credential.ExpiresAt > time.Now().Add(time.Minute).Unix() {
		return credential, nil
	}
	if credential.RefreshToken == "" || credential.RefreshExpiresAt <= time.Now().Unix() {
		return RepoCredential{}, fmt.Errorf("repository login expired; run termbacktime auth --storage repo")
	}
	// A refresh token is single-use. Persist its consumption before sending so a
	// crash or ambiguous network failure cannot replay a rotated credential.
	delete(cfg, "repo_auth")
	if err = cfg.Save(path); err != nil {
		return RepoCredential{}, err
	}
	var response tokenResponse
	err = c.jsonRequest(ctx, "POST", "https://github.com/login/oauth/access_token", map[string]string{"client_id": credential.App.ClientID, "grant_type": "refresh_token", "refresh_token": credential.RefreshToken}, &response)
	if err != nil || response.Error != "" {
		return RepoCredential{}, fmt.Errorf("could not refresh repository credentials; run termbacktime auth --storage repo")
	}
	credential, err = repoCredential(credential.App, response)
	if err != nil {
		return credential, err
	}
	cfg["repo_auth"], err = json.Marshal(credential)
	if err != nil {
		return RepoCredential{}, err
	}
	if err = cfg.Save(path); err != nil {
		return RepoCredential{}, fmt.Errorf("could not save refreshed credentials; run auth again: %w", err)
	}
	return credential, nil
}

func SaveRepoCredential(path string, credential RepoCredential) error {
	unlock, err := lockRepoCredentials(context.Background(), path)
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	cfg["repo_auth"], err = json.Marshal(credential)
	if err != nil {
		return err
	}
	return cfg.Save(path)
}

// RemoveRepositoryLogin deliberately retains the independent Gist credential.
func RemoveRepositoryLogin(path string) error {
	unlock, err := lockRepoCredentials(context.Background(), path)
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	delete(cfg, "repo_auth")
	return cfg.Save(path)
}

func lockRepoCredentials(ctx context.Context, path string) (func(), error) {
	if err := config.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	fd, err := unix.Open(path+".repo-auth.lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { _ = unix.Flock(fd, unix.LOCK_UN); _ = unix.Close(fd) }, nil
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			_ = unix.Close(fd)
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = unix.Close(fd)
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
