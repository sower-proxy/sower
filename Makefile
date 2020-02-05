export GO111MODULE=on
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
	go build -ldflags \
		"-X conf.version=$(shell git describe --tags) \
		 -X conf.date=$(shell date +%Y-%m-%d)"
image:
	docker build -t sower -f .github/Dockerfile .

kill:
	sudo pkill -9 sower || true

client: build kill
	sudo $(PWD)/sower -s 127.0.0.1:5533 -H "127.0.0.1:8080"

server: build
	$(PWD)/sower -f ''

run: build kill
	$(PWD)/sower -f '' &
	sudo $(PWD)/sower -f '' -s 127.0.0.1:5533 -H "127.0.0.1:8080" &

	@sleep 1
	HTTP_PROXY=http://127.0.0.1:8080 curl http://baidu.com || true
	@echo
	HTTPS_PROXY=http://127.0.0.1:8080 curl https://baidu.com || true
	@echo

	@sleep 1
	@sudo pkill -9 sower || true
