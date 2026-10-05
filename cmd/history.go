package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/termbacktime/termbacktime/internal/history"
)

func newHistory(o *options) *cobra.Command {
	root := &cobra.Command{Use: "history", Short: "Control opt-in playback positions (no recording contents or links)"}
	for _, action := range []string{"enable", "disable", "clear"} {
		root.AddCommand(&cobra.Command{Use: action, Short: map[string]string{"enable": "Remember interactive playback positions", "disable": "Stop remembering playback positions", "clear": "Clear saved playback positions"}[action], Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
			lib, err := o.library()
			if err != nil {
				return err
			}
			store := history.Store{Root: lib.Root}
			if action == "clear" {
				err = store.Clear()
			} else {
				err = store.Enable(action == "enable")
			}
			if err == nil {
				fmt.Fprintln(c.OutOrStdout(), "Playback history:", action)
			}
			return err
		}})
	}
	return root
}
