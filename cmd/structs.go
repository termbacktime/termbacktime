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

import "time"

// Recording JSON struct for recordings.
type Recording struct {
	Info    Info    `json:"i"`
	Started int64   `json:"d"`
	Title   string  `json:"t,omitempty"`
	Sizes   []int   `json:"s,omitempty"`
	Lines   []Lines `json:"r,omitempty"`
	Pack    string  `json:"p,omitempty"`
}

// Lines is an array of terminal STDOUT lines.
type Lines struct {
	Time    int64    `json:"t,omitempty"`
	Command string   `json:"c,omitempty"`
	Lines   []string `json:"l,omitempty"`
	Sizes   []int    `json:"s,omitempty"`
}

// GistFiles is the top-level struct for gist files.
type GistFiles struct {
	Content string `json:"content"`
}

// Gist is the required structure for POST data for API purposes.
type Gist struct {
	Description string               `json:"description,omitempty"`
	Public      bool                 `json:"public,omitempty"`
	GistFiles   map[string]GistFiles `json:"files,omitempty"`
}

// Info is the distro information.
type Info struct {
	Arch string `json:"a"`
	OS   string `json:"o"`
	Go   string `json:"v"`
}

// GetGist represents a GitHub's gist API response.
type GetGist struct {
	ID          string                 `json:"id,omitempty"`
	Description string                 `json:"description,omitempty"`
	Public      bool                   `json:"public,omitempty"`
	Owner       map[string]interface{} `json:"owner,omitempty"`
	Files       map[string]GistFile    `json:"files,omitempty"`
	Comments    int                    `json:"comments,omitempty"`
	HTMLURL     string                 `json:"html_url,omitempty"`
	GitPullURL  string                 `json:"git_pull_url,omitempty"`
	GitPushURL  string                 `json:"git_push_url,omitempty"`
	CreatedAt   time.Time              `json:"created_at,omitempty"`
	UpdatedAt   time.Time              `json:"updated_at,omitempty"`
	NodeID      string                 `json:"node_id,omitempty"`
}

// GistFile represents a file in a gist.
type GistFile struct {
	Size     int    `json:"size,omitempty"`
	Filename string `json:"filename,omitempty"`
	Language string `json:"language,omitempty"`
	Type     string `json:"type,omitempty"`
	RawURL   string `json:"raw_url,omitempty"`
	Content  string `json:"content,omitempty"`
}

// AuthResponse represents the WebSocket response from the broker
type AuthResponse struct {
	Token string `json:"token"`
	Login string `json:"login"`
}

// LiveResponse represents the WebSocket response from the broker
type LiveResponse struct {
	Connected bool   `json:"connected"`
	Answer    string `json:"answer"`
}

// LiveOffer creates SDP offers
type LiveOffer struct {
	Offer string      `json:"offer"`
	TURN  *TURNServer `json:"turn,omitempty"`
}

// LiveLine is for live terminal streaming
type LiveLine struct {
	Command string   `json:"c,omitempty"`
	Lines   []string `json:"l,omitempty"`
	Sizes   []int    `json:"s,omitempty"`
}

// TURNServer is used for the front end access to a TURN server
type TURNServer struct {
	URLs       string `json:"urls,omitempty"`
	Username   string `json:"username,omitempty"`
	Credential string `json:"credential,omitempty"`
}

// TURNCredentials is the authorization creds for official TURN servers
type TURNCredentials struct {
	URLs       []string `json:"servers,omitempty"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}
