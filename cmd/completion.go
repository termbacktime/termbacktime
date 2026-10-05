package cmd

import "github.com/spf13/cobra"

func newCompletion() *cobra.Command {
	return &cobra.Command{
		Use:       "completion [bash|zsh|fish|powershell]",
		Short:     "Generate shell completions",
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(c *cobra.Command, a []string) error {
			switch a[0] {
			case "bash":
				return c.Root().GenBashCompletionV2(c.OutOrStdout(), true)
			case "zsh":
				return c.Root().GenZshCompletion(c.OutOrStdout())
			case "fish":
				return c.Root().GenFishCompletion(c.OutOrStdout(), true)
			default:
				return c.Root().GenPowerShellCompletionWithDesc(c.OutOrStdout())
			}
		},
	}
}
