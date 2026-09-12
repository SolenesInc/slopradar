#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: action-path.sh <action-path>" >&2
  exit 2
fi

action_path=$1
if [[ ! -f "$action_path/go.mod" || ! -f "$action_path/go.sum" ]]; then
  echo "action path must contain go.mod and go.sum: ${action_path}" >&2
  exit 1
fi

(cd "$action_path" && pwd -P)
