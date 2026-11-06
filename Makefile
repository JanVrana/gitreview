BIN := gitreview
PREFIX ?= /usr/local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build test vet install dist clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) .

test:
	go test ./...

vet:
	go vet ./...

install: build
	install -m 0755 $(BIN) $(PREFIX)/bin/$(BIN)

# Release binaries named gitreview-<os>-<arch> (what the README's curl expects).
dist:
	rm -rf dist && mkdir -p dist
	for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(BIN)-$$os-$$arch . || exit 1; \
	done
	cd dist && sha256sum $(BIN)-* > SHA256SUMS

clean:
	rm -rf $(BIN) dist
