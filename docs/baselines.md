# Baseline reproduction

The README's v0.1.0 baseline table is backed by the machine-readable
[baseline receipt](baselines-v0.1.0.json). It records the exact tool commit,
binary checksum, corpus revisions, scope filters, warnings, skipped files, and
unrounded results measured on September 13, 2026.

This is a historical receipt. Reproduce it from the pinned revisions below;
when parser or classification behavior changes, write a new receipt instead of
silently replacing these numbers.

## Prerequisites

Install Git, Go 1.27.1, `jq`, `rsync`, and a C compiler. The recorded binary was
built with `go1.27.1 darwin/arm64`; its SHA-256 checksum is
`eddf1dd8ff7c3941e5bc57f388c97be55b42ef0a1c3480d2ca234abce675d4eb`.
Network access is required to fetch the pinned inputs.

Start in an empty temporary directory:

```sh
baseline_root=$(mktemp -d)
git clone https://github.com/SolenesInc/slopradar.git "$baseline_root/slopradar"
git -C "$baseline_root/slopradar" checkout --detach 6d64189b74f0c751217ef73cd7920b856c301b1a
go -C "$baseline_root/slopradar" build -o "$baseline_root/slopradar-bin" ./cmd/slopradar
```

Verify `go version` reports `go1.27.1` before continuing. A different OS or
architecture produces a different binary checksum but should not change the
scan output.

## Fetch the inputs

Go's standard library comes from the selected toolchain. The two Go modules
come from the module cache:

```sh
go_root=$(go env GOROOT)
go mod download golang.org/x/tools@v0.49.0
go mod download honnef.co/go/tools@v0.8.1
tools_root=$(go env GOMODCACHE)/golang.org/x/tools@v0.49.0
staticcheck_root=$(go env GOMODCACHE)/honnef.co/go/tools@v0.8.1
```

Clone the remaining inputs and detach them at their recorded revisions:

```sh
git clone https://github.com/colinhacks/zod.git "$baseline_root/zod-source"
git -C "$baseline_root/zod-source" checkout --detach 46da95720b7293f156ad9c683c14bd8ab9664c2f

git clone https://github.com/vitest-dev/vitest.git "$baseline_root/vitest-source"
git -C "$baseline_root/vitest-source" checkout --detach 2ce29d5fa758046e5453bd92b8ed6c9da9709bb5

git clone https://github.com/excalidraw/excalidraw.git "$baseline_root/excalidraw-source"
git -C "$baseline_root/excalidraw-source" checkout --detach afa3a653fc5d2b742adcbd5a6063187b056d2419

git clone https://github.com/SolenesInc/attn.git "$baseline_root/attn-source"
git -C "$baseline_root/attn-source" checkout --detach e05560650af06855452a9793a4f987ded9ca1d99
```

The source archive SHA-256 receipts were:

| Input | SHA-256 |
| --- | --- |
| zod | `b41d80e5c859df6d8a68a477e4b9ed2303b26ea80f1803570eb726e26acec5ec` |
| vitest | `e595da491a06c9f8cd4c42d5cfafbe698a1ea6ace16f435c59320beeab2bb712` |
| excalidraw | `cf365b9f653da86c248f4cd30f8bcf6379f166ecea9981af9e17ad14244e9c57` |
| attn | `f90b8c7b987ece9da6240a3b77ea77b3086a433a3eb69bb8d39eb1afb45a5255` |

## Assemble the measured scopes

The TypeScript prototype did not analyze declaration files, so the comparable
TypeScript scopes omit `*.d.ts`. The attn TypeScript scope keeps its unmarked
generated file in the input tree and excludes it through configuration.

