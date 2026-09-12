# Native Oxc bridge

The TypeScript, TSX and JavaScript analyzer calls Oxc through cgo. Published
modules contain static libraries for the platform matrix approved in the
project plan:

| Go platform | Rust target | Provenance |
| --- | --- | --- |
| `darwin/arm64` | `aarch64-apple-darwin` | `lib/darwin_arm64/provenance.json` |
| `darwin/amd64` | `x86_64-apple-darwin` | `lib/darwin_amd64/provenance.json` |
| `linux/arm64` | `aarch64-unknown-linux-gnu` | `lib/linux_arm64/provenance.json` |
| `linux/amd64` | `x86_64-unknown-linux-gnu` | `lib/linux_amd64/provenance.json` |

Compatibility receipt: archives with source SHA-256
`6689746710f36b452a0900f000f9585e345c6f48647d3e72d216f947d84228bf`
linked, passed the Go suite, and ran the CLI with Go `1.24.13` on both
architectures in `golang:1.24-bookworm`. `ldd --version` in that image reports
Debian GLIBC `2.36-9+deb12u13`; the linked executable uses libc, libm and
libgcc_s from the image.

Installing slopradar needs Go, cgo and a C compiler. It does not need Rust,
Cargo or Node. Rust `1.96.0`, pinned by `rust-toolchain.toml`, is only needed
to rebuild the libraries.

## Rebuild

Install the targets without changing the global Rust default:

```sh
rustup target add --toolchain 1.96.0 \
  aarch64-apple-darwin \
  x86_64-apple-darwin \
  aarch64-unknown-linux-gnu \
  x86_64-unknown-linux-gnu
```

Build and verify every archive:

```sh
python3 scripts/build-oxc.py
python3 scripts/build-oxc.py --check
```

The build reads the locked Cargo graph, remaps maintainer paths, and writes an
archive hash, Rust compiler identity, source hash and deployment target where
applicable into each provenance file. The generated `archive_<os>_<arch>.go`
file puts the archive hash into the Go package input, forcing Go to relink when
only a native archive changes.

## Ownership contract

`slopradar_oxc_analyze` receives file and source byte pointers with explicit
lengths and consumes them before returning. Rust never retains those pointers.
The returned byte buffer is Rust-owned and must be passed to
`slopradar_oxc_release`. Parser diagnostics return no functions, comments or
tokens. Rust panics are caught and converted to a nonzero bridge status before
they can unwind into Go.

The Rust dependency graph and source checksums are pinned in `Cargo.lock`.
Oxc's upstream notice is preserved in [LICENSE-OXC](LICENSE-OXC); every locked
crate's declared license can be audited with:

```sh
cargo +1.96.0 metadata --locked --format-version 1 \
  --manifest-path internal/oxc/rust/Cargo.toml
```
