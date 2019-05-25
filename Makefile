PBURL=https://termbackti.me
GISTAPI=https://api.github.com/gists
ABLYIO_TOKEN=Yn3xbQ.W4fPqA:_aZ8tpIGEPJXscWv
GITURL=github.com/termbacktime/termbacktime
APP_NAME=termbacktime
VERSION=0.0.2-alpha
CONFIG_TYPE=json
REV=`git rev-parse --short HEAD`
BINARY_DARWIN=$(APP_NAME)-$(REV)-darwin
BINARY_UNIX=$(APP_NAME)-$(REV)-unix
BINARY_FREEBSD=$(APP_NAME)-$(REV)-freebsd
LDFLAGS=-ldflags "-X '${GITURL}/cmd.Application=${APP_NAME}' -X '${GITURL}/cmd.Version=${VERSION}' -X '${GITURL}/cmd.Revision=${REV}' \
		-X '${GITURL}/cmd.PlaybackURL=${PBURL}'  -X '${GITURL}/cmd.GistAPI=${GISTAPI}' -X '${GITURL}/cmd.AblyToken=${ABLYIO_TOKEN}' \
		-X '${GITURL}/cmd.ConfigType=${shell echo ${CONFIG_TYPE} | tr '[:upper:]' '[:lower:]'}'"

build:
	go build -o ./builds/$(APP_NAME) -v ${LDFLAGS}
build-crosscompile:
	GOOS=darwin GOARCH=amd64 go build -o ./builds/$(BINARY_DARWIN) -v ${LDFLAGS}
	GOOS=darwin GOARCH=386 go build -o ./builds/$(BINARY_DARWIN)-386 -v ${LDFLAGS}
	GOOS=linux GOARCH=amd64 go build -o ./builds/$(BINARY_UNIX) -v ${LDFLAGS}
	GOOS=linux GOARCH=386 go build -o ./builds/$(BINARY_UNIX)-386 -v ${LDFLAGS}
	GOOS=linux GOARCH=arm64 go build -o ./builds/$(BINARY_UNIX)-arm64 -v ${LDFLAGS}
	GOOS=freebsd GOARCH=amd64 go build -o ./builds/$(BINARY_FREEBSD) -v ${LDFLAGS}
	GOOS=freebsd GOARCH=386 go build -o ./builds/$(BINARY_FREEBSD)-386 -v ${LDFLAGS}
install:
	go install -i ${LDFLAGS}
uninstall:
	go clean -i
test:
	go vet ./...
	go test -v ./...
clean:
	go clean
	rm -rf ./builds/termbacktime*
run:
	go run -v ${LDFLAGS} ./main.go
