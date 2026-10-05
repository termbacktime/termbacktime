package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/termbacktime/termbacktime/internal/buildinfo"
	"github.com/termbacktime/termbacktime/internal/config"
	"github.com/termbacktime/termbacktime/internal/updates"
)

func (o *options) checkUpdates(c *cobra.Command, standalone bool) error {
	installed := buildinfo.Tag()
	if !o.checkUpdate && !updates.ValidVersion(installed) {
		return nil
	}
	directory, err := config.DataDir(o.dataDir)
	if err != nil {
		return err
	}
	result, err := o.updates.Check(c.Context(), directory, o.checkUpdate)
	if err != nil {
		if standalone {
			return err
		}
		if o.checkUpdate {
			fmt.Fprintln(c.ErrOrStderr(), "Update check:", err)
		}
		return nil // Update availability never prevents local work.
	}
	if result.Cached && !o.checkUpdate {
		return nil
	}
	out := c.ErrOrStderr()
	if standalone {
		out = c.OutOrStdout()
	}
	if updates.Newer(result.Tag, installed) {
		fmt.Fprintf(out, "Update available: %s (installed %s)\n%s/tag/%s\n", result.Tag, installed, updates.ReleasesURL, result.Tag)
	} else if o.checkUpdate {
		fmt.Fprintf(out, "Latest published release: %s (installed %s)\n", result.Tag, installed)
	}
	return nil
}
