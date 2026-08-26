GOHOSTOS := $(shell go env GOHOSTOS)
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
BUF ?= $(shell go env GOPATH)/bin/buf

.PHONY: init
init:
	go install github.com/bufbuild/buf/cmd/buf@v1.61.0

.PHONY: api
api:
	$(BUF) dep update
	$(BUF) generate --template buf.gen.yaml

.PHONY: config
config:
	$(BUF) generate --template buf.gen.config.yaml

.PHONY: generate
generate:
	go generate ./...
	go mod tidy

.PHONY: all
all: api config generate

.PHONY: lint
lint:
	$(BUF) lint
	go vet ./...

.PHONY: test
test:
	go test ./...

.PHONY: build
build:
	mkdir -p bin
	go build -ldflags "-X main.Version=$(VERSION)" -o bin/core ./app/core/cmd/core
	go build -ldflags "-X main.Version=$(VERSION)" -o bin/vision ./app/vision/cmd/vision

.PHONY: run-core run-vision
run-core:
	go run ./app/core/cmd/core -conf ./configs/core.yaml
run-vision:
	go run ./app/vision/cmd/vision -conf ./configs/vision.yaml
