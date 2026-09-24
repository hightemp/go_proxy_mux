BINARY_NAME=go_proxy_mux
CMD_PACKAGE=./cmd/go_proxy_mux
LDFLAGS=-ldflags "-s -w"
CGO_ENABLED=0

.PHONY: build build-linux build-windows build-darwin build-all docker-build docker-push clean test run install deps release format-check vet lint ci

build:
	CGO_ENABLED=$(CGO_ENABLED) go build $(LDFLAGS) -o $(BINARY_NAME) $(CMD_PACKAGE)

build-linux:
	CGO_ENABLED=$(CGO_ENABLED) GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY_NAME)-linux-amd64 $(CMD_PACKAGE)

build-windows:
	CGO_ENABLED=$(CGO_ENABLED) GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY_NAME)-windows-amd64.exe $(CMD_PACKAGE)

build-darwin:
	CGO_ENABLED=$(CGO_ENABLED) GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY_NAME)-darwin-amd64 $(CMD_PACKAGE)

build-all: build-linux build-windows build-darwin

docker-build:
	docker build -t hightemp/go_proxy_mux:1.0.2 .

docker-push:
	docker push hightemp/go_proxy_mux:1.0.2

clean:
	rm -f $(BINARY_NAME) $(BINARY_NAME)-*

test:
	go test -race ./...

format-check:
	@test -z "$$(gofmt -l cmd internal)"

vet:
	go vet ./...

lint:
	golangci-lint run ./...

ci: format-check vet lint test

run:
	go run $(CMD_PACKAGE)

install:
	go install $(LDFLAGS) $(CMD_PACKAGE)

deps:
	go mod tidy
	go mod download

release: clean deps build-all
