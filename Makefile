MAKEFLAGS += --jobs all
GO:=CGO_ENABLED=0 go

default: test build

test:
	${GO} vet ./...
	${GO} test ./...

build: sower sowerd

sower:
	${GO} build -ldflags "\
		-X main.version=$(shell git describe --tags --always) \
		-X main.date=$(shell date +%Y-%m-%d)" \
		-o sower ./cmd/sower
sowerd:
	${GO} build -ldflags "\
		-X main.version=$(shell git describe --tags --always) \
		-X main.date=$(shell date +%Y-%m-%d)" \
		-o sowerd ./cmd/sowerd
