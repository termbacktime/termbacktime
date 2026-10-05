package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/termbacktime/termbacktime/internal/config"
	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/live"
)

func newDoctor(o *options) *cobra.Command {
	var jsonOutput, probe, relay bool
	c := &cobra.Command{Use: "doctor", Short: "Check configuration and optionally verify live delivery", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		checks := map[string]any{}
		root, err := config.DataDir(o.dataDir)
		if err != nil {
			return err
		}
		checks["data_dir"] = root
		checks["config_path"] = o.configPath
		permissions := map[string]string{}
		for _, path := range []string{root, filepath.Join(root, "recordings"), filepath.Join(root, "library"), filepath.Join(root, "shares"), o.configPath} {
			st, err := os.Stat(path)
			if os.IsNotExist(err) {
				permissions[path] = "not created"
			} else if err != nil {
				permissions[path] = "unavailable"
			} else {
				permissions[path] = fmt.Sprintf("%04o", st.Mode().Perm())
			}
		}
		checks["permissions"] = permissions
		cfg, err := o.credentials()
		if err != nil {
			checks["configuration_error"] = err.Error()
		} else {
			checks["github_token_configured"] = cfg.String("token") != ""
		}
		endpoint, e := config.ValidateSiteURL(o.endpoint)
		if e != nil {
			checks["endpoint_error"] = e.Error()
		} else {
			checks["endpoint"] = endpoint
			client := gh.New("")
			client.SiteURL = endpoint
			client.HTTP = &http.Client{Timeout: 10 * time.Second}
			if err := client.CheckEncryption(c.Context()); err != nil {
				checks["compatibility_error"] = err.Error()
			} else {
				checks["encrypted_uploads_supported"] = true
			}
		}
		var probeErr error
		if relay && !probe {
			return fmt.Errorf("--relay-only requires --live")
		}
		if probe {
			result, err := live.Probe(c.Context(), o.endpoint, 1, relay, false)
			probeErr = err
			if err != nil {
				checks["live_error"] = err.Error()
			} else {
				checks["live"] = result
			}
		}
		if jsonOutput {
			if err := json.NewEncoder(c.OutOrStdout()).Encode(checks); err != nil {
				return err
			}
		} else {
			b, _ := json.MarshalIndent(checks, "", "  ")
			fmt.Fprintln(c.OutOrStdout(), string(b))
		}
		return probeErr
	}}
	c.Flags().BoolVar(&jsonOutput, "json", false, "print machine-readable checks")
	c.Flags().BoolVar(&probe, "live", false, "verify delivery using a temporary synthetic room")
	c.Flags().BoolVar(&relay, "relay-only", false, "require TURN relaying for the live check")
	return c
}
