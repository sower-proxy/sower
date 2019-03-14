SERVER:=127.0.0.1:5533

default: test build

generate:
ifeq ("", "$(shell which stringer)")
	@echo installing generator
	go get -v golang.org/x/tools/cmd/stringer
endif
	go generate ./...

test:
	go vet ./...
	go test ./...
build:
	GO111MODULE=on go build -v -ldflags \
		"-X main.version=$(shell git describe --tags) \
		 -X main.date=$(shell date +%Y-%m-%d)"
image:
	docker build -t sower -f .circleci/Dockerfile .

kill:
	sudo pkill -9 sower || true

client: build kill
	sudo $(PWD)/sower

server: build kill
	$(PWD)/sower -n TCP -v 1

run: build kill
	$(PWD)/sower -n TCP -v 1  &
	sudo $(PWD)/sower &
	@sleep 1
	curl 127.0.0.1
	@sleep 1
	@sudo pkill -9 sower || true
