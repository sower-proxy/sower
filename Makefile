MAKEFLAGS += --jobs all
GO:=CGO_ENABLED=0 go

default: test build

test: web check-scripts
	${GO} vet ./...
	${GO} test ./...

.PHONY: check-scripts
check-scripts:
	sh -n install.sh
	sh -n .github/sower.init

build: sower sowerd
.PHONY: web
web:
	cd web && bun install --frozen-lockfile && bun run build

.PHONY: sower
sower: web
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