```sh
mkdir -p "$baseline_root/scopes/zod/packages/zod/src"
rsync -a "$baseline_root/zod-source/packages/zod/src/" "$baseline_root/scopes/zod/packages/zod/src/"

mkdir -p "$baseline_root/scopes/vitest/packages"
rsync -a --exclude='*.d.ts' "$baseline_root/vitest-source/packages/" "$baseline_root/scopes/vitest/packages/"

mkdir -p "$baseline_root/scopes/excalidraw/packages" "$baseline_root/scopes/excalidraw/excalidraw-app"
rsync -a --exclude='*.d.ts' "$baseline_root/excalidraw-source/packages/" "$baseline_root/scopes/excalidraw/packages/"
rsync -a --exclude='*.d.ts' "$baseline_root/excalidraw-source/excalidraw-app/" "$baseline_root/scopes/excalidraw/excalidraw-app/"

mkdir -p "$baseline_root/scopes/attn-go" "$baseline_root/scopes/attn-ts/app"
rsync -a "$baseline_root/attn-source/cmd" "$baseline_root/attn-source/internal" "$baseline_root/scopes/attn-go/"
rsync -a --exclude='*.d.ts' "$baseline_root/attn-source/app/src" "$baseline_root/attn-source/app/lint" "$baseline_root/scopes/attn-ts/app/"
rsync -a --exclude='*.d.ts' "$baseline_root/attn-source/sdk" "$baseline_root/attn-source/plugins" "$baseline_root/scopes/attn-ts/"
```

Create `$baseline_root/scopes/attn-go/.slopradar.json` so the copied prompt
editor fixtures retain their test classification:

```json
{
  "test_globs": ["cmd/prompt-editor/test/"]
}
```

Create `$baseline_root/scopes/attn-ts/.slopradar.json` with the classification
rules used by the receipt:

```json
{
  "excludes": ["app/src/types/generated.ts"],
  "test_globs": ["app/src/test/", "plugins/attn-pi/test/", "sdk/plugin/test/"]
}
```

## Run the measurements

Define the receipt metric once. Its argument is the language-extension regular
expression shown in every README command:

```sh
erosion() {
  jq --arg files "$1" '[.functions[] | select(.bucket == "source" and (.file | test($files)))] as $f | (($f | map(select(.cc > 10) | .mass) | add) / ($f | map(.mass) | add))'
}
```

Run the ten scans without cache:

```sh
"$baseline_root/slopradar-bin" scan --no-cache --format=json "$go_root/src" | erosion '\.go$'
"$baseline_root/slopradar-bin" scan --no-cache --format=json "$tools_root" | erosion '\.go$'
"$baseline_root/slopradar-bin" scan --no-cache --format=json "$staticcheck_root" | erosion '\.go$'
"$baseline_root/slopradar-bin" scan --no-cache --format=json "$go_root/src/net/http" | erosion '\.go$'
"$baseline_root/slopradar-bin" scan --no-cache --format=json "$go_root/src/os" | erosion '\.go$'
"$baseline_root/slopradar-bin" scan --no-cache --format=json "$baseline_root/scopes/attn-go" | erosion '\.go$'
"$baseline_root/slopradar-bin" scan --no-cache --format=json "$baseline_root/scopes/zod" | erosion '\.(ts|tsx)$'
"$baseline_root/slopradar-bin" scan --no-cache --format=json "$baseline_root/scopes/vitest" | erosion '\.(ts|tsx)$'
"$baseline_root/slopradar-bin" scan --no-cache --format=json "$baseline_root/scopes/excalidraw" | erosion '\.(ts|tsx)$'
"$baseline_root/slopradar-bin" scan --no-cache --format=json "$baseline_root/scopes/attn-ts" | erosion '\.(ts|tsx)$'
```

Compare the unrounded erosion values, warnings, skipped-file details, function
counts, and mass totals with [the JSON receipt](baselines-v0.1.0.json). The
standard-library and `net/http` scans have no parser warnings.

Function counts use the same language filter as erosion. The historical
standard-library receipt's 39,709 count accidentally included 75 JavaScript
and 48 Python functions embedded under `$GOROOT/src`; its Go-only count was
39,586. The standard parser reports 39,587 Go functions because it also parses
`math/rand/v2.Rand.N`, the single source row the old parser missed.
