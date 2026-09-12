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
`2e1fde5f600e7af12f40ddf37d8f00df2c6868bf5b0bfaf4ffc3e01f0a5e5db2`
linked, passed the Go suite, and ran the CLI with Go `1.24.13` on both
architectures in `golang:1.24-bookworm`. `ldd --version` in that image reports
Debian GLIBC `2.36-9+deb12u13`; the linked executable uses libc, libm and
libgcc_s from the image.

Installing slopradar needs Go, cgo and a C compiler. It does not need Rust,
Cargo or Node. Rust `1.96.0`, pinned by `rust-toolchain.toml`, is only needed
to rebuild the libraries.

Generated-file markers are recognized in leading comments before source code.
Marker text inside string literals does not exclude handwritten files. Unmarked
generator output can be excluded explicitly through `.slopradar.json`.

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
Oxc's upstream notice is preserved in [LICENSE-OXC](LICENSE-OXC). The checked-in
[THIRD_PARTY_LICENSES.txt](THIRD_PARTY_LICENSES.txt) maps every locked crate and
the Rust standard library to the license texts included in the bundle. Refresh
and verify it with:

```sh
python3 scripts/collect-oxc-licenses.py
python3 scripts/collect-oxc-licenses.py --check
```
