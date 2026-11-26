.PHONY: all build test clean run

BINARY=bin/wifichat

all: build

build:
	@mkdir -p bin
	go build -o $(BINARY) ./cmd/wifichat

test:
	go test -v -race ./...

clean:
	rm -rf bin/ downloads/

run: build
	./$(BINARY)
