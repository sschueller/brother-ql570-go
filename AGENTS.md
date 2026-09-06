# AGENTS.md

Guidelines for humans and AI agents working on this repository.

## Project

Pure-Go driver library + CLI (`ql570`) for the Brother QL-570 label printer.
Speaks the native QL raster protocol directly over USB (Linux `/dev/usb/lp*`,
Android usbdevfs via `termux-usb`), plus an HTTP print daemon with an embedded
web UI. No CUPS, no cgo, no third-party printer drivers.

- Module: `github.com/sschueller/brother-ql570-go`
- Go: 1.25.7 (see `go.mod`)
- License: MIT (`LICENSE`)

## Commands

```sh
make build    # go build -o ql570 ./cmd/ql570
make test     # go test ./...
make vet      # go vet ./...
make check    # vet + test
```

Run `make check` before pushing. Keep `gofmt`-clean code.

## Code conventions

- **No cgo.** Everything must cross-compile with `CGO_ENABLED=0`. Use raw
  syscalls / ioctls for USB access (see `pkg/ql/backend_linux.go`).
- Platform-specific backends use build tags: `pkg/ql/backend_linux.go`,
  `backend_android.go` (`//go:build android`), `backend_other.go`. Never move
  shared logic into a tagged file.
- The CLI lives in `cmd/ql570`. The version is a `const Version` in
  `cmd/ql570/main.go` — it is **bumped automatically** by the release
  workflow. Never edit it manually.
- Keep `README.md` docs (CLI flags, media table, protocol notes) in sync with
  code changes.
- Do not commit build artifacts (`/ql570`, `/bin/`, `/dist/` are gitignored).

## Contributing

Contribution guidelines (branch workflow, conventional commits, tests,
release process) live in `CONTRIBUTING.md`. Follow them for every change and
refer contributors to that file.
