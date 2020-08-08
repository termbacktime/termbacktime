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
	"regexp"
	"runtime"

	"github.com/caarlos0/spin"
	"github.com/pion/webrtc/v2"
)

// Exported default compile settings.
var (
	GistFileName  = "terminal-recording.json"
	Application   = "termbacktime"
	GistAPI       = "https://api.github.com/gists"
	PlaybackURL   = "https://termbackti.me"
	LiveURL       = "https://xterm.live"
	Broker        = "wss://broker.termbackti.me"
	APIEndpoint   = "https://api.termbackti.me"
	Revision      = "0000000"
	Version       = "0.0.0"
	ConfigType    = "json"
	STUNServerOne = "stun:stun1.l.google.com:19302"
	STUNServerTwo = "stun:stun2.l.google.com:19302"
	HomeDir       = getHome()
	Username      = uuid()
)

// Base application variables.
var (
	versionTemplate = fmt.Sprintf("%s - %s/ - version=%s revision=%s (%s)\r\n",
		Application, PlaybackURL, Version, Revision, runtime.Version())
	cfgFile      string
	Closed       bool
	GistResponse map[string]interface{}
	GithubToken  string
	Instructions []Lines
	spinner      = spin.New("%s Working...")
	skipConfig   = map[string]bool{
		"completion": true,
		"help":       true,
		Application:  true,
	}
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
