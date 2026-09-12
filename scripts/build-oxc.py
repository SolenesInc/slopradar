#!/usr/bin/env python3

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


TARGETS = {
    "darwin_arm64": "aarch64-apple-darwin",
    "darwin_amd64": "x86_64-apple-darwin",
    "linux_arm64": "aarch64-unknown-linux-gnu",
    "linux_amd64": "x86_64-unknown-linux-gnu",
}


def output(arguments, **kwargs):
    return subprocess.check_output(arguments, text=True, **kwargs).strip()


def fingerprint(crate):
    digest = hashlib.sha256()
    files = sorted([crate / "Cargo.toml", crate / "Cargo.lock", *crate.glob("src/**/*.rs")])
    for file in files:
        digest.update(str(file.relative_to(crate)).encode() + b"\0")
        digest.update(file.read_bytes())
    return digest.hexdigest()


def check(bridge, platforms):
    source_hash = fingerprint(bridge / "rust")
    recipe_hash = hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    for platform in platforms:
        destination = bridge / "lib" / platform
        receipt = json.loads((destination / "provenance.json").read_text())
        archive = destination / receipt["archive"]
        archive_hash = hashlib.sha256(archive.read_bytes()).hexdigest()
        marker = (bridge / ("archive_" + platform + ".go")).read_text()
        if receipt["source_sha256"] != source_hash or receipt["build_script_sha256"] != recipe_hash:
            raise SystemExit(platform + ": native archive is stale; rebuild with scripts/build-oxc.py")
        if archive_hash != receipt["archive_sha256"] or archive_hash not in marker:
            raise SystemExit(platform + ": native archive or Go build fingerprint differs from provenance; rebuild with scripts/build-oxc.py")
        print(platform + ": archive, source, and Go fingerprint verified")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--target", action="append", choices=TARGETS)
    parser.add_argument("--target-dir", type=Path)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    bridge = root / "internal/oxc"
    crate = bridge / "rust"
    if args.check:
        check(bridge, args.target or TARGETS)
        return
    toolchain = "1.96.0"
    cargo = ["cargo", "+" + toolchain]
    metadata = json.loads(output(cargo + ["metadata", "--locked", "--no-deps", "--format-version", "1", "--manifest-path", str(crate / "Cargo.toml")]))
    package = next(p for p in metadata["packages"] if Path(p["manifest_path"]).parent == crate)
    library = next(t["name"] for t in package["targets"] if "staticlib" in t["crate_types"])
    archive_name = "lib" + library + ".a"
    target_dir = args.target_dir or Path(tempfile.mkdtemp(prefix="slopradar-oxc-build-"))
    source_hash = fingerprint(crate)
    cargo_home = Path(os.environ.get("CARGO_HOME", Path.home() / ".cargo"))
    environment = {**os.environ, "CARGO_INCREMENTAL": "0"}
    environment["RUSTFLAGS"] = " ".join([
        "--remap-path-prefix=" + str(root) + "=slopradar",
        "--remap-path-prefix=" + str(cargo_home) + "=cargo",
    ])
    compiler = output(["rustc", "+" + toolchain, "--version", "--verbose"])
    for platform in args.target or TARGETS:
        target = TARGETS[platform]
        if fingerprint(crate) != source_hash:
            raise SystemExit("Rust sources changed during the platform build; rerun to produce matching archives")
        subprocess.run(cargo + ["build", "--locked", "--release", "--manifest-path", str(crate / "Cargo.toml"), "--target", target, "--target-dir", str(target_dir)], env=environment, check=True)
        if fingerprint(crate) != source_hash:
            raise SystemExit("Rust sources changed during the platform build; rerun to produce matching archives")
        destination = bridge / "lib" / platform
        destination.mkdir(parents=True, exist_ok=True)
        archive = destination / archive_name
        shutil.copy2(target_dir / target / "release" / archive_name, archive)
        receipt = {
            "platform": platform,
            "rust_target": target,
            "compiler": compiler,
            "source_sha256": source_hash,
            "build_script_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
            "cargo_lock_sha256": hashlib.sha256((crate / "Cargo.lock").read_bytes()).hexdigest(),
            "archive": archive.name,
            "archive_bytes": archive.stat().st_size,
            "archive_sha256": hashlib.sha256(archive.read_bytes()).hexdigest(),
        }
        if platform.startswith("darwin_"):
            receipt["deployment_target"] = output(["rustc", "+" + toolchain, "--print", "deployment-target", "--target", target], env=environment)
        (destination / "provenance.json").write_text(json.dumps(receipt, indent=2) + "\n")
        go_os, go_arch = platform.split("_")
        (bridge / ("archive_" + platform + ".go")).write_text(
            "//go:build " + go_os + " && " + go_arch + "\n\n"
            "// Code generated by scripts/build-oxc.py; DO NOT EDIT.\n\n"
            "package oxc\n\n"
            'const nativeArchiveSHA256 = "' + receipt["archive_sha256"] + '"\n'
        )
        print(json.dumps(receipt))


if __name__ == "__main__":
    main()
