# TermBackTime
Terminal recording and playback, written in [Go]. All terminal recordings are currently saved as private [Gist] postings.

## Install
There is now an install script for Linux, Darwin, and Windows 10 using [WSL]. This will install go1.12.5 by default.
For other distributions please see the [releases] page. [Go] will be installed in `/usr/local/go` and `$GOPATH` is set to `$HOME/go`.

```shell
curl -s -L https://github.com/termbacktime/termbacktime/raw/master/install.sh | sudo bash
```

To install a different version of [Go] besides v1.12.5:
```shell
 wget https://github.com/termbacktime/termbacktime/raw/master/install.sh
 sudo ./install.sh <version>
```

For example, `1.12.0` would be `sudo ./install.sh 1.12.0`

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
