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
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/caarlos0/spin"
	"github.com/spf13/cobra"
)

// playCmd represents the auth command.
var playCmd = &cobra.Command{
	Use:   "play [hash]",
	Short: "Play terminal recordings",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		spinner = spin.New("%s Fetching Gist...")
		spinner.Set(spin.Box1)
		spinner.Start()
		defer spinner.Stop()

		match, _ := regexp.MatchString("^[a-fA-F0-9]{32}$", args[0])
		if match == false {
			return Error(fmt.Errorf("%s is not a valid Gist hash", args[0]))
		}

		u := fmt.Sprintf("%v/%v", GistAPI, args[0])
		req, err := http.NewRequest("GET", u, nil)
		if err != nil {
			return Error(err)
		}

		var gist GetGist

		var client = &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return Error(err)
		}

		err = json.NewDecoder(resp.Body).Decode(&gist)
		if err != nil {
			return Error(fmt.Errorf("response JSON error: %v", err))
		}

		var rec Recording
		err = json.NewDecoder(strings.NewReader(gist.Files[GistFileName].Content)).Decode(&rec)
		if err != nil {
			return Error(fmt.Errorf("could not read %s: %v", GistFileName, err))
		}

		data, err := uncompress(rec.Pack)
		if err != nil {
			return Error(err)
		}

		spinner.Stop()

		var lines []Lines
		err = json.NewDecoder(strings.NewReader(data)).Decode(&lines)
		if err != nil {
			return Error(fmt.Errorf("could not load recording data from Gist: %v", err))
		}

		fmt.Print("Starting playback!\r\n\r\n")
		for _, line := range lines {
			if line.Command == "s" {
				os.Stdout.WriteString(fmt.Sprintf("\033[8;%d;%dt", line.Sizes[1], line.Sizes[0]))
			} else {
				if line.Time > 0 {
					time.Sleep(time.Duration(line.Time) * time.Millisecond)
				}
				for _, out := range line.Lines {
					os.Stdout.WriteString(fmt.Sprintf("%v", out))
				}
			}
		}
		fmt.Println("\r\nPlayback finished.")

		return nil
	},
}

func init() {
	recordCmd.AddCommand(playCmd)
}
