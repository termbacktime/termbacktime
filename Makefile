.PHONY: build build\:dev build\:prod install test check format release clean

build build\:prod:
	go run ./scripts/build-go build prod

build\:dev:
	go run ./scripts/build-go build dev

install:
	go install .

test:
	go test -race ./...

check:
	go vet ./...
	test -z "$$(gofmt -l cmd internal scripts main.go)"

format:
	gofmt -w cmd internal scripts main.go
	shfmt -ln posix -i 2 -ci -sr -w install.sh

release:
	go run ./scripts/release $(VERSION)

clean:
	rm -rf builds
