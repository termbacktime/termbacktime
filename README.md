# TermBackTime
Terminal recording and playback, written in [Go]. All terminal recordings are currently saved as private [Gist] postings.

### Playback Example
![TermBackTime - Playback](https://i.imgur.com/RtLL8e2.gif)

https://termbackti.me/p/1fc1b6cd6317180d01f60b3011490e75

## Install
#### Note: If [Go] is already installed, will use the currently installed version to install `termbacktime`.
There is now an install script for Linux, Darwin, and Windows 10 using [WSL]. This will install go1.12.5 locally by default. For other distributions please see the [releases] page. [Go] will be installed in `$HOME/.goroot` as `$GOROOT` and `$GOPATH` is set to `$HOME/go`.

```shell
curl -s -L https://github.com/termbacktime/termbacktime/raw/master/install.sh | bash
```

To install a different version of [Go] besides v1.12.5:
```shell
 wget https://github.com/termbacktime/termbacktime/raw/master/install.sh
 ./install.sh <version>
```

For example, `1.12.0` would be `./install.sh 1.12.0`

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

[Go]: https://golang.com/
[WSL]: https://docs.microsoft.com/en-us/windows/wsl/install-win10
[releases]: https://github.com/termbacktime/termbacktime/releases
[Gist]: https://gist.github.com/
