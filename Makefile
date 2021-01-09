# Production servers
PBURL=https://termbackti.me
BROKER=wss://broker.termbackti.me
API=https://api.termbackti.me
LIVE=https://xterm.live

# Development servers
DEVPBURL=https://dev.termbackti.me
DEVBROKER=wss://dev-broker.termbackti.me
DEVAPI=https://dev-api.termbackti.me
DEVLIVE=https://dev.xterm.live

# Misc settings
GISTAPI=https://api.github.com/gists
GITURL=louist.dev/termbacktime
APP_NAME=termbacktime
GFILE_NAME=terminal-recording.json
CONFIG_TYPE=json
REV=$(shell git rev-parse --short HEAD)

# STUN servers for WebRTC (live terminal sharing)
STUN_SERVER1=stun:stun1.l.google.com:19302
STUN_SERVER2=stun:stun2.l.google.com:19302

# Binary file names
BINARY_DARWIN=$(APP_NAME)-$(REV)-darwin
BINARY_UNIX=$(APP_NAME)-$(REV)-unix
BINARY_FREEBSD=$(APP_NAME)-$(REV)-freebsd

# Get the application version
VERSION=$(shell cat ./VERSION)

# Production compiler flags
LDFLAGS=-s -w -X '${GITURL}/cmd.Application=${APP_NAME}' -X '${GITURL}/cmd.Version=${VERSION}' -X '${GITURL}/cmd.Revision=${REV}' \
	-X '${GITURL}/cmd.PlaybackURL=${PBURL}'  -X '${GITURL}/cmd.GistAPI=${GISTAPI}' -X '${GITURL}/cmd.Broker=${BROKER}' \
	-X '${GITURL}/cmd.GistFileName=${GFILE_NAME}' -X '${GITURL}/cmd.ConfigType=${shell echo ${CONFIG_TYPE} | tr '[:upper:]' '[:lower:]'}' \
	-X '${GITURL}/cmd.STUNServerOne=${STUN_SERVER1}' -X '${GITURL}/cmd.STUNServerTwo=${STUN_SERVER2}' -X '${GITURL}/cmd.APIEndpoint=${API}' \
	-X '${GITURL}/cmd.LiveURL=${LIVE}'

# Development compiler flag options
DEVLDFLAGS=-X '${GITURL}/cmd.Application=${APP_NAME}-dev' -X '${GITURL}/cmd.Revision=DEV-${REV}' -X '${GITURL}/cmd.PlaybackURL=${DEVPBURL}' \
	-X '${GITURL}/cmd.Broker=${DEVBROKER}' -X '${GITURL}/cmd.APIEndpoint=${DEVAPI}' -X '${GITURL}/cmd.LiveURL=${DEVLIVE}'

# Check if upx is installed
UPX := $(shell command -v upx 2> /dev/null)

build: initial
	go build -o ./builds/$(APP_NAME) -v -ldflags "${LDFLAGS}"

build-dev: initial
	go build -o ./builds/$(APP_NAME)-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"

build-crosscompile: initial
	GOOS=darwin GOARCH=amd64 go build -o ./builds/$(BINARY_DARWIN) -v -ldflags "${LDFLAGS}"
	GOOS=linux GOARCH=amd64 go build -o ./builds/$(BINARY_UNIX) -v -ldflags "${LDFLAGS}"
	GOOS=linux GOARCH=386 go build -o ./builds/$(BINARY_UNIX)-386 -v -ldflags "${LDFLAGS}"
	GOOS=linux GOARCH=arm64 go build -o ./builds/$(BINARY_UNIX)-arm64 -v -ldflags "${LDFLAGS}"
	GOOS=linux GOARCH=arm GOARM=7 go build -o ./builds/$(BINARY_UNIX)-armv7 -v -ldflags "${LDFLAGS}"
	GOOS=linux GOARCH=arm GOARM=6 go build -o ./builds/$(BINARY_UNIX)-armv6 -v -ldflags "${LDFLAGS}"
	GOOS=freebsd GOARCH=amd64 go build -o ./builds/$(BINARY_FREEBSD) -v -ldflags "${LDFLAGS}"
	GOOS=freebsd GOARCH=386 go build -o ./builds/$(BINARY_FREEBSD)-386 -v -ldflags "${LDFLAGS}"

build-crosscompile-dev: initial
	GOOS=darwin GOARCH=amd64 go build -o ./builds/$(BINARY_DARWIN)-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"
	GOOS=linux GOARCH=amd64 go build -o ./builds/$(BINARY_UNIX)-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"
	GOOS=linux GOARCH=386 go build -o ./builds/$(BINARY_UNIX)-386-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"
	GOOS=linux GOARCH=arm64 go build -o ./builds/$(BINARY_UNIX)-arm64-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"
	GOOS=linux GOARCH=arm GOARM=7 go build -o ./builds/$(BINARY_UNIX)-armv7-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"
	GOOS=linux GOARCH=arm GOARM=6 go build -o ./builds/$(BINARY_UNIX)-armv6-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"
	GOOS=freebsd GOARCH=amd64 go build -o ./builds/$(BINARY_FREEBSD)-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"
	GOOS=freebsd GOARCH=386 go build -o ./builds/$(BINARY_FREEBSD)-386-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"

install: initial
	go install -i -ldflags "${LDFLAGS}"

install-upx: initial install upx-installed

install-dev: initial
	go build -i -o $(GOPATH)/bin/$(APP_NAME)-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"

install-dev-upx: initial install-dev upx-installed

uninstall:
ifneq (,$(shell which termbacktime))
	rm -rf $(shell which termbacktime)
endif
ifneq (,$(shell which termbacktime-dev))
	rm -rf $(shell which termbacktime-dev)
endif
	go clean -i

upx-check:
ifndef UPX
	$(error "upx is not installed; please see https://upx.github.io/ for more information")
endif

upx: upx-check
	upx -5 ./builds/*

upx-installed: upx-check
	upx -5 $(GOPATH)/bin/$(APP_NAME)*

# upx does not support freebsd
upx-crosscompile: upx-check
	upx -5 ./builds/$(BINARY_DARWIN)* ./builds/$(BINARY_UNIX)*

initial:
	go clean
	rm -rf ./builds/termbacktime*
	go vet ./...

run:
	go run -v -ldflags "${LDFLAGS}" ./main.go

run-dev:
	go run -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}" ./main.go

update-pkg-cache:
	cd .. && GOPROXY=https://proxy.golang.org GO111MODULE=on go get ${GITURL}@$(VERSION)