CPUS ?= $(shell nproc)
MAKEFLAGS += --jobs=$(CPUS)
GO:=CGO_ENABLED=0 go

default: test build

test:
	${GO} vet ./...
	${GO} test ./...

build: client server

.PHONY: client
client:
	${GO} build -ldflags "\
		-X main.version=$(shell git describe --tags --always) \
		-X main.date=$(shell date +%Y-%m-%d)" ./cmd/client
.PHONY: server
server:
	${GO} build -ldflags "\
		-X main.version=$(shell git describe --tags --always) \
		-X main.date=$(shell date +%Y-%m-%d)" ./cmd/server