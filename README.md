# TermBackTime
Terminal recording and playback, written in Go. All terminal recordings are currently saved as private [Gist] postings.

## Install
There is now an install script for Linux, Darwin and Windows 10 using [WSL].
For other distrubutions, please see the [releases] page.

```bash
curl -s -L https://github.com/termbacktime/termbacktime/raw/master/install.sh | sudo bash
```

## Authorization
In order to submit recordings to [Gist] you must first authorize termbacktime with GitHub.
```
termbacktime auth
```

## Recording
After authorizing TermBackTime with GitHub simply run `termbacktime` to start recording!

[WSL]: https://docs.microsoft.com/en-us/windows/wsl/install-win10
[releases]: https://github.com/termbacktime/termbacktime/releases
[Gist]: https://gist.github.com/
