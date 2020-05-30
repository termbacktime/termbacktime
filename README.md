# TermBackTime
Terminal recording and playback, written in [Go]. All terminal recordings are currently saved as private [Gist] postings.

### Playback Example
![TermBackTime - Playback](https://i.imgur.com/RtLL8e2.gif)

https://termbackti.me/p/1fc1b6cd6317180d01f60b3011490e75

## Install
#### Note: If [Go] is already installed, will use the currently installed version to install `termbacktime`.
There is now an install script for Linux, Darwin, and Windows 10 using [WSL]. This will attempt to install the latest version of Go, defaulting back to 1.13. For other distributions please see the [releases] page. [Go] will be installed in `$HOME/.goroot` as `$GOROOT` and `$GOPATH` is set to `$HOME/go`.

```shell
curl -s -L https://github.com/termbacktime/termbacktime/raw/master/install.sh | bash
```

To install a different version of [Go]:
```shell
 wget https://github.com/termbacktime/termbacktime/raw/master/install.sh
 ./install.sh <version>
```

For example, `1.13.11` would be `./install.sh 1.13.11`

If you already have [Go] installed, you can manually install:
```shell
go get -u github.com/termbacktime/termbacktime
cd $GOPATH/src/github.com/termbacktime/termbacktime
make install
```

## Authorization
In order to submit recordings to [Gist] you must first authorize termbacktime with GitHub.
```shell
termbacktime auth
```

## Recording
After authorizing TermBackTime with GitHub simply run `termbacktime` to start recording!

## Live terminal sharing (BETA)
To share your terminal over the web with [WebRTC], simply run `termbacktime live` and share the provided link.
- This uses a [broker server] via [WebSockets] to handle [signaling]. Once the [data channel] via [WebRTC] is established the WebSocket connection is closed.

## Development
You can build your own development builds via `make build-dev` or `make build-crosscompile-dev`.

I provide development server endpoints for playback + live terminal, login, and WebRTC signaling at:
- https://dev.termbackti.me/
- https://dev-login.termbackti.me/
- https://dev-broker.termbackti.me/

Please note that these endpoints are under active development and may change or be unavailable at any time.


[Go]: https://golang.com/
[WSL]: https://docs.microsoft.com/en-us/windows/wsl/install-win10
[releases]: https://github.com/termbacktime/termbacktime/releases
[Gist]: https://gist.github.com/
[WebRTC]: https://webrtc.org/
[WebSockets]: https://developer.mozilla.org/en-US/docs/Web/API/WebSockets_API
[signaling]: https://developer.mozilla.org/en-US/docs/Web/API/WebRTC_API/Signaling_and_video_calling
[data channel]: https://webrtc.org/getting-started/data-channels
[broker server]: https://broker.termbackti.me/