SERVER:=127.0.0.1:5533

default: build

build:
	GOBIN=$(PWD) go install -v

kill:
	sudo pkill -9 sower || true

client: build kill
	sudo $(PWD)/sower -f conf/sower.toml

server: build kill
	$(PWD)/sower -D true

run: build kill
	$(PWD)/sower -D true &
	sudo $(PWD)/sower -f conf/sower.toml
