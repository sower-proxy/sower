default: test build

test:
	go vet ./...
	go list ./... | grep -v internal | xargs go test
build:
	go build -ldflags "-w -s \
		-X conf.version=$(shell git describe --tags --always) \
		-X conf.date=$(shell date +%Y-%m-%d)"
image:
	docker build -t sower -f .github/Dockerfile .

