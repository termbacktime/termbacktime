# [TermBackTime]
[![Go Report Card](https://goreportcard.com/badge/louist.dev/termbacktime)](https://goreportcard.com/report/louist.dev/termbacktime)
[![Discord](https://discordapp.com/api/guilds/426492725664153611/widget.png?style=shield)](https://discord.gg/vGaGZWDTsw)

Terminal recording and playback, written in [Go]. All terminal recordings are currently saved as secret [Gist] postings.

### Playback Example
[![TermBackTime - Playback](https://i.imgur.com/RtLL8e2.gif)](https://termbackti.me/p/1fc1b6cd6317180d01f60b3011490e75)

## Install / Update
> Note: If [Go] is already installed, will use the currently installed version to install `termbacktime`.
There is now an install script for Linux, Darwin, and Windows 10 using [WSL]. This will attempt to install the latest version of Go, defaulting back to 1.14. For other distributions please see the [releases] page. [Go] will be installed in `$HOME/.goroot` as `$GOROOT` and `$GOPATH` is set to `$HOME/go`.

```shell
curl -s -L https://termbackti.me/install.sh | bash
```

To install a different version of [Go]:
```shell
 wget https://termbackti.me/install.sh
 ./install.sh <version>
```

For example, `1.19.0` would be `./install.sh 1.19.0`

If you already have [Go] installed, you can manually install:
```shell
go get -u louist.dev/termbacktime
cd $GOPATH/src/louist.dev/termbacktime
make install
```

## Authorization
In order to submit recordings to [Gist] you must first authorize [TermBackTime] with GitHub.
We request access to the `read:user` and `gist` scopes. For more information, please see [available scopes].
You can request an auth token from [~/auth] or by running the following terminal command:
```shell
termbacktime auth
```
* _GitHub authorization is NOT required for live terminal sharing._

## Recording
After authorizing [TermBackTime] with GitHub simply run `termbacktime record` to start recording!
Please see `termbacktime --help` for more options.

## Live terminal sharing (BETA)
To start sharing your terminal over the web via [WebRTC], simply run `termbacktime live` and give the provided link to someone. Please see `termbacktime live --help` for more options.
- This uses a [broker server] via [WebSockets] to handle [signaling]. Once the [data channel] via [WebRTC] is established the WebSocket connection is closed.

##### STUN options
For now [TermBackTime] uses Google's STUN servers unless changed at compile time.
```shell
STUNServerOne = "stun:stun1.l.google.com:19302"
STUNServerTwo = "stun:stun2.l.google.com:19302"
```

A STUN server is used to detect network addresses. Please see https://en.wikipedia.org/wiki/STUN for more information.

##### TURN options
* Use an official TURN server provided by [TermBackTime]:
  * `termbacktime live`
* Provide your own TURN server credentials:
  * `termbacktime live --turn <username>:<password>@<server>[:<port>]`
  * `termbacktime live --user <username> --password <password> --addr <server>[:<port>]`
* Attempt to share without any TURN server:
  * `termbacktime live --no-turn`

A TURN server is used to relay WebRTC data between clients. Please see https://webrtc.org/getting-started/turn-server for more information.

## Contributing
Please feel free to open a PR or an issue on GitHub. Convention for this repository is to fork it, create a branch `feature/<feature name>`
in your fork based on `master` branch, and commit changes there. Once your changes are ready to merge back into this repository, squash your feature
branch into one commit and open a PR from your repository's feature branch into the `dev` branch of this repository. Please make sure your `master`
branch is up to date with `master` in this repo, and rebase your feature onto the tip of `master`.

## Reporting Issues
> Please do not report security related issues, vulnerabilities or bugs to the issue tracker, or elsewhere in public. Instead report sensitive bugs by email to <security@termbackti.me>.
- Open an [Issue](issues/new) with a clear and descriptive title.
  - Until we can discover whether it is a bug or not, we ask that you label it with `possible-bug`.
- Explain the behavior you would expect and the actual behavior.
- Please provide as much context as possible and describe the *reproduction steps* that someone else can follow to recreate the issue on their own.
  - Describe the current behavior and explain which behavior you expected to see instead and why.
  - Any availble stack traces or screenshots.
  - OS, platform and version. (Windows, Linux, macOS, x86, ARM)
  - If possible, please provide the `termbacktime --version` string.
    - If manually compiling please provide `go version`, termbacktime version located in the `VERSION` file, and the revision with `git rev-parse --short HEAD`.

## Development
You can build your own development builds via `make build-dev` or `make build-crosscompile-dev`.
I provide development server endpoints for playback + live terminal, login, and WebRTC signaling at:

- [dev.termbackti.me](https://dev.termbackti.me/)
- [dev-login.termbackti.me](https://dev-login.termbackti.me/)
- [dev-api.termbackti.me](https://dev-api.termbackti.me/)
- [dev-broker.termbackti.me](https://dev-broker.termbackti.me/)

Please note that these endpoints are under active development and may change or be unavailable at any time.

[TermBackTime]: https://termbackti.me/
[~/auth]: https://termbackti.me/auth
[Go]: https://golang.com/
[WSL]: https://docs.microsoft.com/en-us/windows/wsl/install-win10
[releases]: https://github.com/termbacktime/termbacktime/releases
[Gist]: https://gist.github.com/
[WebRTC]: https://webrtc.org/
[WebSockets]: https://developer.mozilla.org/en-US/docs/Web/API/WebSockets_API
[signaling]: https://developer.mozilla.org/en-US/docs/Web/API/WebRTC_API/Signaling_and_video_calling
[data channel]: https://webrtc.org/getting-started/data-channels
[broker server]: https://broker.termbackti.me/
[available scopes]: https://developer.github.com/apps/building-oauth-apps/understanding-scopes-for-oauth-apps/#available-scopes