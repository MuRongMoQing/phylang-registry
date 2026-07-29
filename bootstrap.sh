#!/usr/bin/env bash
set -euo pipefail
repo="${1:-MuRongMoQing/phylang-registry}"
visibility="${2:---public}"
[[ "$repo" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || { echo "usage: ./bootstrap.sh OWNER/REPOSITORY [--public|--private]" >&2; exit 2; }
case "$visibility" in --public|--private) ;; *) echo "visibility must be --public or --private" >&2; exit 2;; esac
command -v git >/dev/null || { echo "git was not found" >&2; exit 127; }
command -v gh >/dev/null || { echo "GitHub CLI was not found" >&2; exit 127; }
command -v go >/dev/null || { echo "go was not found" >&2; exit 127; }
command -v python3 >/dev/null || { echo "python3 was not found" >&2; exit 127; }
gh auth status --hostname github.com
owner="${repo%%/*}"; name="${repo#*/}"
[[ "${owner,,}" == "murongmoqing" ]] || { echo "expected owner MuRongMoQing" >&2; exit 2; }
python3 scripts/github/validate-deployment-layout.py .
bash scripts/github/build-registry.sh build/pages-local "$repo" "https://$owner.github.io/$name/"
python3 - "$repo" <<'PY'
from pathlib import Path
import json,sys
repo=sys.argv[1]; owner,name=repo.split('/',1)
p=Path('registry-hosting.json')
c=json.loads(p.read_text(encoding='utf-8'))
c.update(repository=repo,pages_base=f'https://{owner}.github.io/{name}/',release_base=f'https://github.com/{repo}/releases/download/',description='PhyLang 0.6.2 community registry')
p.write_text(json.dumps(c,ensure_ascii=False,indent=2)+'\n',encoding='utf-8',newline='\n')
PY
[[ -d .git ]] || git init
git config core.autocrlf false
git config core.safecrlf true
git config user.name >/dev/null 2>&1 || git config user.name MuRongMoQing
git config user.email >/dev/null 2>&1 || git config user.email MuRongMoQing@users.noreply.github.com
git checkout -B main
git add --all
git update-index --chmod=+x -- bootstrap.sh scripts/github/*.sh
git diff --cached --quiet || git commit -m 'Deploy PhyLang Registry 0.6.2'
if gh repo view "$repo" --json nameWithOwner >/dev/null 2>&1; then
  git remote get-url origin >/dev/null 2>&1 && git remote set-url origin "https://github.com/$repo.git" || git remote add origin "https://github.com/$repo.git"
else
  gh repo create "$repo" "$visibility" --source=. --remote=origin
fi
gh api --method PUT "repos/$repo/actions/permissions/workflow" -H 'X-GitHub-Api-Version: 2026-03-10' -f default_workflow_permissions=read -F can_approve_pull_request_reviews=false
if gh api "repos/$repo/pages" -H 'X-GitHub-Api-Version: 2026-03-10' >/dev/null 2>&1; then
  gh api --method PUT "repos/$repo/pages" -H 'X-GitHub-Api-Version: 2026-03-10' -f build_type=workflow >/dev/null
else
  gh api --method POST "repos/$repo/pages" -H 'X-GitHub-Api-Version: 2026-03-10' -f build_type=workflow >/dev/null
fi
git push --set-upstream origin main
printf '[PASS] Repository: https://github.com/%s\n[INFO] Pages: https://%s.github.io/%s/\n' "$repo" "$owner" "$name"
