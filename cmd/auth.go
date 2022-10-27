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

package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/websocket"
	au "github.com/logrusorgru/aurora"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage GitHub API authentication",
	Run: func(cmd *cobra.Command, args []string) {
		tkn := cmd.Flag("set-token").Value.String()
		if len(tkn) > 0 {
			saveToken(map[string]interface{}{"token": tkn}, true)
		} else {
			interrupt := make(chan os.Signal, 1)
			signal.Notify(interrupt, os.Interrupt)

			chn := fmt.Sprintf("tbt:%s", uuid())
			client, resp, err := wsDialer.Dial(fmt.Sprintf("%s/live?token=%s", Broker, chn), nil)
			if err != nil {
				fmt.Println(au.Sprintf(au.Red("\nError: %v (status code: %d)\n"), err, resp.StatusCode))
				os.Exit(1)
			}
			defer client.Close()

			AuthURL := fmt.Sprintf("%s/auth/#%s", PlaybackURL, chn)
			open, _ := cmd.Flags().GetBool("open")
			if open {
				browser.OpenURL(AuthURL)
			}
			fmt.Println(au.Sprintf(au.Bold("Please authorize %s with GitHub: %s\r"), Application, AuthURL))

			go func() {
				ticker := time.Tick(time.Second)
				fmt.Print(colorify("Waiting for token; timeout in %d seconds...", 120))
				for i := 119; i >= 0; i-- {
					<-ticker
					fmt.Printf("\r%s", colorify("Waiting for token; timeout in %d seconds...", i))
				}
				fmt.Printf("\r%v\r\n", au.Bold(au.Red("Authentication timed out, please try again!")))
				os.Exit(1)
			}()

			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					var r map[string]interface{}
					if err := client.ReadJSON(&r); err != nil {
						fmt.Println(au.Sprintf(au.Red("\nError: %v\n"), err))
						return
					}
					if r["token"] != nil && len(r["token"].(string)) > 0 {
						fmt.Printf("\r\nLogged in as %s\r\n", r["login"].(string))
						saveToken(r, false)
					}
				}
			}()

			for {
				select {
				case <-done:
				case <-interrupt:
					client.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
					time.Sleep(1 * time.Second)
					return
				}
			}
		}
	},
}

func init() {
	authCmd.Flags().StringP("set-token", "s", "", "manually set a GitHub auth token")
	authCmd.Flags().BoolP("open", "o", false, "open the GitHub auth URL in your browser")
	root.AddCommand(authCmd)
}
