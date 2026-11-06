BIN := gitreview
PREFIX ?= /usr/local

.PHONY: build test vet install clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN) .

test:
	go test ./...

vet:
	go vet ./...

install: build
	install -m 0755 $(BIN) $(PREFIX)/bin/$(BIN)

clean:
	rm -f $(BIN)
