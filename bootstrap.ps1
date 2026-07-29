[CmdletBinding()]
param(
    [string]$Repository = 'MuRongMoQing/phylang-registry',
    [switch]$Private,
    [switch]$UpdateExisting,
    [switch]$SkipGitHubSettings
)
$visibility = if ($Private) { 'private' } else { 'public' }
& (Join-Path $PSScriptRoot 'Deploy-To-GitHub-Windows10.ps1') -Repository $Repository -Visibility $visibility -UpdateExisting:$UpdateExisting -SkipWait:$SkipGitHubSettings
