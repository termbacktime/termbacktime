package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/termbacktime/termbacktime/internal/config"
	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/recording"
)

func newAuth(o *options) *cobra.Command {
	var stdin, logout, refreshClientID bool
	var token, storage string
	c := &cobra.Command{
		Use:   "auth",
		Short: "Authorize the selected GitHub recording storage",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if storage != "" && storage != "repo" && storage != "gist" {
				return fmt.Errorf("--storage must be gist or repo")
			}
			// Cache public discovery without persisting runtime token overrides
			cfg, err := config.Load(o.configPath)
			if err != nil {
				return err
			}
			if logout {
				if storage != "repo" {
					delete(cfg, "token")
				}
				if storage != "gist" {
					if err := gh.RemoveRepositoryLogin(o.configPath); err != nil {
						return err
					}
					delete(cfg, "repo_auth")
				}
				return cfg.Save(o.configPath)
			}
			if stdin {
				b, err := recording.ReadBounded(c.InOrStdin(), 4096)
				if err != nil {
					return err
				}
				token = strings.TrimSpace(string(b))
			}
			destination, err := o.uploadStorage(storage)
			if err != nil {
				return err
			}
			if destination == "repo" {
				return o.authorizeRepository(c, cfg, token, refreshClientID)
			}
			if token == "" {
				clientID, err := o.githubClientID(c.Context(), cfg, refreshClientID)
				if err != nil {
					return err
				}
				token, err = gh.New("").Authorize(c.Context(), clientID, func(uri, code string) {
					fmt.Fprintf(c.OutOrStdout(), "Open %s and enter code %s\n", uri, code)
					if o.open {
						_ = openURL(uri)
					}
				})
				if err != nil {
					return err
				}
			}
			if strings.ContainsAny(token, "\r\n") || len(token) < 10 {
				return fmt.Errorf("invalid token")
			}
			cfg.Set("token", token)
			if err = cfg.Save(o.configPath); err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), "GitHub credentials saved.")
			return nil
		},
	}
	c.Flags().StringVar(&storage, "storage", "", "storage to authorize: repo or gist (last upload choice, otherwise repo)")
	c.Flags().BoolVar(&stdin, "token-stdin", false, "read token from standard input")
	c.Flags().BoolVar(&logout, "logout", false, "remove saved token")
	c.Flags().BoolVar(&refreshClientID, "refresh-client-id", false, "refresh the cached GitHub client ID from the Worker")
	c.Flags().StringVar(&token, "set-token", "", "save supplied token (prefer --token-stdin)")
	c.MarkFlagsMutuallyExclusive("token-stdin", "set-token", "logout", "refresh-client-id")
	return c
}

// Cache Worker discovery before device authorization so retries do not repeat the lookup
func (o *options) githubClientID(ctx context.Context, cfg config.Config, refresh bool) (string, error) {
	if !refresh {
		if value := strings.TrimSpace(os.Getenv("TERMBACKTIME_GITHUB_CLIENT_ID")); value != "" {
			return value, nil
		}
		if value := cfg.String("github_client_id"); value != "" {
			return value, nil
		}
	}

	endpoint, err := config.ValidateSiteURL(o.endpoint)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"/api/v1/config", nil)
	if err != nil {
		return "", err
	}
	if refresh {
		req.Header.Set("Cache-Control", "no-cache")
	}

	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch GitHub authorization configuration: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch GitHub authorization configuration: Worker returned HTTP %d", res.StatusCode)
	}

	b, err := recording.ReadBounded(res.Body, 4096)
	if err != nil {
		return "", fmt.Errorf("read GitHub authorization configuration: %w", err)
	}
	var data struct {
		ClientID string `json:"githubClientId"`
	}
	if err := json.Unmarshal(b, &data); err != nil {
		return "", fmt.Errorf("Worker returned invalid GitHub authorization configuration")
	}
	clientID := strings.TrimSpace(data.ClientID)
	if clientID == "" {
		return "", fmt.Errorf("GitHub device authorization is not configured; configure GITHUB_CLIENT_ID on the Worker or use auth --token-stdin")
	}
	if len(clientID) > 128 || strings.ContainsAny(clientID, " \t\r\n") {
		return "", fmt.Errorf("Worker returned an invalid GitHub client ID")
	}

	cfg.Set("github_client_id", clientID)
	if err := cfg.Save(o.configPath); err != nil {
		return "", fmt.Errorf("cache GitHub client ID: %w", err)
	}
	return clientID, nil
}
