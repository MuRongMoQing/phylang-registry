[CmdletBinding()]
param([string]$Root = $PSScriptRoot, [switch]$RunRuntimeSelfTest)
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$Root = (Resolve-Path -LiteralPath $Root).Path
$required = @(
    'Deploy-To-GitHub-Windows10.ps1','Deploy-To-GitHub-Windows10.cmd',
    'Resume-GitHub-Deployment-Windows10.ps1','Resume-GitHub-Deployment-Windows10.cmd',
    'Repair-Existing-GitHub-Deployment-Windows10.ps1','Repair-Existing-GitHub-Deployment-Windows10-R8.ps1',
    'Build-Registry-Windows.ps1','Remove-Failed-Deployment.ps1','Remove-Failed-Deployment.cmd',
    'scripts\github\build-registry.sh',
    'scripts\github\build-registry.ps1','scripts\github\validate-package-pr.sh',
    'scripts\github\simulate-windows-git-eol.sh',
    '.github\workflows\validate.yml','.github\workflows\deploy.yml',
    '.github\workflows\integrity.yml','.github\workflows\release.yml',
    'tools\windows-x64\phylang.exe','tools\windows-arm64\phylang.exe',
    'runtime\portable-go\go.mod','runtime\portable-go\registry.go',
    'runtime\portable-go\registry_path_test.go','runtime\portable-go\package_test.go',
    'runtime\portable-go\package_cross_platform_determinism_test.go','registry-hosting.json'
)
$missing = @($required | Where-Object { -not (Test-Path -LiteralPath (Join-Path $Root $_)) })
if ($missing.Count -gt 0) { throw "Deployment files missing:`n$($missing -join "`n")" }

foreach ($path in Get-ChildItem -LiteralPath $Root -Filter '*.ps1' -File -Recurse) {
    $bytes = [IO.File]::ReadAllBytes($path.FullName)
    if ($bytes.Length -lt 3 -or $bytes[0] -ne 0xEF -or $bytes[1] -ne 0xBB -or $bytes[2] -ne 0xBF) {
        throw "UTF-8 BOM missing: $($path.FullName)"
    }
    $raw = [Text.Encoding]::UTF8.GetString($bytes,3,$bytes.Length-3)
    if ($raw -match '(?<!\r)\n') { throw "PowerShell file contains LF without CR: $($path.FullName)" }
    $tokens = $null
    $parseErrors = $null
    [void][Management.Automation.Language.Parser]::ParseFile($path.FullName,[ref]$tokens,[ref]$parseErrors)
    if ($parseErrors.Count -gt 0) { throw "PowerShell parser error in $($path.FullName): $($parseErrors[0].Message)" }
}

foreach ($path in Get-ChildItem -LiteralPath $Root -Filter '*.cmd' -File -Recurse) {
    $bytes = [IO.File]::ReadAllBytes($path.FullName)
    if ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) {
        throw "CMD file must not contain UTF-8 BOM: $($path.FullName)"
    }
    if ($bytes | Where-Object { $_ -gt 0x7F }) { throw "CMD file must contain ASCII only: $($path.FullName)" }
    $raw = [Text.Encoding]::ASCII.GetString($bytes)
    if ($raw -match '(?<!\r)\n') { throw "CMD file contains LF without CR: $($path.FullName)" }
}

foreach ($path in Get-ChildItem -LiteralPath $Root -Filter '*.sh' -File -Recurse) {
    $bytes = [IO.File]::ReadAllBytes($path.FullName)
    if ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) {
        throw "Shell script must not contain UTF-8 BOM: $($path.FullName)"
    }
    $raw = [Text.Encoding]::UTF8.GetString($bytes)
    if ($raw.Contains("`r")) { throw "Shell script contains CRLF/CR: $($path.FullName)" }
}

$utf8Strict = New-Object System.Text.UTF8Encoding -ArgumentList @($false, $true)
$configText = [IO.File]::ReadAllText((Join-Path $Root 'registry-hosting.json'), $utf8Strict)
$config = $configText | ConvertFrom-Json
if ($config.repository -notmatch '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$') { throw 'Invalid repository setting.' }

