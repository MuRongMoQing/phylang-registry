#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
if [[ -d "$ROOT/deployment/github-registry" ]]; then
  DEPLOY_ROOT="$ROOT/deployment/github-registry"
else
  DEPLOY_ROOT="$ROOT"
fi
TMP="$(mktemp -d)"
trap 'kill "${HTTP_PID:-}" 2>/dev/null || true; rm -rf "$TMP"' EXIT
export GITHUB_REPOSITORY="${GITHUB_REPOSITORY:-MuRongMoQing/phylang-registry}"
export GITHUB_REPOSITORY_OWNER="${GITHUB_REPOSITORY%%/*}"
export GITHUB_REF="refs/heads/main"
export GITHUB_REF_NAME="main"
export GITHUB_SHA="$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || printf local-simulation)"
export RUNNER_TEMP="$TMP/runner-temp"
mkdir -p "$RUNNER_TEMP"

python3 "$ROOT/scripts/github/validate-deployment-layout.py" "$DEPLOY_ROOT"
python3 - "$DEPLOY_ROOT/.github/workflows" <<'PY'
from pathlib import Path
import sys
for p in sorted(Path(sys.argv[1]).glob('*.yml')):
    text=p.read_text(encoding='utf-8')
    if not text.startswith('name:') or '\njobs:' not in text:
        raise SystemExit(f'invalid workflow structure: {p}')
    print('[PASS] workflow structure',p.name)
PY
(cd "$ROOT/runtime/portable-go" && go test ./... && go vet ./...)
find "$ROOT/stdlib/packages" "$ROOT/community-packages" -name phylang.lock -type f -print0 | sort -z | xargs -0 sha256sum > "$TMP/locks.before"
bash "$ROOT/scripts/github/validate-package-pr.sh"
find "$ROOT/stdlib/packages" "$ROOT/community-packages" -name phylang.lock -type f -print0 | sort -z | xargs -0 sha256sum > "$TMP/locks.after"
cmp "$TMP/locks.before" "$TMP/locks.after"
bash "$ROOT/scripts/github/build-registry.sh" "$TMP/pages" "$GITHUB_REPOSITORY" "https://murongmoqing.github.io/phylang-registry/"
test -s "$TMP/pages/index.json"
test -s "$TMP/pages/health.json"
test -s "$TMP/pages/SHA256SUMS.txt"
tar -C "$TMP/pages" -czf "$TMP/registry-release.tar.gz" .
tar -tzf "$TMP/registry-release.tar.gz" >/dev/null
PORT="$(python3 - <<'PYPORT'
import socket
s=socket.socket(); s.bind(('127.0.0.1',0)); print(s.getsockname()[1]); s.close()
PYPORT
)"
(
 cd "$TMP/pages"
 python3 -m http.server "$PORT" --bind 127.0.0.1 >"$TMP/http.log" 2>&1
) & HTTP_PID=$!
ready=0
for i in {1..50}; do
  if curl -fsS "http://127.0.0.1:$PORT/health.json" >/dev/null; then ready=1; break; fi
  sleep 0.2
done
[[ "$ready" == 1 ]] || { cat "$TMP/http.log" >&2; exit 1; }
(cd "$ROOT/runtime/portable-go" && go build -trimpath -o "$TMP/phylang" .)
HOME="$TMP/home" "$TMP/phylang" package registry add simulated "http://127.0.0.1:$PORT/index.json"
HOME="$TMP/home" "$TMP/phylang" package registry check simulated
HOME="$TMP/home" "$TMP/phylang" package search ""
echo '[PASS] simulated GitHub Actions validate/build/release/integrity steps'
