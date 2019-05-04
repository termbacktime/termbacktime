// Copyright (c) 2019 Louis Tarango - me@lou.ist

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
	"crypto/rand"
	"fmt"
	"os"
	"time"

	"github.com/ably/ably-go/ably"
	au "github.com/logrusorgru/aurora"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func uuid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%x-%x%x-%x%x-%x", b[0:4], b[4:6], (b[6]&0x0f)|0x40, b[7:8], (b[8]&0x3f)|0x80, b[9:10], b[10:])
}

func saveToken(tkn string, man bool) {
	if man {
		fmt.Printf("Saving token: %s\r\n", tkn)
	}
	viper.Set("token", tkn)
	if err := viper.WriteConfig(); err == nil {
		fmt.Println(au.Sprintf(au.Bold("Saved config: %s"), viper.ConfigFileUsed()))
		os.Exit(0)
	} else {
		fmt.Println(au.Sprintf(au.Bold(au.Red("Error: %v")), err))
		os.Exit(1)
	}
}

func colorify(str string, t int) string {
	if t >= 11 {
		return au.Sprintf(au.Bold(str), au.Bold(au.Green(t)))
	}
	return au.Sprintf(au.Bold(str), au.Bold(au.Red(t)))
}

// authCmd represents the auth command
var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage GitHub API authentication",
	Run: func(cmd *cobra.Command, args []string) {
		tkn := cmd.Flag("set-token").Value.String()
		if len(tkn) > 0 {
			saveToken(tkn, true)
		} else {
			client, err := ably.NewRealtimeClient(ably.NewClientOptions(AblyToken))
			if err != nil {
				fmt.Println(au.Sprintf(au.Red("Error: %v"), err))
				os.Exit(1)
			}

			chn := fmt.Sprintf("tbt:%s", uuid())
			channel := client.Channels.Get(chn)
			sub, err := channel.Subscribe()
			if err != nil {
				fmt.Println(au.Sprintf(au.Red("Error: %v"), err))
				os.Exit(1)
			}

			fmt.Printf("Please authorize %s with GitHub: %s/auth/#%s\r\n", Application, PlaybackURL, chn)

			go func() {
				ticker := time.Tick(time.Second)
				fmt.Print(colorify("Waiting for token; timeout in %d seconds...", 30))
				for i := 29; i >= 0; i-- {
					<-ticker
					fmt.Printf("\r%s", colorify("Waiting for token; timeout in %d seconds...", i))
				}
				fmt.Printf("\r%v\r\n", au.Bold(au.Red("Authentication timed out, please try again!")))
				os.Exit(1)
			}()

			for msg := range sub.MessageChannel() {
				if data, ok := msg.Data.(map[string]interface{}); ok {
					if data["login"] != "" {
						fmt.Printf("\rLogged in as %s - Token: %s\r\n", data["login"], data["token"])
						saveToken(fmt.Sprintf("%s", data["token"]), false)
					}
				}
			}
		}
	},
}

func init() {
	authCmd.Flags().StringP("set-token", "s", "", "manually set a GitHub auth token")
	recordCmd.AddCommand(authCmd)
}
