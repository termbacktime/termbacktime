package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/termbacktime/termbacktime/internal/config"
	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/manage"
)

func newManage(o *options) *cobra.Command {
	c := &cobra.Command{Use: "manage", Short: "Browse recordings, manage uploads, and play saved sessions", Long: "Choose Local recordings, GitHub Gists, or TBT-Recordings inside the manager. Press b to choose again.\n\nUse f to sort/filter, Enter to preview, v to fully verify, a for saved uploads,\nJ to view the queue, U to run it, H for opt-in history, S for storage, and ?\nfor all shortcuts. Queue uploads continue while browsing and stop on exit.", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if !interactiveUpload(c) {
			return fmt.Errorf("manage requires interactive terminal input and output; use list --json or info for scripting")
		}
		lib, err := o.library()
		if err != nil {
			return err
		}
		cfg, err := o.credentials()
		if err != nil {
			return err
		}
		store := &manage.Store{Library: lib, GitHub: gh.New(cfg.String("token"))}
		if o.endpoint != "" {
			store.GitHub.SiteURL, err = config.ValidateSiteURL(o.endpoint)
			if err != nil {
				return err
			}
		}
		o.configureBackends(store.GitHub)
		backend := &manageBackend{Store: store, options: o}
		return manage.Run(c.Context(), c.InOrStdin(), c.OutOrStdout(), backend, false, store.LoadPlayback)
	}}

	return c
}
