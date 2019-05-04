PBURL=https://termbackti.me
GISTAPI=https://api.github.com/gists
ABLYIO_TOKEN=Yn3xbQ.W4fPqA:_aZ8tpIGEPJXscWv
GITURL=github.com/LouisT/termbacktime
APP_NAME=termbacktime
VERSION=0.0.1-alpha
CONFIG_TYPE=json

REV=`git rev-parse --short HEAD`
BINARY_OSX=${APP_NAME}-${REV}-osx
BINARY_UNIX=$(APP_NAME)-${REV}-unix
BINARY_WIN=$(APP_NAME)-${REV}-win.exe
LDFLAGS=-ldflags "-X '${GITURL}/cmd.Application=${APP_NAME}' -X '${GITURL}/cmd.Version=${VERSION}' -X '${GITURL}/cmd.Revision=${REV}' \
		-X '${GITURL}/cmd.PlaybackURL=${PBURL}'  -X '${GITURL}/cmd.GistAPI=${GISTAPI}' -X '${GITURL}/cmd.AblyToken=${ABLYIO_TOKEN}' \
		-X '${GITURL}/cmd.ConfigType=${shell echo ${CONFIG_TYPE} | tr '[:upper:]' '[:lower:]'}'"

build: build-osx build-unix
build-osx:
	go build -o ./builds/$(BINARY_OSX) -v ${LDFLAGS}
build-unix:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ./builds/$(BINARY_UNIX) -v ${LDFLAGS}
install: deps
	go install -i ${LDFLAGS}
uninstall:
	go clean -i
test:
	go test -v ./...
clean:
	go clean
	rm -rf ./builds/termbacktime-*
run: deps
	go run -v ${LDFLAGS} ./...
deps:
	go get github.com/ably/ably-go/ably
	go get github.com/caarlos0/spin
	go get github.com/kr/pty
	go get github.com/logrusorgru/aurora
	go get golang.org/x/crypto/ssh/terminal
	go get github.com/mitchellh/go-homedir
	go get github.com/spf13/cobra
	go get github.com/spf13/viper
