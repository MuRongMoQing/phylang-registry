[CmdletBinding()]
param(
    [string]$OutDirectory = 'build\pages-local',
    [string]$Repository = 'MuRongMoQing/phylang-registry',
    [string]$PagesUrl = 'https://murongmoqing.github.io/phylang-registry/',
    [string]$RuntimePath,
    [switch]$BuildRuntimeWithGo,
    [switch]$KeepFailedArtifacts
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath $PSScriptRoot).Path
$invoke = @{
    OutDirectory = $OutDirectory
    Repository = $Repository
    PagesUrl = $PagesUrl
    BuildRuntimeWithGo = $BuildRuntimeWithGo
}
if ($RuntimePath) { $invoke.RuntimePath = $RuntimePath }

try {
    & (Join-Path $PSScriptRoot 'scripts\github\build-registry.ps1') @invoke
} catch {
    $failure = $_
    if (-not $KeepFailedArtifacts) {
        try {
            & (Join-Path $PSScriptRoot 'Remove-Failed-Deployment.ps1') -Root $root -BuildArtifactsOnly
        } catch {
            Write-Warning "Automatic cleanup also failed: $($_.Exception.Message)"
        }
    } else {
        Write-Warning 'Failed build artifacts were preserved because -KeepFailedArtifacts was specified.'
    }
    throw $failure
}
