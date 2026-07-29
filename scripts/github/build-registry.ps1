[CmdletBinding()]
param(
    [string]$OutDirectory = 'build\pages-local',
    [string]$Repository = 'MuRongMoQing/phylang-registry',
    [string]$PagesUrl = 'https://murongmoqing.github.io/phylang-registry/',
    [string]$RuntimePath,
    [switch]$BuildRuntimeWithGo
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$Utf8Strict = New-Object System.Text.UTF8Encoding -ArgumentList @($false, $true)

function Invoke-Native {
    param([string]$FilePath, [string[]]$Arguments = @())
    & $FilePath @Arguments
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) {
        throw "Native command failed ($exitCode): $FilePath $($Arguments -join ' ')"
    }
}

function Read-Utf8Text {
    param([Parameter(Mandatory = $true)][string]$Path)
    try {
        return [IO.File]::ReadAllText($Path, $Utf8Strict)
    } catch {
        throw "File is not valid UTF-8: $Path`n$($_.Exception.Message)"
    }
}

function Read-Utf8Json {
    param([Parameter(Mandatory = $true)][string]$Path)
    $text = Read-Utf8Text -Path $Path
    try {
        return ($text | ConvertFrom-Json)
    } catch {
        throw "Invalid UTF-8 JSON: $Path`n$($_.Exception.Message)"
    }
}

$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..')).Path
if ($Repository -notmatch '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$') {
    throw "Invalid repository: $Repository"
}
$out = if ([IO.Path]::IsPathRooted($OutDirectory)) { $OutDirectory } else { Join-Path $root $OutDirectory }
$packagesOut = Join-Path $root 'build\registry-packages'

if (-not $RuntimePath) {
    $arch = $env:PROCESSOR_ARCHITEW6432
    if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
    if ($arch -eq 'ARM64') { $RuntimePath = Join-Path $root 'tools\windows-arm64\phylang.exe' }
    else { $RuntimePath = Join-Path $root 'tools\windows-x64\phylang.exe' }
} elseif (-not [IO.Path]::IsPathRooted($RuntimePath)) {
    $RuntimePath = Join-Path $root $RuntimePath
}

if ($BuildRuntimeWithGo) {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'Go was not found.' }
    $RuntimePath = Join-Path $root 'build\phylang-registry.exe'
    Push-Location (Join-Path $root 'runtime\portable-go')
    try {
        Invoke-Native go @('test','./...')
        Invoke-Native go @('vet','./...')
        Invoke-Native go @('build','-trimpath','-o',$RuntimePath,'.')
    } finally {
        Pop-Location
    }
}
if (-not (Test-Path -LiteralPath $RuntimePath -PathType Leaf)) { throw "Runtime not found: $RuntimePath" }

$env:PHYLANG_PACKAGE_PATH = (Join-Path $root 'stdlib\packages') + ';' + (Join-Path $root 'community-packages')
Remove-Item -LiteralPath $out -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath $packagesOut -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $out,$packagesOut,(Join-Path $root 'build') | Out-Null

$manifests = @(
    Get-ChildItem -LiteralPath (Join-Path $root 'stdlib\packages'),(Join-Path $root 'community-packages') -Filter 'phylang.package.toml' -File -Recurse |
        Sort-Object FullName
)
if ($manifests.Count -eq 0) { throw 'No package manifests found.' }

foreach ($manifest in $manifests) {
    $dir = $manifest.Directory.FullName
    Invoke-Native $RuntimePath @('package','validate',$dir)
    Invoke-Native $RuntimePath @('package','audit',$dir)
    Invoke-Native $RuntimePath @('package','lock',$dir)
    Invoke-Native $RuntimePath @('package','test',$dir)

    # Windows PowerShell 5.1 treats BOM-less UTF-8 as the system ANSI code page
    # when Get-Content is used without -Encoding. Read the manifest with a
    # strict UTF-8 decoder so package metadata cannot be corrupted.
    $content = (Read-Utf8Text -Path $manifest.FullName) -split "`r?`n"
    $nameLine = $content | Where-Object { $_ -match '^name\s*=\s*"([^"]+)"' } | Select-Object -First 1
    $versionLine = $content | Where-Object { $_ -match '^version\s*=\s*"([^"]+)"' } | Select-Object -First 1
    if (-not $nameLine -or -not $versionLine) { throw "Manifest name/version missing: $($manifest.FullName)" }
    [void]($nameLine -match '^name\s*=\s*"([^"]+)"'); $name = $Matches[1]
    [void]($versionLine -match '^version\s*=\s*"([^"]+)"'); $version = $Matches[1]
    $file = ($name -replace '\.','-') + '-' + $version + '.phypkg'
    Invoke-Native $RuntimePath @('package','pack',$dir,'--out',(Join-Path $packagesOut $file))
}

$siteArgs = @('package','github-site',$packagesOut,'--out',$out,'--repo',$Repository)
if ($PagesUrl) { $siteArgs += @('--pages-url',$PagesUrl) }
Invoke-Native $RuntimePath $siteArgs
Copy-Item -LiteralPath (Join-Path $root 'community-registry\schema\index.schema.json') -Destination (Join-Path $out 'index.schema.json') -Force
Copy-Item -LiteralPath (Join-Path $root 'community-registry\schema\manifest.schema.json') -Destination (Join-Path $out 'manifest.schema.json') -Force

$lines = foreach ($pkg in Get-ChildItem -LiteralPath (Join-Path $out 'packages') -Filter '*.phypkg' -File | Sort-Object Name) {
    $hash = (Get-FileHash -LiteralPath $pkg.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    "$hash  packages/$($pkg.Name)"
}
[IO.File]::WriteAllText((Join-Path $out 'SHA256SUMS.txt'),(($lines -join "`n") + "`n"),(New-Object Text.ASCIIEncoding))
[IO.File]::WriteAllText((Join-Path $out 'BUILD-INFO.txt'),"Built by PhyLang Community 0.6.2-R4`n",(New-Object Text.UTF8Encoding($false)))
foreach ($required in @('index.json','health.json','registry-hosting.json','SHA256SUMS.txt','index.schema.json','manifest.schema.json','.nojekyll')) {
    if (-not (Test-Path -LiteralPath (Join-Path $out $required) -PathType Leaf)) { throw "Generated file missing: $required" }
}

# Do not use Get-Content without -Encoding here. The generated files are
# UTF-8 without BOM. On Windows PowerShell 5.1 the default decoder is ANSI,
# which can turn valid Chinese UTF-8 into malformed JSON.
$index = Read-Utf8Json -Path (Join-Path $out 'index.json')
$health = Read-Utf8Json -Path (Join-Path $out 'health.json')
if ($index.schema -ne 'phylang.registry/v2') { throw "Unexpected index schema: $($index.schema)" }
if (@($index.packages).Count -eq 0) { throw 'Generated registry contains no packages.' }
if ($health.ok -ne $true) { throw 'Generated health.json does not report ok=true.' }

Write-Host "[PASS] Windows registry build: $out"
