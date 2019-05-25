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
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/caarlos0/spin"
	"github.com/kr/pty"
	au "github.com/logrusorgru/aurora"
	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh/terminal"
)

// Setting storage.
var (
	cfgFile      string
	Closed       bool
	GistResponse map[string]interface{}
	GithubToken  string
	GithubLogin  string
	HomeDir      = getHome()
	Instructions []Lines
	spinner      = spin.New("%s Working...")
)

func (r *Recording) setTerminalDimensions(init bool) {
	if Width, Height, err := terminal.GetSize(int(os.Stdout.Fd())); err != nil {
		log.Println(err)
	} else {
		if init == true {
			r.Sizes = []int{Width, Height}
		} else {
			Instructions = append(Instructions, Lines{
				Command: "s",
				Sizes:   []int{Width, Height},
			})
		}
	}
}

// TODO: Improve shell detection.
func shell(cmd *cobra.Command) string {
	shell := cmd.Flag("shell").Value.String()
	if len(shell) > 0 {
		return shell
	} else if os.Getenv("SHELL") != "" {
		return os.Getenv("SHELL")
	}
	return "/bin/bash"
}

func compress(rec []Lines) (string, error) {
	cmds, err := json.Marshal(rec)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	if _, err := gz.Write([]byte(cmds)); err != nil {
		return "", err
	}
	if err := gz.Flush(); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(b.Bytes()), nil
}

func upload(rec Recording, cmd *cobra.Command) error {
	spinner = spin.New("%s Uploading to Gist...")
	spinner.Set(spin.Box1)
	spinner.Start()
	defer spinner.Stop()

	compressed, err := compress(Instructions)
	if err != nil {
		return Error(fmt.Errorf("gzip failed: %v", err))
	}

	rec.Pack = compressed
	jsn, err := json.MarshalIndent(rec, "", "\t")
	if err != nil {
		return Error(fmt.Errorf("could not generate JSON: %v", err))
	}

	file, err := json.Marshal(Gist{
		Description: fmt.Sprintf("Created with %s", PlaybackURL),
		GistFile: map[string]GistFile{
			"terminal-recording.json": GistFile{string(jsn)},
		},
		Public: false,
	})
	if err != nil {
		return Error(fmt.Errorf("could not generate Gist: %v", err))
	}

	req, err := http.NewRequest("POST", GistAPI, bytes.NewBuffer(file))
	if err != nil {
		return Error(fmt.Errorf("cannot create request: %v", err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("token %s", GithubToken))

	client := http.Client{}
	response, err := client.Do(req)
	if err != nil {
		return Error(fmt.Errorf("request HTTP error: %v", err))
	}
	defer response.Body.Close()
	err = json.NewDecoder(response.Body).Decode(&GistResponse)
	if err != nil {
		return Error(fmt.Errorf("response JSON error: %v", err))
	}

	spinner.Stop()

	if _, ok := GistResponse["html_url"]; !ok {
		fmt.Println(au.Sprintf(au.Bold(au.Red("\r\nAPI Error: %v\r\n")), GistResponse["message"]))

		if a, ok := GistResponse["errors"]; ok {
			for i, m := range a.([]interface{}) {
				for k, v := range m.(map[string]interface{}) {
					fmt.Printf("%d %s: %s\r\n", i, k, v)
				}
			}
		}

		return Error(fmt.Errorf("please check authentication: %s/auth/", PlaybackURL))
	}

	fmt.Println(au.Sprintf(au.Bold("\r\nGist: %s"), GistResponse["html_url"]))
	fmt.Println(au.Sprintf(au.Bold("Playback: %s/p/%s"), PlaybackURL, GistResponse["id"]))

	// Update the Gist description, we don't care about errors here.
	if patch, err := json.Marshal(Gist{Description: fmt.Sprintf("Created with %s/p/%s", PlaybackURL, GistResponse["id"])}); err == nil {
		if req, err = http.NewRequest("PATCH", fmt.Sprintf("%s/%s", GistAPI, GistResponse["id"]), bytes.NewBuffer(patch)); err == nil {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json")
			req.Header.Set("Authorization", fmt.Sprintf("token %s", GithubToken))
			client = http.Client{}
			_, _ = client.Do(req)
		}
	}

	return nil
}

// Error stops the spinner whenever an error is thrown.
func Error(err error) error {
	spinner.Stop()
	return fmt.Errorf("%v\r\n ", err)
}

var recordCmd = &cobra.Command{
	Use:   Application,
	Short: fmt.Sprintf("Terminal Recorder - v%s-%s - %s/", Version, Revision, PlaybackURL),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(GithubToken) <= 0 {
			GithubToken = viper.GetString("token")
			if len(GithubToken) <= 0 {
				return Error(fmt.Errorf("missing GitHub authentication token"))
			}
		}
		GithubLogin = viper.GetString("login")
		if len(GithubLogin) > 0 {
			fmt.Println(au.Sprintf(au.Bold("Initiating terminal recording for %s..."), GithubLogin))
		} else {
			fmt.Println(au.Bold("Initiating terminal recording..."))
		}

		// Start recording Object.
		ccmd := shell(cmd)
		rec := Recording{
			Info: Info{
				Arch: runtime.GOARCH,
				OS:   runtime.GOOS,
				Go:   runtime.Version(),
			},
			Started: time.Now().Unix(),
			Title:   ccmd,
		}
		rec.setTerminalDimensions(true)

		// Start PTY.
		fmt.Println(au.Sprintf(au.Bold("Launching PTY session (%s)..."), ccmd))

		pcmd := exec.Command(ccmd)
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
					if err == io.EOF {
						err = nil
					} else {
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
				if err == io.EOF {
					err = nil
				} else {
					log.Println(err)
				}
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
		fmt.Printf(au.Sprintf(au.Bold("\r\nSave to Gist? [y/n]: ")))
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
						fmt.Printf(au.Sprintf(au.Bold("\r\nSave to Gist? [y/n]: ")))
					}
				}
			case <-time.After(10 * time.Second):
				fmt.Println(au.Bold("\r\n[bold]Ten second timeout hit, exiting..."))
				break stdinloop
			}
		}
		return nil
	},
}

// Execute runs the recording function.
func Execute() {
	if err := recordCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	viper.SetConfigType(ConfigType)
	cobra.OnInitialize(initConfig)
	recordCmd.Version = Version
	recordCmd.SetVersionTemplate(fmt.Sprintf("%s - version=%s revision=%s (%s)\r\n", Application, Version, Revision, runtime.Version()))
	recordCmd.Flags().StringP("shell", "s", os.Getenv("SHELL"), "set the shell to use for recording")
	recordCmd.PersistentFlags().StringVar(&GithubToken, "token", "", "use the specified GitHub authentication token")
	recordCmd.PersistentFlags().StringVar(&cfgFile, "config", fmt.Sprintf("%s%s.termbacktime.%s", HomeDir, string(os.PathSeparator), ConfigType), "config file")
}

func getHome() string {
	home, err := homedir.Dir()
	if err != nil {
		fmt.Println(au.Sprintf(au.Bold(au.Red("Could not find home dir: %v\r\n")), err))
		os.Exit(1)
	}
	return home
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.SetConfigName(".termbacktime")
		viper.AddConfigPath(HomeDir)
		viper.AddConfigPath(".")
	}
	viper.AutomaticEnv()
	if err := viper.ReadInConfig(); err == nil {
		fmt.Println(au.Sprintf(au.Bold("Loaded config: %s"), viper.ConfigFileUsed()))
	}
}
