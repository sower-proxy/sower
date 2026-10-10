MAKEFLAGS += --jobs all
GO:=CGO_ENABLED=0 go

default: test build

test: web check-scripts test-install
	${GO} vet ./...
	${GO} test ./...

.PHONY: check-scripts
check-scripts:
	sh -n install.sh
	sh -n install_e2e_test.sh
	sh -n .github/sower.init

.PHONY: test-install
test-install:
	sh install_e2e_test.sh

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
