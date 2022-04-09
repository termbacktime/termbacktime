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
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
	au "github.com/logrusorgru/aurora"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	terminal "golang.org/x/term"
)

var recordCmd = &cobra.Command{
	Use:   "record",
	Short: "Start terminal recording",
	RunE: func(cmd *cobra.Command, args []string) error {
		shellPath := shell() // Detect shell
		if len(GithubToken) <= 0 {
			GithubToken = viper.GetString("token")
			if len(GithubToken) <= 0 {
				return Error(fmt.Errorf("missing GitHub authentication token"))
			}
		}
		if len(Username) > 0 {
			fmt.Println(au.Sprintf(au.Bold("Initiating terminal recording for %s..."), Username))
		} else {
			fmt.Println(au.Bold("Initiating terminal recording..."))
		}

		// Start recording Object.
		rec := Recording{
			Info: Info{
				Arch: runtime.GOARCH,
				OS:   runtime.GOOS,
				Go:   runtime.Version(),
			},
			Started: time.Now().Unix(),
			Title:   shellPath,
		}
		rec.setTerminalDimensions(true)

		// Start PTY.
		fmt.Println(au.Sprintf(au.Bold("Launching PTY session (%s)..."), shellPath))

		pcmd := exec.Command(shellPath)
		ptmx, err := pty.Start(pcmd)
		if err != nil {
			return Error(err)
		}

		go func() {
			defer func() {
				ptmx.Close()
			}()
			pcmd.Wait()
			Closed = true
		}()

		// Terminal resize.
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGWINCH)
		go func() {
			for range ch {
				if err := pty.InheritSize(os.Stdin, ptmx); err != nil {
					log.Printf("error resizing pty: %v", err)
				} else {
					rec.setTerminalDimensions(false)
				}
			}
		}()
		ch <- syscall.SIGWINCH

		// Set terminal to raw.
		oldState, err := terminal.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return Error(err)
		}
		defer func() { _ = terminal.Restore(int(os.Stdin.Fd()), oldState) }()

		// Write STDIN to PTY.
		chn := make(chan string)
		go func(ch chan string) {
			bufin := make([]byte, 2048)
			for {
				nr, err := os.Stdin.Read(bufin)
				if err != nil {
					if err != io.EOF {
						log.Println(err)
					}
					break
				}
				if !Closed {
					if _, err = ptmx.Write(bufin[0:nr]); err != nil {
						log.Println(err)
						break
					}
				} else {
					chn <- string(bufin[0:nr])
				}
			}
		}(chn)

		fmt.Println(au.Green(au.Bold("Recording started!\r\n")))

		// Read from the PTY into a buffer.
		rectime := time.Now()
		bufout := make([]byte, 4096)
		lines := []string{}
		for {
			nr, err := ptmx.Read(bufout)
			if err != nil {
				break
			}

			// Record to the JSON object.
			tstamp := int64(time.Since(rectime).Nanoseconds() / int64(time.Millisecond))
			line := string(bufout[0:nr])
			if tstamp == 0 {
				lines = append(lines, line)
			} else {
				if len(lines) > 0 {
					Instructions = append(Instructions, Lines{Lines: lines})
					lines = []string{}
				}
				Instructions = append(Instructions, Lines{
					Time:  tstamp,
					Lines: []string{line},
				})
			}

			// Write to STDOUT
			if _, err = os.Stdout.WriteString(line); err != nil {
				log.Println(err)
				break
			}

			rectime = time.Now()
		}

		// Confirm upload to Gist.
		_ = terminal.Restore(int(os.Stdin.Fd()), oldState)
		fmt.Println(au.Sprintf(au.Bold("\r\nSave to Gist? [y/n]: ")))
		yes := regexp.MustCompile(`^y(es)?$`)
		no := regexp.MustCompile(`^no?$`)

	stdinloop:
		for {
			select {
			case stdin, ok := <-chn:
				if !ok {
					break stdinloop
				} else {
					text := strings.Replace(stdin, "\n", "", -1)
					if yes.MatchString(text) {
						return upload(rec, cmd)
					} else if no.MatchString(text) {
						fmt.Println(au.Sprintf(au.Bold("Canceled!")))
						break stdinloop
					} else {
						fmt.Println(au.Sprintf(au.Bold("\r\nSave to Gist? [y/n]: ")))
					}
				}
			case <-time.After(60 * time.Second):
				fmt.Println(au.Bold("\r\nUpload timeout; exiting..."))
				break stdinloop
			}
		}
		return nil
	},
}

func init() {
	recordCmd.Flags().StringVar(&Shell, "shell", shell(), "shell to use")
	root.AddCommand(recordCmd)
}
