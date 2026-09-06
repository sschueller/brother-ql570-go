GO ?= go
BINARY := ql570

.PHONY: all
all: build

.PHONY: build
build:
	$(GO) build -o $(BINARY) ./cmd/ql570

.PHONY: test
test:
	$(GO) test ./...

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: check
check: vet test

# Cross-compile for macOS on Apple Silicon (no cgo involved, so this works
# from any platform). The resulting binary prints via the HTTP daemon
# (`ql570 serve` on a Linux host) - direct USB is Linux/Android-only.
.PHONY: build-darwin-arm64
build-darwin-arm64:
	GOOS=darwin GOARCH=arm64 $(GO) build -o dist/ql570-darwin-arm64 ./cmd/ql570
	GOOS=darwin GOARCH=arm64 $(GO) vet ./cmd/ql570/

# Cross-compile for Android (Termux). The binary prints through the raw
# usbdevfs file descriptor handed over by `termux-usb` (termux-api) - no
# root needed.
.PHONY: build-android-arm64
build-android-arm64:
	GOOS=android GOARCH=arm64 $(GO) build -o dist/ql570-android-arm64 ./cmd/ql570
	GOOS=android GOARCH=arm64 $(GO) vet ./cmd/ql570/

.PHONY: build-darwin-amd64
build-darwin-amd64:
	GOOS=darwin GOARCH=amd64 $(GO) build -o dist/ql570-darwin-amd64 ./cmd/ql570

.PHONY: build-linux-arm64
build-linux-arm64:
	GOOS=linux GOARCH=arm64 $(GO) build -o dist/ql570-linux-arm64 ./cmd/ql570

# Build the daemon Docker image (multi-stage; runs on amd64 hosts, targets
# the runtime arch with --platform, e.g. linux/arm64 for a Raspberry Pi).
.PHONY: docker-build
docker-build:
	docker build -t ql570 .

.PHONY: clean
clean:
	rm -f $(BINARY)
	rm -rf dist
