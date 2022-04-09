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
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/bits"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/caarlos0/spin"
	au "github.com/logrusorgru/aurora"
	homedir "github.com/mitchellh/go-homedir"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/mod/semver"
	terminal "golang.org/x/term"
)

// shell attempts to return the users terminal shell.
// TODO: Improve shell detection.
func shell() string {
	shellPath := ""
	if len(Shell) > 0 {
		shellPath = Shell
	} else if os.Getenv("SHELL") != "" {
		shellPath = os.Getenv("SHELL")
	}
	if _, err := exec.LookPath(shellPath); err != nil {
		str := "no valid terminal shell provided; use --shell or set SHELL environment variable"
		fmt.Println(au.Sprintf(au.Red("Error: %v\n(error: %s)\n"), str, err))
		os.Exit(1)
	}
	return shellPath
}

// Error stops the spinner whenever an error is thrown.
func Error(err error) error {
	spinner.Stop()
	return fmt.Errorf("%v\r\n ", err)
}

// compress deflates into gzipped (Base64 encoded) data.
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

// uncompress inflates (Base64 encoded) gzipped data.
func uncompress(data string) (string, error) {
	dat, _ := base64.StdEncoding.DecodeString(data)
	b := bytes.NewBuffer(dat)
	var r io.Reader
	r, err := gzip.NewReader(b)
	if err != nil {
		return "", err
	}
	var resB bytes.Buffer
	_, err = resB.ReadFrom(r)
	if err != nil {
		return "", err
	}
	return resB.String(), nil
}

// getHome returns the users home directory path.
func getHome() string {
	home, err := homedir.Dir()
	if err != nil {
		fmt.Println(au.Sprintf(au.Bold(au.Red("Error: could not find home dir: %v\r\n")), err))
		os.Exit(1)
	}
	return home
}

// VersionCheck checks the current version against the semver at termbackti.me/_version
// TODO: Create a manual command to check versions.
func VersionCheck(current string) (string, bool) {
	client := &http.Client{
		Transport: &http.Transport{
			IdleConnTimeout:       5 * time.Second,
			ResponseHeaderTimeout: 5 * time.Second,
		},
	}
	if res, err := client.Get(fmt.Sprintf("%s/_version", PlaybackURL)); err == nil {
		defer res.Body.Close()
		if version, err := ioutil.ReadAll(res.Body); err == nil {
			ver := string(version)
			if semver.Compare(current, ver) == -1 {
				return ver, true
			}
			return ver, false
		}
	}

	return "", false
}

// InitConfig reads in config file and ENV variables if set.
func InitConfig(fn func() (string, bool)) (string, bool) {
	created := false
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
		if !fileExists(cfgFile) {
			viper.Set("version-check", true)
			viper.Set("login", uuid()) // Give a unique default login ID
			if err := viper.WriteConfig(); err == nil {
				created = true
				fmt.Println(au.Sprintf(au.Bold("Created config: %s\n"), viper.ConfigFileUsed()))
			} else {
				fmt.Println(au.Sprintf(au.Bold(au.Red("Error: %v")), err))
				os.Exit(1)
			}
		}
		viper.AutomaticEnv()
		if err := viper.ReadInConfig(); err == nil {
			if !created {
				fmt.Println(au.Sprintf(au.Bold("Loaded config: %s\n"), viper.ConfigFileUsed()))
			}
		} else {
			fmt.Println(au.Sprintf(au.Bold(au.Red("Error: %v")), err))
			os.Exit(1)
		}
		Username = viper.GetString("login")
	} else {
		fmt.Println(au.Bold(au.Red("Error: no configuration provided")))
	}

	return fn()
}

// uuid returns a UUIDv4 token.
func uuid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%x-%x%x-%x%x-%x", b[0:4], b[4:6], (b[6]&0x0f)|0x40, b[7:8], (b[8]&0x3f)|0x80, b[9:10], b[10:])
}

// colorify returns color coded strings, green for success and red for failure.
func colorify(str string, t int) string {
	if t >= 11 {
		return au.Sprintf(au.Bold(str), au.Bold(au.Green(t)))
	}
	return au.Sprintf(au.Bold(str), au.Bold(au.Red(t)))
}

// saveToken saves a GitHub token to the config file.
func saveToken(data map[string]interface{}, man bool) {
	if man {
		fmt.Printf("Saving token: %s\r\n", data["token"].(string))
	}
	viper.Set("token", data["token"].(string))
	if !man {
		if data["login"] != nil {
			viper.Set("login", data["login"].(string))
		}
	}
	if err := viper.WriteConfig(); err == nil {
		fmt.Println(au.Sprintf(au.Bold("\nSaved config: %s"), viper.ConfigFileUsed()))
		os.Exit(0)
	} else {
		fmt.Println(au.Sprintf(au.Bold(au.Red("Error: %v")), err))
		os.Exit(1)
	}
}

// setTerminalDimensions records adds terminal demensions to a recording.
func (r *Recording) setTerminalDimensions(init bool) {
	if Width, Height, err := terminal.GetSize(int(os.Stdout.Fd())); err != nil {
		log.Println(err)
	} else {
		if init {
			r.Sizes = []int{Width, Height}
		} else {
			Instructions = append(Instructions, Lines{
				Command: "s",
				Sizes:   []int{Width, Height},
			})
		}
	}
}

// upload submits recordings to Gist.
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
		Description: fmt.Sprintf("Created with %s/", PlaybackURL),
		GistFiles: map[string]GistFiles{
			GistFileName: {string(jsn)},
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

	PlayURL := fmt.Sprintf("%s/p/%s", PlaybackURL, GistResponse["id"])
	fmt.Println(au.Sprintf("\r\n%s: %s play %s", au.Bold("Recording saved"), Application, GistResponse["id"]))
	fmt.Println(au.Sprintf("%s: %s", au.Bold("Gist"), GistResponse["html_url"]))
	fmt.Println(au.Sprintf("%s: %s", au.Bold("Playback"), PlayURL))

	if cmd.Flags().Changed("open") {
		_ = browser.OpenURL(PlayURL)
	}

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

// ToJSON converts interface to JSON
func ToJSON(obj interface{}) string {
	jsn, _ := json.Marshal(obj)

	return string(jsn)
}

// EncodeOffer encodes WebRTC offer in base64
func EncodeOffer(obj interface{}) (string, error) {
	b, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(b), nil
}

// DecodeAnswer decodes WebRTC answer from base64
func DecodeAnswer(in string, obj interface{}) (interface{}, error) {
	b, err := base64.StdEncoding.DecodeString(in)
	if err != nil {
		return obj, err
	}

	if err = json.Unmarshal(b, obj); err != nil {
		return obj, err
	}

	return obj, nil
}

// FMTTurn converts a string into a TURN URI
func FMTTurn(addrs []string) []string {
	servers := []string{}
	for _, addr := range addrs {
		servers = append(servers, fmt.Sprintf("turn:%s", strings.TrimPrefix(addr, "turn:")))
	}

	return servers
}

// fileExists checks if the file exists and is not a dir
func fileExists(file string) bool {
	info, err := os.Stat(file)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

// bytesToSize converts byte count to human readable string
func bytesToSize(bytes uint64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	base := uint(bits.Len64(bytes) / 10)
	return fmt.Sprintf("%.1f%cB", float64(bytes)/float64(uint64(1<<(base*10))), " KMGTPE"[base])
}
