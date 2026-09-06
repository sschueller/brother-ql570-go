# Contributing

Thanks for contributing to `brother-ql570-go`. Please follow these
guidelines.

## Workflow

1. Create a feature branch off `main` (default branch). Commit early and
   often.
2. Open a pull request against `main`.
3. Every commit must follow **Conventional Commits**:
   `type(scope): description`, e.g. `feat(daemon): add lazy printer
   connection` or `fix(protocol): correct status parsing`.
   Types: `feat` (new feature), `fix` (bug fix), `docs`, `refactor`, `test`,
   `chore`, `perf`, `build`, `ci`.
4. Add or update tests in the corresponding `*_test.go` file. Print-path
   changes need coverage in `pkg/ql` or `cmd/ql570`.
5. CI release: `.github/workflows/release.yml` runs release-please on push
   to `main`, derives the next version from conventional commits, and opens
   a release PR. Merging it creates the tagged GitHub release and
   cross-compiled binaries (linux amd64/arm64/armv7, darwin amd64/arm64,
   windows amd64, android arm64). Do not create tags or releases manually.
6. Break nothing for other platforms: when touching USB/backend code, at
   minimum `make build-darwin-arm64`, `make build-android-arm64`, and
   `make build-linux-arm64` must still compile.

## License

By contributing you agree that your contributions are licensed under the
MIT License (`LICENSE`).
