[CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'Medium')]
param(
    [string]$Root = $PSScriptRoot,
    [ValidatePattern('^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$')]
    [string]$Repository = 'MuRongMoQing/phylang-registry',
    [switch]$BuildArtifactsOnly,
    [switch]$RestoreTrackedFiles,
    [switch]$RemoveLocalGitRepository,
    [string]$ConfirmLocalPath,
    [switch]$RemoveRemoteRepository,
    [string]$ConfirmRepository
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$Root = (Resolve-Path -LiteralPath $Root).Path

function Assert-ChildPath {
    param([Parameter(Mandatory = $true)][string]$Path)
    $full = [IO.Path]::GetFullPath($Path)
    $prefix = $Root.TrimEnd('\','/') + [IO.Path]::DirectorySeparatorChar
    if (-not $full.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to remove a path outside the deployment root: $full"
    }
    return $full
}

function Remove-SafeItem {
    param([Parameter(Mandatory = $true)][string]$Path)
    $full = Assert-ChildPath -Path $Path
    if (Test-Path -LiteralPath $full) {
        if ($PSCmdlet.ShouldProcess($full, 'Remove failed deployment artifact')) {
            Remove-Item -LiteralPath $full -Recurse -Force
            Write-Host "[REMOVED] $full"
        }
    }
}

$buildPath = Join-Path $Root 'build'
Remove-SafeItem -Path $buildPath

if (-not $BuildArtifactsOnly) {
    Remove-SafeItem -Path (Join-Path $Root 'deployment-report.json')

    $backupRoot = Join-Path $Root '.phylang-deployment-backup'
    if (Test-Path -LiteralPath $backupRoot -PathType Container) {
        $configBackup = Join-Path $backupRoot 'registry-hosting.json'
        $codeownersBackup = Join-Path $backupRoot 'CODEOWNERS'
        if (Test-Path -LiteralPath $configBackup -PathType Leaf) {
            Copy-Item -LiteralPath $configBackup -Destination (Join-Path $Root 'registry-hosting.json') -Force
            Write-Host '[RESTORED] registry-hosting.json'
        }
        if (Test-Path -LiteralPath $codeownersBackup -PathType Leaf) {
            Copy-Item -LiteralPath $codeownersBackup -Destination (Join-Path $Root '.github\CODEOWNERS') -Force
            Write-Host '[RESTORED] .github\CODEOWNERS'
        }
        Remove-SafeItem -Path $backupRoot
    }

    if ($RestoreTrackedFiles) {
        if (-not (Test-Path -LiteralPath (Join-Path $Root '.git') -PathType Container)) {
            throw '-RestoreTrackedFiles requires an existing local Git repository.'
        }
        Push-Location -LiteralPath $Root
        try {
            & git restore -- registry-hosting.json .github/CODEOWNERS ':(glob)**/phylang.lock'
            if ($LASTEXITCODE -ne 0) { throw 'git restore failed.' }
            Write-Host '[RESTORED] tracked configuration and lock files'
        } finally {
            Pop-Location
        }
    }

    if ($RemoveLocalGitRepository) {
        if (-not $ConfirmLocalPath) {
            throw "To remove .git, pass -ConfirmLocalPath with the exact deployment root: $Root"
        }
        $expected = [IO.Path]::GetFullPath($ConfirmLocalPath)
        if (-not $expected.Equals($Root, [StringComparison]::OrdinalIgnoreCase)) {
            throw "To remove .git, pass -ConfirmLocalPath with the exact deployment root: $Root"
        }
        Remove-SafeItem -Path (Join-Path $Root '.git')
    }

    if ($RemoveRemoteRepository) {
        if ($ConfirmRepository -cne $Repository) {
            throw "To remove the remote repository, pass -ConfirmRepository '$Repository'."
        }
        if (-not (Get-Command gh -ErrorAction SilentlyContinue)) {
            throw 'GitHub CLI was not found. Install gh and authenticate before remote removal.'
        }
        if ($PSCmdlet.ShouldProcess($Repository, 'Delete GitHub repository permanently')) {
            & gh repo delete $Repository --yes
            if ($LASTEXITCODE -ne 0) {
                throw "Remote deletion failed. The token may need delete_repo scope. Run: gh auth refresh -h github.com -s delete_repo"
            }
            Write-Host "[REMOVED] https://github.com/$Repository"
        }
    }
}

Write-Host '[PASS] Failed deployment cleanup completed.'
