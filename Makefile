SERVER:=127.0.0.1:5533

default: test build

generate:
	go generate ./...
test:
	go test -race ./...
build:
	GO111MODULE=on go build -v -ldflags \
		"-X main.version=$(shell git describe --tags) \
		 -X main.date=$(shell date +%Y-%m-%d)"
image:
	docker build -t sower -f deploy/Dockerfile .

kill:
	sudo pkill -9 sower || true

client: build kill
	sudo $(PWD)/sower -f conf/sower.toml

server: build kill
	$(PWD)/sower -n TCP -v 1

run: build kill
	$(PWD)/sower -n TCP -v 1  &
	sudo $(PWD)/sower -f conf/sower.toml &
	@sleep 1
	curl 127.0.0.1
	@sleep 1
	@sudo pkill -9 sower || true
