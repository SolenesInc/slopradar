# How slopradar analyzes code

slopradar reads source files from Git objects or a directory, parses each
supported language with a real parser, and reports two independent signals:
complexity mass concentrated in functions over cyclomatic complexity 10, and
exact token clones. This document holds the rules behind those numbers.

## Metrics

Cyclomatic complexity (CC) counts decision paths through a function. Every
function starts at CC 1, and each decision point listed under
[Languages and decision points](#languages-and-decision-points) adds one. SLOC
is the function's non-blank, non-comment source lines. Mass is CC × √SLOC, and
erosion is the share of repository function mass in functions with CC over 10.
The mass and erosion model, including the CC 10 threshold, comes from
[SlopCodeBench (arXiv:2603.24755)](https://arxiv.org/abs/2603.24755). The
analysis also builds on [scb-check](https://github.com/gabeorlanski/scb-check)
and Sebastian's
[essay on measuring code sloppiness](https://earendil.com/posts/measuring-code-sloppiness/).

Functions nested inside another function are folded into the outer function's
CC, SLOC, and mass. slopradar also keeps rows for nested functions so the report
can point inside a monolith, but only top-level rows enter erosion totals.

A clone pair is two ranges with the same tokens after comments are removed.
Clone detection compares exact lexical tokens, with a minimum of 50 tokens and
5 source lines per range, the minimums the [baseline receipts](baselines.md)
were measured with. Clone share is the share of source lines in such ranges. A
near-duplicate with renamed identifiers is not a clone.

Lower erosion and clone share are generally easier to maintain, but duplication
is not always wrong. Absolute erosion varies by language, so a mixed-language
bucket is useful for a repository's own trend, not for comparing languages or
unrelated projects.

A pull request diff compares each function by file and name, so a renamed or
moved function reads as removed plus new.

## Languages and decision points

Every function starts at CC 1. The following syntax adds one decision point.

| Language | Decision points |
| --- | --- |
| Go | `if`; `for` and `range`; each non-default expression-case, type-case, and communication-case clause; each `&&` and `\|\|` |
| TypeScript, TSX, JavaScript | `if`; `for`, `for…in`, and `for…of`; `while` and `do…while`; `catch`; each non-default `switch` case; ternary expressions; each `&&`, `\|\|`, and `??` |
| Python | `if` and each `elif`; `for`; `while`; `except`; each boolean operator; ternary expressions; each comprehension `for` and `if` clause; each `match` case |
| Rust | `if` and `if let`; `while` and `while let`; `for`; `loop`; each `match` arm; each `&&`, `\|\|`, and `?` |

Python docstrings count as comments. Rust's `?` counts as an explicit
error-flow decision, analogous to the explicit error checks visible in Go.

Go declarations without bodies contribute no function mass: their
implementations are outside the analyzed Go source. Empty implemented functions
and function literals still count. gocyclo includes bodyless declarations; the
difference is deliberate and matches the Go prototype the shipped analyzer was
validated against in the [baselines](baselines.md).

The Go analyzer uses the Go 1.27 standard parser and accepts generalized
`new(expression)` and generic methods. TypeScript, TSX, and JavaScript are
parsed with [Oxc](https://oxc.rs) through native archives shipped in the module;
Python and Rust use tree-sitter grammars.

### Function names

Rust method names include their owner: `Worker::run` for an inherent method,
`Service::run` for a trait default, and `<Worker as Service>::run` for a trait
implementation. Nested test functions retain their test bucket in drill-down
rows; totals still include only top-level functions.

TypeScript namespaces retain their complete owner paths, such as `A.Inner.run`,
including class and object members. Nested function names remain relative to
their enclosing function.

Inline Rust modules and nested Python classes retain their complete owner paths,
such as `alpha::inner::run` and `Outer.Inner.run`. Function boundaries stop owner
inheritance for nested local functions; their enclosing function row already
provides that context.

JavaScript and TypeScript class and object-literal members also retain their
owner, such as `Worker.run` and `worker.handlers.run`. Object ownership follows
variable, assignment, nested-property, and named-expression paths, such as
`holder.child.Inner.run`. Function boundaries stop owner inheritance. Callbacks
retain their binding and callee, such as `task.cb:map`.

Collection functions retain their key or position in the literal, such as
`handlers["a"]` and `items[0]`. Calls with multiple arguments also retain argument
positions, such as `task.cb:combine[1]`, so exchanging two callbacks does not hide
their individual changes.

### Python suite boundaries

Python tokens also carry parsed suite boundaries. Every tree-sitter `block`
with retained tokens contributes an unambiguous NUL-prefixed open/close pair,
anchored to the first and last retained physical token lines. A block containing
only removed comments or docstrings contributes neither marker. Tabs and spaces
that parse to the same nesting therefore compare equally, inline suites retain
the same block shape, and indentation inside a continued expression contributes
no suite boundary. String-content whitespace remains exact. This analysis does
not invoke a Python runtime.

## Source and test buckets

Every function and every clone line belongs to the source bucket or the tests
bucket. An optional `.slopradar.json` at the repository root adds exclusions and
test paths to the built-in classification:

```json
{
  "excludes": ["third_party/", "app/src/types/generated.ts"],
  "test_globs": ["integration/"]
}
```

Patterns use Go's `path.Match` syntax. A trailing slash matches a directory
and every descendant, including wildcard directories such as
`packages/*/generated/`; the same rule applies to `excludes` and `test_globs`.
Configuration globs use `/` as the path separator on the supported Unix
platforms. A backslash escapes the next glob character; it is not a separator.

Built-in exclusions are `vendor/`, `node_modules/`, `dist/`, `target/`, Go files
under `testdata/`, and dot directories. Built-in test paths are Go `_test.go`
files; JavaScript and TypeScript `.test.*`, `.spec.*`, and `__tests__/`; Python
`test_*.py`, `*_test.py`, and `tests/`; and Rust `tests/` plus `#[cfg(test)]`
items and `#[test]` functions. Test-directory conventions are language-specific:
a Go file in a `tests/` directory is source unless it ends in `_test.go`, and
TypeScript helpers in ordinary `tests/` directories are source unless their
filenames or configured globs classify them as tests.

A physical line containing both production and test-only code counts once in
each bucket. File-wide test classification counts that shared line only once;
clone pair lengths count distinct physical lines across both buckets.

### Rust conditional compilation

Rust recognizes compound `cfg` predicates that prove an item requires a test
build, such as `all(test, unix)` and `not(not(test))`. Predicates that can
permit a production build, such as `any(test, unix)`, stay in the source
bucket. Other configuration options remain unknown; their target or feature
values are never guessed. These combinations follow the
[Rust conditional compilation rules](https://doc.rust-lang.org/reference/conditional-compilation.html).
Rust attributes attached to a test-only item inherit its test bucket, including
companion attributes.

### Rust module resolution

Rust test scope follows external `mod` declarations through `name.rs`,
`name/mod.rs`, nested inline modules, and literal `#[path]` attributes. Each
import retains the directory used to resolve its child modules, including when
the same file is loaded through ordinary and `#[path]` declarations. A file
also reachable from production keeps its source bucket. Scope is recomputed
for each snapshot, including when an unchanged child comes from the cache;
parent-only scope changes appear in the diff. File-level `#![cfg(test)]` also
marks the file and its child modules as tests.

Module resolution uses only analyzed files, without running Cargo or expanding
macros. Crate roots follow standard Cargo entry-point layouts relative to the
nearest `Cargo.toml` directory (or the snapshot root for standalone sources):
`src/lib.rs`, `src/main.rs`, `build.rs`, binaries, examples, benches, and
integration tests. Custom Cargo target paths are not interpreted. Build-script
roots use `Cargo.toml` path metadata even when source files are generated or
excluded. Nested files named `main.rs` or `lib.rs` remain ordinary modules.
Conditional or unresolvable module paths produce warnings; scans retain
conservative source classification and diff and trend reject incomplete
analysis. Explicit `test_globs` can classify standalone test files.

## Generated files

Generated files are skipped when a leading comment contains Go's
`Code generated … DO NOT EDIT.`, `@generated`, or `linguist-generated` marker.
Marker text inside a string literal does not exclude a handwritten file.
Generator output without a marker must be listed in `excludes`.

## Line endings

Python accepts LF, CRLF, and CR physical line endings, including mixed endings.
Parser coordinates and SLOC follow those physical lines; clone tokens retain
the original literal bytes. Git line matching is disabled for files whose
physical lines differ from Git's LF-based coordinates. JavaScript line comments
end at LF, CRLF, CR, U+2028, or U+2029, so marker strings on the following
source line stay outside the comment.

## Paths in output

Output keeps ordinary UTF-8 paths unchanged. To represent every Unix filename
without collisions, path backslashes are written as `\\`, line breaks as `\n`
or `\r`, tabs as `\t`, other control characters as `\uHHHH`, and invalid UTF-8
bytes as `\xHH` with uppercase hex digits. Decode those escapes after decoding
JSON itself; a literal sequence such as `\xFF` is written as `\\xFF`.

## Revisions and the cache

Revision scans report the Git tree ID in `rev`, so commits with identical trees
produce identical output. Directory scans use `directory`. Diff `base` and `head`
and each trend point's `rev` retain commit IDs to identify the compared history.
`diff` resolves its base to the merge base of `--base` and `--head`; `trend`
walks first-parent history.

The cache stores analysis by blob SHA, language, and analyzer version under the
operating system's user cache directory. `--no-cache` bypasses it. Analyses
containing non-UTF-8 token bytes are recomputed so JSON caching cannot alter
their clone identities. Deleting the cache is always safe, and slopradar never
writes it into the repository being analyzed.

## Oversized files

Files above `max_file_bytes=2097152` are checked for a leading generated marker
within that bounded prefix. Recognized generated files are ignored; other
oversized files are skipped and named with both the limit and requested size.
Canonical Go generation directives must end on a complete line within the
prefix; leading block markers such as `/* @generated */` also work in
single-line files. This tripwire was set above attn revision `e05560650`'s
measured largest source file (`925234` bytes) and largest handwritten source
file (`256764` bytes). The full Go standard library scans without warnings or
incomplete-file skips; its oversized generated files, such as
`cmd/compile/internal/ssa/opGen.go` and `cmd/compile/internal/ssa/rewriteAMD64.go`,
are excluded by their leading markers.
