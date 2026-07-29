#!/usr/bin/env python3
from __future__ import annotations
import argparse, json, re, struct
from pathlib import Path

REQUIRED=[
 '.gitattributes','.gitignore','README.md','DEPLOYMENT-GUIDE.zh-CN.md',
 'Deploy-To-GitHub-Windows10.ps1','Deploy-To-GitHub-Windows10.cmd',
 'Test-Deployment-Package.ps1','Build-Registry-Windows.ps1',
 'scripts/github/build-registry.sh','scripts/github/build-registry.ps1',
 'scripts/github/validate-package-pr.sh','scripts/github/simulate-actions.sh',
 '.github/workflows/validate.yml','.github/workflows/deploy.yml',
 '.github/workflows/integrity.yml','.github/workflows/release.yml',
 'runtime/portable-go/go.mod','tools/windows-x64/phylang.exe','tools/windows-arm64/phylang.exe',
 'community-registry/schema/index.schema.json','community-registry/schema/manifest.schema.json'
]

def pe_machine(path:Path)->int:
    data=path.read_bytes()
    if data[:2]!=b'MZ': raise RuntimeError(f'not PE: {path}')
    off=struct.unpack_from('<I',data,0x3c)[0]
    if data[off:off+4]!=b'PE\0\0': raise RuntimeError(f'PE signature missing: {path}')
    return struct.unpack_from('<H',data,off+4)[0]

def main():
    ap=argparse.ArgumentParser(); ap.add_argument('root',nargs='?',default='.')
    root=Path(ap.parse_args().root).resolve()
    missing=[p for p in REQUIRED if not (root/p).exists()]
    if missing: raise SystemExit('missing required deployment files:\n'+'\n'.join(missing))
    if pe_machine(root/'tools/windows-x64/phylang.exe')!=0x8664: raise SystemExit('wrong Windows x64 PE machine')
    if pe_machine(root/'tools/windows-arm64/phylang.exe')!=0xAA64: raise SystemExit('wrong Windows ARM64 PE machine')
    cfg=json.loads((root/'registry-hosting.json').read_text(encoding='utf-8-sig'))
    if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+',cfg['repository']): raise SystemExit('invalid repository in registry-hosting.json')
    for p in root.rglob('*.sh'):
        b=p.read_bytes()
        if b.startswith(b'\xef\xbb\xbf'): raise SystemExit(f'BOM is forbidden in shell script: {p}')
        if b'\r\n' in b: raise SystemExit(f'CRLF is forbidden in shell script: {p}')
    for p in root.rglob('*.ps1'):
        b=p.read_bytes()
        if not b.startswith(b'\xef\xbb\xbf'): raise SystemExit(f'PowerShell UTF-8 BOM missing: {p}')
        if b'\r\n' not in b: raise SystemExit(f'PowerShell CRLF missing: {p}')
    print('[PASS] deployment package layout, encoding and PE architecture')
if __name__=='__main__': main()
