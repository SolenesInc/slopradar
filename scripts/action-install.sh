#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: action-install.sh <action-path> <install-dir>" >&2
  exit 2
fi

action_path=$1
install_dir=$2
mkdir -p "$install_dir"
(cd "$action_path" && env GOBIN="$install_dir" go install ./cmd/slopradar)
