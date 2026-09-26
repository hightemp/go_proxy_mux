BINARY_NAME=go_proxy_mux
CMD_PACKAGE=./cmd/go_proxy_mux
DOCKER_IMAGE=hightemp/go_proxy_mux
VERSION := $(shell tr -d '[:space:]' < VERSION)
LINT_VERSION=v2.12.0

.PHONY: build build-static docker-build docker-push clean test load-test run install deps release format-check vet lint ci

build:
	go build -o $(BINARY_NAME) $(CMD_PACKAGE)

build-static:
	CGO_ENABLED=0 go build -trimpath -ldflags '-s -w -extldflags "-static"' -o $(BINARY_NAME)_static $(CMD_PACKAGE)

docker-build:
	docker build -t $(DOCKER_IMAGE):$(VERSION) .

docker-push:
	docker push $(DOCKER_IMAGE):$(VERSION)

clean:
	rm -f $(BINARY_NAME) $(BINARY_NAME)_static

test:
	go test -race ./...

load-test:
	go test ./internal/proxy -run '^$$' -bench 'BenchmarkProxy(HTTP|HTTPBulk|CONNECT|HTTP2CONNECT|SOCKS5CONNECT)$$' -benchtime=3s -cpu=4 -benchmem

format-check:
	@test -z "$$(gofmt -l cmd internal)"

vet:
	go vet ./...

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(LINT_VERSION) run ./...

ci: format-check vet lint test

run:
	go run $(CMD_PACKAGE)

install:
	go install $(CMD_PACKAGE)

deps:
	go mod tidy
	go mod download

release: ci build
	bash scripts/release.sh
