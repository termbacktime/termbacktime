// Copyright (c) 2019-2022 Louis T. - contact@lou.ist

// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:

// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package cmd // import "louist.dev/termbacktime/cmd"

import (
	"fmt"
	"os"

	au "github.com/logrusorgru/aurora"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// The default application handler
var root = &cobra.Command{
	Use:     Application,
	Version: Version,
	Short:   versionTemplate,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if ok := skipConfig[cmd.CalledAs()]; !ok {
			if version, outdated := InitConfig(func() (string, bool) {
				if !viper.IsSet("version-check") || viper.GetBool("version-check") {
					return VersionCheck(Version)
				}
				return "", false
			}); outdated {
				fmt.Println(au.Sprintf(au.Bold(au.Green("A new version of %s is available: %s (current %s)\r\n")), Application, version, Version))
			}
		}
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		// Show help by default if no commands are provided
		return cmd.Help()
	},
}

// Execute runs the recording function by default.
func Execute() {
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	root.SetVersionTemplate(versionTemplate)
	root.Flags().BoolP("open", "", false, "open recording playback in default browser after save")
	root.Flags().StringVar(&GithubToken, "token", "", "use the specified GitHub authentication token")
	root.PersistentFlags().StringVar(&cfgFile, "config", fmt.Sprintf("%s%s.%s.%s", HomeDir, string(os.PathSeparator), Application, ConfigType), "config file")
}
