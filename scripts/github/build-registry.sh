#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="${1:-$ROOT/build/github-registry}"
REPO="${2:-${GITHUB_REPOSITORY:-MuRongMoQing/phylang-registry}}"
PAGES_URL="${3:-}"
RUNTIME="$ROOT/build/phylang-registry"
PKGS="$ROOT/build/registry-packages"

[[ "$REPO" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || { echo "invalid repository: $REPO" >&2; exit 2; }
command -v go >/dev/null 2>&1 || { echo "go was not found" >&2; exit 127; }
command -v python3 >/dev/null 2>&1 || { echo "python3 was not found" >&2; exit 127; }

export PHYLANG_PACKAGE_PATH="$ROOT/stdlib/packages:$ROOT/community-packages${PHYLANG_PACKAGE_PATH:+:$PHYLANG_PACKAGE_PATH}"
rm -rf "$OUT" "$PKGS"
mkdir -p "$OUT" "$PKGS" "$ROOT/build"

(
  cd "$ROOT/runtime/portable-go"
  go test ./...
  go vet ./...
  go build -trimpath -o "$RUNTIME" .
)

mapfile -t manifests < <(find "$ROOT/stdlib/packages" "$ROOT/community-packages" -name phylang.package.toml -type f | LC_ALL=C sort)
[[ ${#manifests[@]} -gt 0 ]] || { echo "no package manifests found" >&2; exit 1; }

for manifest in "${manifests[@]}"; do
  pkg="$(dirname "$manifest")"
  "$RUNTIME" package validate "$pkg"
  "$RUNTIME" package audit "$pkg"
  "$RUNTIME" package lock "$pkg"
  "$RUNTIME" package test "$pkg"
  name="$(awk -F'"' '/^name[[:space:]]*=/{print $2; exit}' "$manifest")"
  version="$(awk -F'"' '/^version[[:space:]]*=/{print $2; exit}' "$manifest")"
  [[ -n "$name" && -n "$version" ]] || { echo "manifest name/version missing: $manifest" >&2; exit 1; }
  safe="${name//./-}"
  "$RUNTIME" package pack "$pkg" --out "$PKGS/${safe}-${version}.phypkg"
done

args=(package github-site "$PKGS" --out "$OUT" --repo "$REPO")
[[ -z "$PAGES_URL" ]] || args+=(--pages-url "$PAGES_URL")
"$RUNTIME" "${args[@]}"

cp "$ROOT/community-registry/schema/index.schema.json" "$OUT/index.schema.json"
cp "$ROOT/community-registry/schema/manifest.schema.json" "$OUT/manifest.schema.json"

python3 - "$OUT" <<'PY'
from pathlib import Path
import hashlib, json, sys
out=Path(sys.argv[1])
packages=sorted((out/'packages').glob('*.phypkg'))
if not packages:
    raise SystemExit('no .phypkg files were generated')
lines=[f"{hashlib.sha256(p.read_bytes()).hexdigest()}  packages/{p.name}\n" for p in packages]
(out/'SHA256SUMS.txt').write_text(''.join(lines),encoding='ascii',newline='\n')
for rel in ['index.json','health.json','registry-hosting.json']:
    with (out/rel).open('r',encoding='utf-8') as f:
        json.load(f)
required=['index.json','health.json','registry-hosting.json','SHA256SUMS.txt','index.schema.json','manifest.schema.json','.nojekyll']
missing=[x for x in required if not (out/x).exists()]
if missing:
    raise SystemExit(f'missing generated files: {missing}')
PY
printf '%s\n' "Built by PhyLang Community 0.6.2" > "$OUT/BUILD-INFO.txt"
echo "[PASS] GitHub registry site: $OUT"
