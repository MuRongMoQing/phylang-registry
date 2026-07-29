#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "${1:-$(dirname "$0")/../..}" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/home" "$TMP/repo"
cp -a "$ROOT/." "$TMP/repo/"
rm -rf "$TMP/repo/.git" "$TMP/repo/build" "$TMP/repo/.phylang-deployment-backup"
export HOME="$TMP/home"
git config --global core.autocrlf true
git config --global core.eol crlf
git config --global core.safecrlf true
cd "$TMP/repo"
git init -q
git config core.autocrlf false
git config core.eol lf
git config core.safecrlf true
git checkout -q -B main
git -c core.autocrlf=false -c core.eol=lf -c core.safecrlf=false add --all
if git diff --cached --quiet; then
  echo 'expected staged files but index is empty' >&2
  exit 1
fi
git ls-files --error-unmatch .gitattributes >/dev/null
echo '[PASS] simulated Git for Windows CRLF/global-EOL conflict did not block git add.'
