# Development

## Toolchain

Use Go 1.27 or newer, cgo, and a C compiler. Install
[golangci-lint v2.13.2](https://github.com/golangci/golangci-lint/releases/tag/v2.13.2)
to match CI. Node runs the action script tests, and Python 3 runs the native
archive checks.

The module ships Oxc 0.149.0 native archives for Linux and macOS on arm64 and
amd64, so building and installing slopradar needs neither Rust, Cargo, nor Node.
Rebuilding those archives needs the pinned Rust 1.96.0 toolchain; the measured
platform compatibility and the rebuild procedure are in
[`internal/oxc/README.md`](../internal/oxc/README.md).

## Checks

```sh
golangci-lint run ./...
golangci-lint fmt --diff
go test -race ./...
go vet ./...
node --test scripts/*.test.cjs
python3 scripts/build-oxc.py --check
```

`golangci-lint fmt` applies formatting. The lint configuration enables the
standard golangci-lint checks (Staticcheck, govet, errcheck, ineffassign, and
unused) plus gofmt formatting.

## CI

Every push and pull request runs four jobs:

- `lint`: golangci-lint and the formatting diff.
- `test`: the archive check, `go test -race`, `go vet`, and a `go install` with
  only Go on `PATH`, on `ubuntu-22.04`, `ubuntu-22.04-arm`, `macos-15-intel`,
  and `macos-14`.
- `action-scripts`: the Node tests for the comment, summary, and boundary
  scripts.
- `native-notices`: the third-party license inventory for the native archives
  against the pinned Rust toolchain.

Pull requests to this repository also get their own slopradar comment from
[`.github/workflows/slopradar.yml`](../.github/workflows/slopradar.yml).

## Working agreement

[`AGENTS.md`](../AGENTS.md) is the working agreement for every contributor,
human or agent: automated tests are required and wait on real signals, every
number in code or documentation carries the measurement or source behind it,
code explains itself without prose comments, and identical input trees produce
identical output bytes.