if ($RunRuntimeSelfTest) {
    $arch = $env:PROCESSOR_ARCHITEW6432
    if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
    $runtime = if ($arch -eq 'ARM64') { Join-Path $Root 'tools\windows-arm64\phylang.exe' } else { Join-Path $Root 'tools\windows-x64\phylang.exe' }
    $testRoot = Join-Path ([IO.Path]::GetTempPath()) ('phylang-deployment-selftest-' + [Guid]::NewGuid().ToString('N'))
    $testHome = Join-Path $testRoot 'home'
    $testRegistry = Join-Path $testRoot 'registry'
    $testPackages = Join-Path $testRegistry 'packages'
    $testPackageSource = Join-Path $testRoot 'package-source'
    $testPackageArchive = Join-Path $testPackages 'community-deployment-self-test-0.1.0.phypkg'
    $testIndex = Join-Path $testRegistry 'index.json'
    $savedEnvironment = @{
        PHYLANG_PACKAGE_PATH = $env:PHYLANG_PACKAGE_PATH
        PHYLANG_HOME = $env:PHYLANG_HOME
        PHYLANG_REGISTRY_URL = $env:PHYLANG_REGISTRY_URL
        PHYLANG_GITHUB_REGISTRY = $env:PHYLANG_GITHUB_REGISTRY
    }
    New-Item -ItemType Directory -Path $testHome,$testPackages -Force | Out-Null
    Push-Location -LiteralPath $Root
    try {
        $env:PHYLANG_PACKAGE_PATH = (Join-Path $Root 'stdlib\packages') + ';' + (Join-Path $Root 'community-packages')
        $env:PHYLANG_HOME = $testHome
        Remove-Item Env:PHYLANG_REGISTRY_URL -ErrorAction SilentlyContinue
        Remove-Item Env:PHYLANG_GITHUB_REGISTRY -ErrorAction SilentlyContinue

        & $runtime version
        if ($LASTEXITCODE -ne 0) { throw 'Runtime version check failed.' }

        # Build a real local package registry whose index uses a relative
        # packages/*.phypkg URL. This catches both Windows drive-letter parsing
        # and relative URL resolution against C:\...\index.json.
        & $runtime package init $testPackageSource --name community.deployment-self-test
        if ($LASTEXITCODE -ne 0) { throw 'Regression package initialization failed.' }
        & $runtime package pack $testPackageSource --out $testPackageArchive
        if ($LASTEXITCODE -ne 0) { throw 'Regression package archive build failed.' }
        $testPackageHash = (Get-FileHash -LiteralPath $testPackageArchive -Algorithm SHA256).Hash.ToLowerInvariant()
        $fixtureObject = [ordered]@{
            schema = 'phylang.registry/v2'
            name = 'deployment-self-test'
            updated = '2026-07-29T00:00:00Z'
            packages = @(
                [ordered]@{
                    name = 'community.deployment-self-test'
                    description = 'Windows native-path and relative-package regression fixture'
                    versions = @(
                        [ordered]@{
                            version = '0.1.0'
                            url = 'packages/community-deployment-self-test-0.1.0.phypkg'
                            sha256 = $testPackageHash
                            phylang = '>=0.6.0 <0.7.0'
                        }
                    )
                }
            )
        }
        $fixture = ($fixtureObject | ConvertTo-Json -Depth 10) + "`n"
        [IO.File]::WriteAllText($testIndex, $fixture, (New-Object Text.UTF8Encoding($false)))

        # This is an explicit regression test for native Windows paths such as
        # C:\Users\...\index.json. The runtime must treat the drive prefix as a
        # filesystem path, and must resolve packages/*.phypkg beside the index.
        & $runtime package registry add deployment-self-test $testIndex
        if ($LASTEXITCODE -ne 0) { throw 'Windows absolute registry path test failed.' }
        & $runtime package registry check deployment-self-test
        if ($LASTEXITCODE -ne 0) { throw 'Windows local registry check failed.' }
        & $runtime package fetch community.deployment-self-test '^0.1.0'
        if ($LASTEXITCODE -ne 0) { throw 'Windows relative registry package download test failed.' }
        & $runtime package info community.deployment-self-test '=0.1.0'
        if ($LASTEXITCODE -ne 0) { throw 'Windows registry package installation verification failed.' }

        & $runtime self-test
        if ($LASTEXITCODE -ne 0) { throw 'Runtime self-test failed.' }
    } finally {
        Pop-Location
        foreach ($name in $savedEnvironment.Keys) {
            $value = $savedEnvironment[$name]
            if ($null -eq $value) {
                Remove-Item ("Env:" + $name) -ErrorAction SilentlyContinue
            } else {
                Set-Item ("Env:" + $name) -Value $value
            }
        }
        Remove-Item -LiteralPath $testRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
# Static guard against the Windows PowerShell 5.1 BOM-less UTF-8 bug that caused R3.
$buildScriptText = [IO.File]::ReadAllText((Join-Path $Root 'scripts\github\build-registry.ps1'), $utf8Strict)
if ($buildScriptText -match "Get-Content[^`r`n]+index\.json[^`r`n]+ConvertFrom-Json") {
    throw 'Unsafe default-encoding JSON read detected in build-registry.ps1.'
}
$deployText = [IO.File]::ReadAllText((Join-Path $Root 'Deploy-To-GitHub-Windows10.ps1'), $utf8Strict)
foreach ($requiredFragment in @(
    "'config','core.eol','lf'",
    "'-c','core.safecrlf=false'",
    '-RemoveLocalGitRepository -ConfirmLocalPath $root',
    '"head_sha=$HeadSha"',
    ("repos/`$Repository/actions/workflows/`$Workflow/runs"),
    ('$null -ne $response.PSObject.Properties[' + "'workflow_runs'" + ']'),
    '$pushCompleted = $true'
)) {
    if ($deployText.IndexOf($requiredFragment, [StringComparison]::Ordinal) -lt 0) {
        throw "Deployment script is missing required Git EOL/rollback protection: $requiredFragment"
    }
}

$gitignoreText = [IO.File]::ReadAllText((Join-Path $Root '.gitignore'), $utf8Strict)
foreach ($requiredIgnore in @('/.phylang-deployment-backup/','/.phylang-r*-repair-backup/','/.phylang-r9-network-diagnostics/','/deployment-report.json')) {
    if (($gitignoreText -split "`r?`n") -notcontains $requiredIgnore) {
        throw "Missing deployment-residue ignore rule: $requiredIgnore"
    }
}
$repairText = [IO.File]::ReadAllText((Join-Path $Root 'Repair-Existing-GitHub-Deployment-Windows10.ps1'), $utf8Strict)
foreach ($repairFragment in @(
    '.phylang-deployment-backup/CODEOWNERS',
    '.phylang-deployment-backup/registry-hosting.json',
    "`$status -in @(' D','D ')",
    "'reset','--mixed',`$OriginalHead",
    "'HOTFIX-R8-README.zh-CN.md'",
    "'HOTFIX-R9-README.zh-CN.md'",
    "'Resume-R9-Repair-Push-Windows10.ps1'",
    "'Diagnose-GitHub-HTTPS-TLS-Windows10.ps1'",
    "'.gitignore'"
)) {
    if ($repairText.IndexOf($repairFragment,[StringComparison]::Ordinal) -lt 0) {
        throw "R9 known-residue/transport-preservation repair guard missing: $repairFragment"
    }
}

$attributesText = [IO.File]::ReadAllText((Join-Path $Root '.gitattributes'), $utf8Strict)
if ($attributesText -notmatch '(?m)^\.gitattributes text eol=lf$') {
    throw '.gitattributes must explicitly normalize itself to LF.'
}
if ($deployText -match '\._?headSha\s*-eq') { throw 'Unsafe direct headSha property filter remains in deployment script.' }
if ($deployText -match "'run','list'") { throw 'Deployment script must use the workflow-runs REST endpoint instead of gh run list.' }
if ($attributesText -notmatch '(?m)^\*\.phy text eol=lf$') {
    throw '.gitattributes must force PhyLang source files to LF.'
}
if ($attributesText -notmatch '(?m)^\*\.lock text eol=lf$') {
    throw '.gitattributes must force lock files to LF.'
}
$packageSource = [IO.File]::ReadAllText((Join-Path $Root 'runtime\portable-go\package.go'), $utf8Strict)
foreach ($fragment in @('canonicalPackageFileBytes','bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))','Method:   zip.Store')) {
    if ($packageSource.IndexOf($fragment,[StringComparison]::Ordinal) -lt 0) {
        throw "Cross-platform package determinism fix missing: $fragment"
    }
}
$deployWorkflow = [IO.File]::ReadAllText((Join-Path $Root '.github\workflows\deploy.yml'), $utf8Strict)
if ($deployWorkflow -match '(?m)^\s+push:\s*$') {
    throw 'Pages workflow must not deploy directly on push before validation succeeds.'
}
if ($deployWorkflow -notmatch 'Verify source commit already passed validation') {
    throw 'Pages workflow validation gate is missing.'
}
$repairR9Text = [IO.File]::ReadAllText((Join-Path $Root 'Repair-Existing-GitHub-Deployment-Windows10.ps1'), $utf8Strict)
foreach ($fragment in @('Select-GitTransportMode','Push-WithRetry','The local repair commit was preserved','http.sslBackend=schannel')) {
    if ($repairR9Text.IndexOf($fragment,[StringComparison]::Ordinal) -lt 0) { throw "R9 transport recovery guard missing: $fragment" }
}
foreach ($requiredR9File in @('Resume-R9-Repair-Push-Windows10.ps1','Diagnose-GitHub-HTTPS-TLS-Windows10.ps1','HOTFIX-R9-README.zh-CN.md')) {
    if (-not (Test-Path -LiteralPath (Join-Path $Root $requiredR9File) -PathType Leaf)) { throw "R9 file missing: $requiredR9File" }
}
Write-Host '[PASS] Windows deployment package layout, encoding, UTF-8 JSON decoding, native-path registry access, relative package resolution, cross-platform lock/archive determinism, validation-gated Pages deployment, Git EOL isolation, workflow diagnostics, known R5 backup-residue cleanup, repair workflow, original-HEAD rollback, TLS transport diagnostics, retryable push preservation and PowerShell syntax.'
