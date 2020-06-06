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
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/caarlos0/spin"
	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	au "github.com/logrusorgru/aurora"
	"github.com/pion/webrtc/v2"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh/terminal"
)

var (
	webrtcConfig = webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{STUNServerOne}},
			{URLs: []string{STUNServerTwo}},
		},
	}
	turnCredentials map[string]interface{}
	tserver         = webrtc.ICEServer{}
	turnMatch       = regexp.MustCompile(`^(?:([^:]+)?(?::([^@]+))?@)?((?:[^:]+)(?::\d+)?)$`)
	streaming       = false
	dataChannel     *webrtc.DataChannel
)

// liveCmd represents the auth command
var liveCmd = &cobra.Command{
	Use:   "live",
	Short: "Live share your terminal via WebRTC to browser",
	RunE: func(cmd *cobra.Command, args []string) error {
		defer spinner.Stop()
		stopTicker := make(chan bool, 1)
		closeWebsocket := make(chan bool, 1)
		interrupt := make(chan os.Signal, 1)
		signal.Notify(interrupt, os.Interrupt)

		// Convert a TURN server address into an ICEServer struct ([<user>[:<password>]@]<server[:<port>]>)
		turn := cmd.Flag("turn").Value.String()
		if len(turn) > 0 && turnMatch.MatchString(turn) {
			match := turnMatch.FindStringSubmatch(turn)
			if len(match[1]) > 0 {
				tserver.Username = match[1]
			}
			if len(match[2]) > 0 {
				tserver.Credential = match[2]
			}
			if len(match[3]) > 0 {
				tserver.URLs = FMTTurn([]string{match[3]})
			}
		}

		// Add TURN server flags into an ICEServer struct
		user := cmd.Flag("user").Value.String()
		if len(user) > 0 {
			tserver.Username = user
		}
		pass := cmd.Flag("pass").Value.String()
		if len(pass) > 0 {
			tserver.Credential = pass
		}
		addr := cmd.Flag("addr").Value.String()
		if len(addr) > 0 {
			tserver.URLs = FMTTurn([]string{addr})
		}

		// If a TURN server is provided add it to the ICEServers
		if len(tserver.URLs) > 0 {
			webrtcConfig.ICEServers = append(webrtcConfig.ICEServers, tserver)
		} else {
			if noturn, _ := cmd.Flags().GetBool("no-turn"); !noturn {
				spinner = spin.New("%s Requesting TURN server access...")
				spinner.Set(spin.Box1)
				spinner.Start()

				endpoint := fmt.Sprintf("%s/turn/credentials", APIEndpoint)
				req, err := http.NewRequest("GET", endpoint, nil)
				if err != nil {
					return Error(fmt.Errorf("cannot create request: %v", err))
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json")
				req.Header.Set("User-Agent", fmt.Sprintf("%s/%s", Application, Version))

				client := http.Client{}
				response, err := client.Do(req)
				if err != nil {
					return Error(fmt.Errorf("request HTTP error: %v", err))
				}
				defer response.Body.Close()
				var tcreds TURNCredentials
				if err = json.NewDecoder(response.Body).Decode(&tcreds); err != nil {
					return Error(fmt.Errorf("response JSON error: %v", err))
				}

				spinner.Stop()
				if len(tcreds.URLs) > 0 {
					tserver.Username = tcreds.Username
					tserver.Credential = tcreds.Credential
					tserver.URLs = FMTTurn(tcreds.URLs)
					webrtcConfig.ICEServers = append(webrtcConfig.ICEServers, tserver)
					fmt.Println(au.Green(au.Sprintf(au.Bold("Access granted: %s\nUsername: %s\nPassword: %s\r\n"),
						tserver.URLs, tserver.Username, tserver.Credential)))
				} else {
					return Error(fmt.Errorf("could not get TURN server credentials from %s - please try again later", endpoint))
				}
			}
		}

		spinner = spin.New("%s Creating WebRTC DataChannel...")
		spinner.Set(spin.Box1)
		spinner.Start()

		chn := fmt.Sprintf("tbt:%s", uuid())
		client, resp, err := websocket.DefaultDialer.Dial(fmt.Sprintf("%s/live?token=%s", Broker, chn), nil)
		if err != nil {
			fmt.Println(au.Sprintf(au.Red("\nError: %v (status code: %s)\n"), err, resp.StatusCode))
			os.Exit(1)
		}
		defer client.Close()

		peerConnection, err := webrtc.NewPeerConnection(webrtcConfig)
		if err != nil {
			fmt.Println(au.Sprintf(au.Red("\nError: %v\n"), err))
			os.Exit(1)
		}

		dataChannel, err = peerConnection.CreateDataChannel("live-terminal", nil)
		if err != nil {
			fmt.Println(au.Sprintf(au.Red("\nError: %v\n"), err))
			os.Exit(1)
		}

		offer, err := peerConnection.CreateOffer(nil)
		if err != nil {
			fmt.Println(au.Sprintf(au.Red("\nError: %v\n"), err))
			os.Exit(1)
		}

		err = peerConnection.SetLocalDescription(offer)
		if err != nil {
			fmt.Println(au.Sprintf(au.Red("\nError: %v\n"), err))
			os.Exit(1)
		}

		encoded, err := EncodeOffer(offer)
		if err != nil {
			fmt.Println(au.Sprintf(au.Red("\nError: %v\n"), err))
			os.Exit(1)
		}

		dataChannel.OnOpen(func() {
			if streaming == false {
				startpty()
			}
		})

		spinner.Stop()
		fmt.Println(au.Sprintf(au.Bold("Live playback: %s\r"), fmt.Sprintf("%s/live/#%s", PlaybackURL, chn)))

		go func() {
			ticker := time.NewTicker(1 * time.Second)
			cdown := 120
			for {
				select {
				case <-ticker.C:
					if cdown <= 0 {
						fmt.Printf("\n%v\r\n", au.Bold(au.Red("Live timed out, please try again!")))
						os.Exit(1)
					}
					fmt.Printf("\r%s", colorify("Waiting for connection; timeout in %d seconds...", cdown))
					cdown = cdown - 1
				case <-stopTicker:
					fmt.Printf(" %s\r\n\r\n", au.Bold(au.Green("Connected.")))
					return
				}
			}
		}()

		done := make(chan struct{})
		go func() {
			for {
				defer close(done)
				var l LiveResponse
				if err := client.ReadJSON(&l); err != nil {
					fmt.Println(au.Sprintf(au.Red("\nError: %v\n"), err))
					return
				}
				if l.Connected {
					stopTicker <- true
					packet := &LiveOffer{Offer: encoded}
					if len(tserver.URLs) > 0 {
						packet.TURN = &TURNServer{
							URLs:       tserver.URLs[0],
							Username:   tserver.Username,
							Credential: tserver.Credential.(string),
						}
					}
					if err := client.WriteJSON(packet); err != nil {
						fmt.Println(au.Sprintf(au.Red("\nError: %v\n"), err))
						os.Exit(1)
					}
				} else if len(l.Answer) > 0 {
					answer := webrtc.SessionDescription{}
					DecodeAnswer(l.Answer, &answer)
					err = peerConnection.SetRemoteDescription(answer)
					close(closeWebsocket)
					select {}
				}
			}
		}()

		for {
			select {
			case <-closeWebsocket:
				client.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			case <-done:
				return nil
			case <-interrupt:
				client.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				select {
				case <-done:
				case <-time.After(time.Second):
				}
				return nil
			}
		}
	},
}

func startpty() {
	streaming = true

	// Get shell command
	ccmd := shell()

	// Start PTY.
	fmt.Println(au.Sprintf(au.Bold("Launching PTY session (%s)..."), ccmd))

	pcmd := exec.Command(ccmd)
	ptmx, err := pty.Start(pcmd)
	if err != nil {
		fmt.Println(au.Sprintf(au.Red("\nError: %v\n"), err))
		os.Exit(1)
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
			if err := pty.InheritSize(os.Stdin, ptmx); err == nil {
				if Width, Height, err := terminal.GetSize(int(os.Stdout.Fd())); err == nil {
					_ = dataChannel.SendText(ToJSON(LiveLine{
						Command: "s",
						Sizes:   []int{Width, Height},
					}))
				}
			}
		}
	}()
	ch <- syscall.SIGWINCH

	// Set terminal to raw.
	oldState, err := terminal.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Println(au.Sprintf(au.Red("\nError: %v\n"), err))
		os.Exit(1)
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
					break
				}
			} else {
				chn <- string(bufin[0:nr])
			}
		}
	}(chn)

	// XXX: Send this over the data channel?
	fmt.Println(au.Green(au.Bold("Live streaming started!\r\n")))

	// Read from the PTY into a buffer.
	bufout := make([]byte, 4096)
	for {
		nr, err := ptmx.Read(bufout)
		if err != nil {
			break
		}
		// Get the current line
		line := string(bufout[0:nr])

		// Send the line over the data channel
		dataChannel.SendText(ToJSON(LiveLine{
			Lines: []string{line},
		}))

		// Write to STDOUT
		os.Stdout.WriteString(line)
	}

	_ = terminal.Restore(int(os.Stdin.Fd()), oldState)
	fmt.Println(au.Bold(au.Green("\r\nLive streaming ended!")))
	os.Exit(0)
}

func init() {
	liveCmd.Flags().StringP("turn", "t", "", "TURN server string ([<user>[:<password>]@]<server[:<port>]>)")
	liveCmd.Flags().StringP("user", "u", "", "TURN server username")
	liveCmd.Flags().StringP("pass", "p", "", "TURN server password")
	liveCmd.Flags().StringP("addr", "a", "", "TURN server address with optional port (<server>[:<port>])")
	liveCmd.Flags().BoolP("no-turn", "n", false, fmt.Sprintf("do NOT use the offical %s TURN servers", Application))
	recordCmd.AddCommand(liveCmd)
}
