# Production servers
PBURL=https://termbackti.me
BROKER=wss://broker.termbackti.me
API=https://api.termbackti.me

# Development servers
DEVPBURL=https://dev.termbackti.me
DEVBROKER=wss://dev-broker.termbackti.me
DEVAPI=https://dev-api.termbackti.me

# Misc settings
ANALYTICS=07d66b96ce0af1bc1bb721c58417df66
GISTAPI=https://api.github.com/gists
GITURL=github.com/termbacktime/termbacktime
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
LDFLAGS=-X '${GITURL}/cmd.Application=${APP_NAME}' -X '${GITURL}/cmd.Version=${VERSION}' -X '${GITURL}/cmd.Revision=${REV}' \
	-X '${GITURL}/cmd.PlaybackURL=${PBURL}'  -X '${GITURL}/cmd.GistAPI=${GISTAPI}' -X '${GITURL}/cmd.Broker=${BROKER}' \
	-X '${GITURL}/cmd.GistFileName=${GFILE_NAME}' -X '${GITURL}/cmd.ConfigType=${shell echo ${CONFIG_TYPE} | tr '[:upper:]' '[:lower:]'}' \
	-X '${GITURL}/cmd.STUNServerOne=${STUN_SERVER1}' -X '${GITURL}/cmd.STUNServerTwo=${STUN_SERVER2}' -X '${GITURL}/cmd.APIEndpoint=${API}' \
	-X '${GITURL}/cmd.Analytics=${ANALYTICS}'

# Development compiler flag options
DEVLDFLAGS=-X '${GITURL}/cmd.Application=${APP_NAME}-dev' -X '${GITURL}/cmd.Revision=DEV-${REV}' -X '${GITURL}/cmd.PlaybackURL=${DEVPBURL}' \
	-X '${GITURL}/cmd.Broker=${DEVBROKER}' -X '${GITURL}/cmd.APIEndpoint=${DEVAPI}'

build:
	go build -o ./builds/$(APP_NAME) -v -ldflags "${LDFLAGS}"

build-dev:
	go build -o ./builds/$(APP_NAME)-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"

build-crosscompile:
	make clean
	GOOS=darwin GOARCH=amd64 go build -o ./builds/$(BINARY_DARWIN) -v -ldflags "${LDFLAGS}"
	GOOS=darwin GOARCH=386 go build -o ./builds/$(BINARY_DARWIN)-386 -v -ldflags "${LDFLAGS}"
	GOOS=linux GOARCH=amd64 go build -o ./builds/$(BINARY_UNIX) -v -ldflags "${LDFLAGS}"
	GOOS=linux GOARCH=386 go build -o ./builds/$(BINARY_UNIX)-386 -v -ldflags "${LDFLAGS}"
	GOOS=linux GOARCH=arm64 go build -o ./builds/$(BINARY_UNIX)-arm64 -v -ldflags "${LDFLAGS}"
	GOOS=linux GOARCH=arm GOARM=7 go build -o ./builds/$(BINARY_UNIX)-armv7 -v -ldflags "${LDFLAGS}"
	GOOS=linux GOARCH=arm GOARM=6 go build -o ./builds/$(BINARY_UNIX)-armv6 -v -ldflags "${LDFLAGS}"
	GOOS=freebsd GOARCH=amd64 go build -o ./builds/$(BINARY_FREEBSD) -v -ldflags "${LDFLAGS}"
	GOOS=freebsd GOARCH=386 go build -o ./builds/$(BINARY_FREEBSD)-386 -v -ldflags "${LDFLAGS}"

build-crosscompile-dev:
	make clean
	GOOS=darwin GOARCH=amd64 go build -o ./builds/$(BINARY_DARWIN)-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"
	GOOS=darwin GOARCH=386 go build -o ./builds/$(BINARY_DARWIN)-386-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"
	GOOS=linux GOARCH=amd64 go build -o ./builds/$(BINARY_UNIX)-dev -v-ldflags "${LDFLAGS} ${DEVLDFLAGS}"
	GOOS=linux GOARCH=386 go build -o ./builds/$(BINARY_UNIX)-386-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"
	GOOS=linux GOARCH=arm64 go build -o ./builds/$(BINARY_UNIX)-arm64-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"
	GOOS=linux GOARCH=arm GOARM=7 go build -o ./builds/$(BINARY_UNIX)-armv7-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"
	GOOS=linux GOARCH=arm GOARM=6 go build -o ./builds/$(BINARY_UNIX)-armv6-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"
	GOOS=freebsd GOARCH=amd64 go build -o ./builds/$(BINARY_FREEBSD)-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"
	GOOS=freebsd GOARCH=386 go build -o ./builds/$(BINARY_FREEBSD)-386-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"

install:
	go install -i -ldflags "${LDFLAGS}"

install-dev:
	go build -i -o $(GOPATH)/bin/$(APP_NAME)-dev -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}"

uninstall:
ifneq (,$(shell which termbacktime))
	rm -rf $(shell which termbacktime)
endif
ifneq (,$(shell which termbacktime-dev))
	rm -rf $(shell which termbacktime-dev)
endif
	go clean -i

test:
	go vet ./...
	go test -v ./...

clean:
	go clean
	rm -rf ./builds/termbacktime*

run:
	go run -v -ldflags "${LDFLAGS}" ./main.go

run-dev:
	go run -v -ldflags "${LDFLAGS} ${DEVLDFLAGS}" ./main.go