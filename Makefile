MAKEFLAGS += --jobs all
GO:=CGO_ENABLED=0 go

default: test build

test:
	${GO} vet ./...
	${GO} test ./...

build: sower sowerd
.PHONY: sower
sower:
	${GO} build -ldflags "\
		-X main.version=$(shell git describe --tags --always) \
		-X main.date=$(shell date +%Y-%m-%d)" \
		-o sower ./cmd/sower
.PHONY: sowerd
sowerd:
	${GO} build -ldflags "\
		-X main.version=$(shell git describe --tags --always) \
		-X main.date=$(shell date +%Y-%m-%d)" \
		-o sowerd ./cmd/sowerd

clean:
	rm -f sower sowerd sower.exe sowerd.exe
