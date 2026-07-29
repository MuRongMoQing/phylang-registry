#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export PHYLANG_PACKAGE_PATH="$ROOT/stdlib/packages:$ROOT/community-packages${PHYLANG_PACKAGE_PATH:+:$PHYLANG_PACKAGE_PATH}"
RUNTIME="$ROOT/build/phylang-pr-validator"
mkdir -p "$ROOT/build"
(cd "$ROOT/runtime/portable-go" && go test ./... && go build -trimpath -o "$RUNTIME" .)
mapfile -t manifests < <(find "$ROOT/community-packages" "$ROOT/stdlib/packages" -name phylang.package.toml -type f | sort)
[[ ${#manifests[@]} -gt 0 ]] || { echo "No package manifests found"; exit 1; }
for manifest in "${manifests[@]}"; do
  dir="$(dirname "$manifest")"
  echo "::group::Validate $dir"
  "$RUNTIME" package validate "$dir"
  "$RUNTIME" package audit "$dir"
  "$RUNTIME" package lock "$dir"
  "$RUNTIME" package test "$dir"
  echo "::endgroup::"
done
