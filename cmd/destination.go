package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/termbacktime/termbacktime/internal/config"
	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/sharing"
)

func (o *options) uploadStorage(explicit string) (string, error) {
	if explicit != "" {
		if explicit != sharing.Gist && explicit != sharing.Repo {
			return "", fmt.Errorf("--storage must be gist or repo")
		}
		return explicit, nil
	}
	cfg, err := config.Load(o.configPath)
	if err != nil {
		return "", err
	}
	if saved := cfg.String("upload_storage"); saved == sharing.Gist || saved == sharing.Repo {
		return saved, nil
	}
	return sharing.Repo, nil
}
func (o *options) configureBackends(base *gh.Client) {
	base.Select = func(ctx context.Context, storage string) (*gh.Client, error) {
		client := gh.New("")
		client.HTTP = base.HTTP
		client.SiteURL = base.SiteURL
		client.Storage = storage
		cfg, err := o.credentials()
		if err != nil {
			return nil, err
		}
		if storage == sharing.Gist || storage == "" {
			client.Token = cfg.String("token")
			if client.Token == "" {
				return nil, fmt.Errorf("Gist authentication required; run termbacktime auth --storage gist")
			}
			return client, nil
		}
		if override := cfg.String("token"); o.token != "" || os.Getenv("TERMBACKTIME_TOKEN") != "" {
			// Discovery may cache public IDs; never persist a runtime token override.
			saved, err := config.Load(o.configPath)
			if err != nil {
				return nil, err
			}
			app, err := o.repoApp(ctx, saved, false)
			if err != nil {
				return nil, err
			}
			client.Token = override
			client.AppID = app.ID
		} else {
			credential, err := base.RepoCredentials(ctx, o.configPath)
			if err != nil {
				return nil, err
			}
			client.Token = credential.AccessToken
			client.AppID = credential.App.ID
		}
		if _, err = client.ValidateRepoAccess(ctx); err != nil {
			return nil, err
		}
		return client, nil
	}
}
func (o *options) repoApp(ctx context.Context, cfg config.Config, refresh bool) (gh.RepoApp, error) {
	var app gh.RepoApp
	if !refresh && json.Unmarshal(cfg["github_repo_app"], &app) == nil && app.Valid() {
		return app, nil
	}
	origin, err := config.ValidateSiteURL(o.endpoint)
	if err != nil {
		return app, err
	}
	request, err := http.NewRequestWithContext(ctx, "GET", origin+"/api/v1/config", nil)
	if err != nil {
		return app, err
	}
	if refresh {
		request.Header.Set("Cache-Control", "no-cache")
	}
	res, err := gh.New("").HTTP.Do(request)
	if err != nil {
		return app, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return app, fmt.Errorf("website configuration returned HTTP %d", res.StatusCode)
	}
	var data struct {
		App gh.RepoApp `json:"githubRepoApp"`
	}
	// Limit the public discovery response; it must never contain credentials.
	decoder := json.NewDecoder(http.MaxBytesReader(nil, res.Body, 16384))
	if err = decoder.Decode(&data); err != nil {
		return app, err
	}
	app = data.App
	if !app.Valid() {
		return app, fmt.Errorf("configure the repository GitHub App on the website before using repo storage; Gists remain available with --storage gist")
	}
	cfg["github_repo_app"], _ = json.Marshal(app)
	return app, cfg.Save(o.configPath)
}
func (o *options) authorizeRepository(c *cobra.Command, cfg config.Config, token string, refresh bool) error {
	app, err := o.repoApp(c.Context(), cfg, refresh)
	if err != nil {
		return err
	}
	client := gh.New("")
	var credential gh.RepoCredential
	if token != "" {
		credential = gh.RepoCredential{App: app, AccessToken: token}
	} else {
		authorize := func() (gh.RepoCredential, error) {
			return client.AuthorizeRepo(c.Context(), app, func(uri, code string) {
				fmt.Fprintf(c.OutOrStdout(), "Open %s and enter code %s\n", uri, code)
				if o.open {
					_ = openURL(uri)
				}
			})
		}
		credential, err = authorize()
		var start *gh.DeviceAuthorizationError
		if !refresh && errors.As(err, &start) && start.InvalidClient {
			fresh, discoveryErr := o.repoApp(c.Context(), cfg, true)
			if discoveryErr != nil {
				return fmt.Errorf("%w; refreshing website configuration failed: %v", err, discoveryErr)
			}
			if fresh.ClientID != app.ClientID {
				fmt.Fprintln(c.ErrOrStderr(), "GitHub App configuration changed; retrying with the current Client ID.")
				app = fresh
				credential, err = authorize()
			}
		}
		if err != nil {
			return err
		}
	}
	client.Token = credential.AccessToken
	client.AppID = app.ID
	client.Storage = sharing.Repo
	owner, login, err := client.User(c.Context())
	if err != nil {
		return fmt.Errorf("read the authorized GitHub account: %w", err)
	}
	continueAt := func(link, message string) error {
		fmt.Fprintf(c.OutOrStdout(), "%s\n%s\n", message, link)
		if o.open {
			_ = openURL(link)
		}
		if !interactiveUpload(c) {
			return fmt.Errorf("finish repository setup on GitHub, then run termbacktime auth --storage repo")
		}
		fmt.Fprint(c.OutOrStdout(), "Press Enter after completing this step (Ctrl+C cancels): ")
		done := make(chan error, 1)
		go func() { _, e := bufio.NewReader(c.InOrStdin()).ReadString('\n'); done <- e }()
		select {
		case <-c.Context().Done():
			return c.Context().Err()
		case e := <-done:
			return e
		}
	}
	repo, err := client.Repository(c.Context(), login)
	if err != nil {
		if failure, ok := err.(*gh.HTTPError); !ok || failure.Status != 404 {
			return err
		}
		if err = continueAt("https://github.com/new?name=TBT-Recordings", "Create a public TBT-Recordings repository under "+login+". TermBackTime requests no repository-creation permission."); err != nil {
			return err
		}
		repo, err = client.Repository(c.Context(), login)
		if err != nil {
			return err
		}
	}
	if _, err = client.ValidateRepoAccess(c.Context()); err != nil {
		if err = continueAt(app.InstallURL(owner, repo.ID), "Install the GitHub App on your personal account. Choose Only select repositories and select only TBT-Recordings. Grant Contents read/write and Metadata read."); err != nil {
			return err
		}
		if _, err = client.ValidateRepoAccess(c.Context()); err != nil {
			return err
		}
	}
	if strings.ContainsAny(credential.AccessToken, "\r\n") {
		return fmt.Errorf("invalid credential")
	}
	if err = gh.SaveRepoCredential(o.configPath, credential); err != nil {
		return err
	}
	fmt.Fprintln(c.OutOrStdout(), "GitHub access saved for "+login+"/TBT-Recordings only.")
	return nil
}
