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

package templates

// CompletionR is the replacements for the completion template
type CompletionR struct {
	Application string
}

// Completion is the Long version for cobra.
var Completion = `To load shell based autocomplete:

Bash:
    $ source <({{.Application}} completion bash)

# To load completions for each session, execute once:
Linux:
    $ {{.Application}} completion bash > /etc/bash_completion.d/{{.Application}}
MacOS:
    $ {{.Application}} completion bash > /usr/local/etc/bash_completion.d/{{.Application}}

Zsh:
    $ source <({{.Application}} completion zsh)

# To load completions for each session, execute once:
    $ {{.Application}} completion zsh > "${fpath[1]}/_{{.Application}}"

Fish:
    $ {{.Application}} completion fish | source

# To load completions for each session, execute once:
    $ {{.Application}} completion fish > ~/.config/fish/completions/{{.Application}}.fish`
