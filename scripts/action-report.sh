#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: action-report.sh <base-ref> <trend-months> <report-path>" >&2
  exit 2
fi

base_ref=$1
trend_months=$2
report_path=$3

if [[ -z "$base_ref" ]]; then
  echo "base input is empty; set base to the pull request base branch" >&2
  exit 1
fi
if ! git check-ref-format "refs/heads/${base_ref}" >/dev/null; then
  echo "base input is not a valid branch name: ${base_ref}" >&2
  exit 1
fi
if [[ ! "$trend_months" =~ ^(0|[1-9][0-9]*)$ ]]; then
  echo "trend-months must be 0 or a positive integer, got: ${trend_months}" >&2
  exit 1
fi

refspec="+refs/heads/${base_ref}:refs/remotes/origin/${base_ref}"
fetch_args=(--no-tags --prune)
if [[ "$(git rev-parse --is-shallow-repository)" == "true" ]]; then
  fetch_args+=(--unshallow)
fi
git fetch "${fetch_args[@]}" origin "$refspec"

diff_args=(diff --base "refs/remotes/origin/${base_ref}" --head HEAD --format md)
if [[ "$trend_months" != "0" ]]; then
  diff_args+=(--trend "$trend_months")
fi
slopradar "${diff_args[@]}" > "$report_path"
