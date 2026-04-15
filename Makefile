.PHONY: build install clean server

build:
	go build -o bin/spout ./cmd/spout/
	go build -o bin/spout-server ./cmd/server/

install:
	go install ./cmd/spout/

server:
	go run ./cmd/server/

clean:
	rm -rf bin/
