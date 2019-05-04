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

// Recording JSON struct for recordings.
type Recording struct {
	Started int64   `json:"d"`
	Title   string  `json:"t,omitempty"`
	Sizes   []int   `json:"s,omitempty"`
	Lines   []Lines `json:"r,omitempty"`
	Pack    string  `json:"p,omitemptu"`
}

// Lines is an array of terminal STDOUT lines.
type Lines struct {
	Time    int64    `json:"t,omitempty"`
	Command string   `json:"c,omitempty"`
	Lines   []string `json:"l,omitempty"`
	Sizes   []int    `json:"s,omitempty"`
}

// GistFile is the top-level struct for a gist file
type GistFile struct {
	Content string `json:"content"`
}

// Gist is the required structure for POST data for API purposes
type Gist struct {
	Description string              `json:"description,omitempty"`
	Public      bool                `json:"public,omitempty"`
	GistFile    map[string]GistFile `json:"files,omitempty"`
}
