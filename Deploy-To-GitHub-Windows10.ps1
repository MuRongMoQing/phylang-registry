[CmdletBinding()]
param(
    [ValidatePattern('^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$')]
    [string]$Repository = 'MuRongMoQing/phylang-registry',
    [ValidateSet('public','private')]
    [string]$Visibility = 'public',
    [switch]$UpdateExisting,
    [switch]$SkipLocalBuild,
    [switch]$SkipWait,
    [switch]$SkipToolInstall
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$ExpectedOwner = 'MuRongMoQing'
$ApiVersion = '2026-03-10'
$Utf8Strict = New-Object System.Text.UTF8Encoding -ArgumentList @($false, $true)
$remoteCreatedByThisRun = $false
$localGitCreatedByThisRun = $false
$pushCompleted = $false

function Write-Step {
    param([string]$Message)
    Write-Host "`n=== $Message ===" -ForegroundColor Cyan
}

function Refresh-Path {
    $machine = [Environment]::GetEnvironmentVariable('Path','Machine')
    $user = [Environment]::GetEnvironmentVariable('Path','User')
    $env:Path = ($machine,$user -join ';')
}

function Invoke-Native {
    param(
        [string]$FilePath,
        [string[]]$Arguments = @(),
        [int[]]$AllowedExitCodes = @(0),
        [switch]$Capture
    )
    $old = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        if ($Capture) {
            $output = @(& $FilePath @Arguments 2>&1)
            $code = $LASTEXITCODE
        } else {
            & $FilePath @Arguments
            $code = $LASTEXITCODE
            $output = @()
        }
    } finally {
        $ErrorActionPreference = $old
    }
    if ($AllowedExitCodes -notcontains $code) {
        $message = ($output | Out-String).Trim()
        throw "Native command failed ($code): $FilePath $($Arguments -join ' ')`n$message"
    }
    return [PSCustomObject]@{ ExitCode = $code; Output = $output }
}

function Ensure-Tool {
    param([string]$Command,[string]$WingetId,[string]$DisplayName)
    if (Get-Command $Command -ErrorAction SilentlyContinue) { return }
    if ($SkipToolInstall) { throw "$DisplayName was not found: $Command" }
    if (-not (Get-Command winget.exe -ErrorAction SilentlyContinue)) {
        throw "$DisplayName was not found and winget is unavailable. Install it, reopen PowerShell, then rerun."
    }
    Write-Step "Install $DisplayName"
    Invoke-Native -FilePath winget.exe -Arguments @('install','--id',$WingetId,'--exact','--source','winget','--accept-source-agreements','--accept-package-agreements','--silent') | Out-Null
    Refresh-Path
    if (-not (Get-Command $Command -ErrorAction SilentlyContinue)) {
        throw "$DisplayName installation completed but $Command is not visible. Restart Windows and rerun."
    }
}

function Test-RemoteRepository {
    param([string]$Name)
    $result = Invoke-Native -FilePath gh -Arguments @('repo','view',$Name,'--json','nameWithOwner') -AllowedExitCodes @(0,1) -Capture
    if ($result.ExitCode -eq 0) { return $true }
    $text = ($result.Output | Out-String)
    if ($text -match 'Could not resolve|not found|HTTP 404|Could not resolve to a Repository') { return $false }
    throw "Unable to query repository:`n$text"
}

function Configure-Pages {
    param([switch]$AllowDeferred)
    $get = Invoke-Native -FilePath gh -Arguments @('api',"repos/$Repository/pages",'-H',"X-GitHub-Api-Version: $ApiVersion") -AllowedExitCodes @(0,1) -Capture
    if ($get.ExitCode -eq 0) {
        Invoke-Native -FilePath gh -Arguments @('api','--method','PUT',"repos/$Repository/pages",'-H',"X-GitHub-Api-Version: $ApiVersion",'-f','build_type=workflow') | Out-Null
        return $true
    }
    $text = ($get.Output | Out-String)
    if ($text -notmatch '404|Not Found') {
        if ($AllowDeferred) {
            Write-Warning "Pages configuration was deferred until after the first push: $text"
            return $false
        }
        throw "Unable to read Pages settings:`n$text"
    }
    $create = Invoke-Native -FilePath gh -Arguments @('api','--method','POST',"repos/$Repository/pages",'-H',"X-GitHub-Api-Version: $ApiVersion",'-f','build_type=workflow') -AllowedExitCodes @(0,1) -Capture
    if ($create.ExitCode -eq 0) { return $true }
    $createText = ($create.Output | Out-String)
    if ($AllowDeferred -and $createText -match '404|409|422|Not Found|Unprocessable') {
        Write-Warning 'Pages cannot be created before the repository has its first pushed branch. It will be configured immediately after push.'
        return $false
    }
    throw "Unable to create Pages site:`n$createText"
}
function Wait-Workflow {
    param(
        [string]$Workflow,
        [string]$HeadSha,
        [string]$Event,
        [int]$TimeoutMinutes = 25
    )
    $deadline = (Get-Date).AddMinutes($TimeoutMinutes)
    $runId = $null
    $lastListing = ''
    while ((Get-Date) -lt $deadline) {
        # Query the official workflow-runs REST endpoint and filter server-side by
        # head_sha. This avoids relying on gh run list objects having a headSha
        # property while a new repository is still indexing its workflows.
        $arguments = @(
            'api','--method','GET',
            "repos/$Repository/actions/workflows/$Workflow/runs",
            '-H',"X-GitHub-Api-Version: $ApiVersion",
            '-f','branch=main',
            '-f',"head_sha=$HeadSha",
            '-f','per_page=20'
        )
        if ($Event) { $arguments += @('-f',"event=$Event") }
        $listed = Invoke-Native -FilePath gh -Arguments $arguments -AllowedExitCodes @(0,1) -Capture
        $lastListing = ($listed.Output -join "`n")
        if ($listed.ExitCode -eq 0 -and $lastListing.Trim()) {
            try {
                $response = $lastListing | ConvertFrom-Json
                $runs = @()
                if ($null -ne $response -and $null -ne $response.PSObject.Properties['workflow_runs']) {
                    $runs = @($response.workflow_runs)
                }
                $candidates = @($runs | Where-Object {
                    $null -ne $_ -and
                    $null -ne $_.PSObject.Properties['id'] -and
                    (-not $Event -or (
                        $null -ne $_.PSObject.Properties['event'] -and
                        [string]$_.event -eq $Event
                    ))
                })
                $run = $candidates | Sort-Object created_at -Descending | Select-Object -First 1
                if ($null -ne $run) {
                    $runId = [string]$run.id
                    $statusText = 'unknown'
                    if ($null -ne $run.PSObject.Properties['status']) {
                        $statusText = [string]$run.status
                    }
                    Write-Host "Found workflow run $runId ($Workflow, event=$Event, status=$statusText)."
                    break
                }
            } catch {
                Write-Warning "Workflow list is not ready; retrying: $($_.Exception.Message)"
            }
        }
        Start-Sleep -Seconds 5
    }
    if (-not $runId) {
        throw "Workflow did not start within timeout: $Workflow (commit=$HeadSha, event=$Event). Last gh output:`n$lastListing"
    }
    Invoke-Native -FilePath gh -Arguments @(
        'run','watch',$runId,
        '--repo',$Repository,
        '--exit-status',
        '--interval','5'
    ) | Out-Null
}

try {
Write-Step 'Preflight'
if ($PSVersionTable.PSVersion.Major -lt 5) { throw 'Windows PowerShell 5.1 or newer is required.' }
if ($env:OS -ne 'Windows_NT') { throw 'This one-click script must run on Windows 10/11.' }
$root = (Resolve-Path -LiteralPath $PSScriptRoot).Path
Set-Location -LiteralPath $root
$parts = $Repository.Split('/',2)
$owner = $parts[0]
$repoName = $parts[1]
if ($owner -ine $ExpectedOwner) { throw "Repository owner must be $ExpectedOwner." }
Ensure-Tool -Command git.exe -WingetId 'Git.Git' -DisplayName 'Git for Windows'
Ensure-Tool -Command gh.exe -WingetId 'GitHub.cli' -DisplayName 'GitHub CLI'

Write-Step 'GitHub authentication'
$auth = Invoke-Native -FilePath gh -Arguments @('auth','status','--hostname','github.com') -AllowedExitCodes @(0,1) -Capture
if ($auth.ExitCode -ne 0) {
    Invoke-Native -FilePath gh -Arguments @('auth','login','--hostname','github.com','--git-protocol','https','--web') | Out-Null
}
Invoke-Native -FilePath gh -Arguments @('auth','status','--hostname','github.com') | Out-Null
$login = ((Invoke-Native -FilePath gh -Arguments @('api','user','--jq','.login') -Capture).Output -join '').Trim()
if ($login -ine $ExpectedOwner) {
    throw "GitHub CLI is logged in as '$login', but this package requires '$ExpectedOwner'. Run: gh auth switch -u $ExpectedOwner"
}

Write-Step 'Validate deployment package'
& (Join-Path $root 'Test-Deployment-Package.ps1') -Root $root -RunRuntimeSelfTest

$configPath = Join-Path $root 'registry-hosting.json'
$backupRoot = Join-Path $root '.phylang-deployment-backup'
# The backup directory is intentionally ignored by .gitignore so it can never be
# included by the initial git add. It remains inside the package root only so the
# existing rollback helper can restore configuration after a pre-push failure.
Remove-Item -LiteralPath $backupRoot -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path $backupRoot -Force | Out-Null
Copy-Item -LiteralPath $configPath -Destination (Join-Path $backupRoot 'registry-hosting.json') -Force
$configText = [IO.File]::ReadAllText($configPath, $Utf8Strict)
$config = $configText | ConvertFrom-Json
$config.repository = $Repository
$config.pages_base = "https://$owner.github.io/$repoName/"
$config.release_base = "https://github.com/$Repository/releases/download/"
$config.description = 'PhyLang 0.6.2 community registry'
[IO.File]::WriteAllText($configPath,(($config | ConvertTo-Json -Depth 10) + "`n"),(New-Object Text.UTF8Encoding($false)))
$codeowners = Join-Path $root '.github\CODEOWNERS'
if (Test-Path -LiteralPath $codeowners) {
    Copy-Item -LiteralPath $codeowners -Destination (Join-Path $backupRoot 'CODEOWNERS') -Force
    $text = [IO.File]::ReadAllText($codeowners, $Utf8Strict)
    $text = $text.Replace('@MAINTAINERS',"@$owner").Replace('@PACKAGE-REVIEWERS',"@$owner")
    [IO.File]::WriteAllText($codeowners,$text,(New-Object Text.UTF8Encoding($false)))
}

if (-not $SkipLocalBuild) {
    Write-Step 'Local Windows registry build'
    & (Join-Path $root 'Build-Registry-Windows.ps1') -OutDirectory 'build\pages-local' -Repository $Repository -PagesUrl $config.pages_base
}

Write-Step 'Initialize Git repository'
if (-not (Test-Path -LiteralPath (Join-Path $root '.git'))) {
    Invoke-Native -FilePath git -Arguments @('init') | Out-Null
    $localGitCreatedByThisRun = $true
}
# Pin repository-local EOL behavior so global Git for Windows settings such as
# core.eol=crlf or core.autocrlf=true cannot make the first staging operation fail.
Invoke-Native -FilePath git -Arguments @('config','core.autocrlf','false') | Out-Null
Invoke-Native -FilePath git -Arguments @('config','core.eol','lf') | Out-Null
Invoke-Native -FilePath git -Arguments @('config','core.safecrlf','true') | Out-Null
$userName = Invoke-Native -FilePath git -Arguments @('config','user.name') -AllowedExitCodes @(0,1) -Capture
if ($userName.ExitCode -ne 0 -or -not (($userName.Output | Out-String).Trim())) {
    Invoke-Native -FilePath git -Arguments @('config','user.name',$ExpectedOwner) | Out-Null
}
$userEmail = Invoke-Native -FilePath git -Arguments @('config','user.email') -AllowedExitCodes @(0,1) -Capture
if ($userEmail.ExitCode -ne 0 -or -not (($userEmail.Output | Out-String).Trim())) {
    Invoke-Native -FilePath git -Arguments @('config','user.email',"$ExpectedOwner@users.noreply.github.com") | Out-Null
}
Invoke-Native -FilePath git -Arguments @('checkout','-B','main') | Out-Null
# One-shot overrides isolate initial staging from system/global Git EOL policy.
# .gitattributes remains the source of truth for committed line endings.
Invoke-Native -FilePath git -Arguments @(
    '-c','core.autocrlf=false',
    '-c','core.eol=lf',
    '-c','core.safecrlf=false',
    'add','--all'
) | Out-Null
foreach ($script in @('bootstrap.sh','scripts/github/build-registry.sh','scripts/github/validate-package-pr.sh','scripts/github/simulate-actions.sh','scripts/github/simulate-windows-git-eol.sh')) {
    if (Test-Path -LiteralPath (Join-Path $root $script)) {
        Invoke-Native -FilePath git -Arguments @('update-index','--chmod=+x','--',$script) | Out-Null
    }
}
$staged = Invoke-Native -FilePath git -Arguments @('diff','--cached','--quiet') -AllowedExitCodes @(0,1) -Capture
if ($staged.ExitCode -eq 1) { Invoke-Native -FilePath git -Arguments @('commit','-m','Deploy PhyLang Registry 0.6.2-R8') | Out-Null }
$head = ((Invoke-Native -FilePath git -Arguments @('rev-parse','HEAD') -Capture).Output -join '').Trim()

Write-Step 'Create or select GitHub repository'
$exists = Test-RemoteRepository -Name $Repository
$visibilityArg = if ($Visibility -eq 'private') { '--private' } else { '--public' }
if (-not $exists) {
    Invoke-Native -FilePath gh -Arguments @('repo','create',$Repository,$visibilityArg,'--source=.','--remote=origin') | Out-Null
    $remoteCreatedByThisRun = $true
} else {
    if (-not $UpdateExisting) { throw 'Remote repository already exists. Rerun with -UpdateExisting to push without force.' }
    $url = "https://github.com/$Repository.git"
    $origin = Invoke-Native -FilePath git -Arguments @('remote','get-url','origin') -AllowedExitCodes @(0,1) -Capture
    if ($origin.ExitCode -eq 0) { Invoke-Native -FilePath git -Arguments @('remote','set-url','origin',$url) | Out-Null }
    else { Invoke-Native -FilePath git -Arguments @('remote','add','origin',$url) | Out-Null }
}

Write-Step 'Configure GitHub Actions permissions'
Invoke-Native -FilePath gh -Arguments @('api','--method','PUT',"repos/$Repository/actions/permissions/workflow",'-H',"X-GitHub-Api-Version: $ApiVersion",'-f','default_workflow_permissions=read','-F','can_approve_pull_request_reviews=false') | Out-Null

# Pages deployment is intentionally gated by validate.yml. The resume helper
# configures and dispatches Pages only after Linux and Windows validation pass.
Write-Step 'Push main branch'
Invoke-Native -FilePath git -Arguments @('push','--set-upstream','origin','main') | Out-Null
$pushCompleted = $true

if (-not $SkipWait) {
    Write-Step 'Wait for validation, then deploy validated commit to Pages'
    & (Join-Path $root 'Resume-GitHub-Deployment-Windows10.ps1') -Repository $Repository -Commit $head
} else {
    Write-Warning "main was pushed but Pages was not dispatched because -SkipWait was used. Run: .\Resume-GitHub-Deployment-Windows10.ps1 -Repository '$Repository' -Commit '$head'"
}

$report = [ordered]@{
    version = '0.6.2'
    deployment_package_revision = 'R8'
    repository = $Repository
    commit = $head
    repository_url = "https://github.com/$Repository"
    actions_url = "https://github.com/$Repository/actions"
    pages_url = "https://$owner.github.io/$repoName/"
    completed_utc = [DateTime]::UtcNow.ToString('o')
}
[IO.File]::WriteAllText((Join-Path $root 'deployment-report.json'),(($report | ConvertTo-Json) + "`n"),(New-Object Text.UTF8Encoding($false)))
Remove-Item -LiteralPath $backupRoot -Recurse -Force -ErrorAction SilentlyContinue
Write-Host "`n[PASS] PhyLang registry deployment completed." -ForegroundColor Green
Write-Host "Repository: $($report.repository_url)"
Write-Host "Actions:    $($report.actions_url)"
Write-Host "Pages:      $($report.pages_url)"
} catch {
    $failure = $_
    try {
        if (Get-Variable root -ErrorAction SilentlyContinue) {
            $cleanupScript = Join-Path $root 'Remove-Failed-Deployment.ps1'
            if (Test-Path -LiteralPath $cleanupScript -PathType Leaf) {
                if ($pushCompleted) {
                    # After a successful push, keep the configured tracked files in
                    # sync with the remote branch. Only generated build output and the
                    # temporary backup directory are removed.
                    & $cleanupScript -Root $root -BuildArtifactsOnly
                    Remove-Item -LiteralPath (Join-Path $root '.phylang-deployment-backup') -Recurse -Force -ErrorAction SilentlyContinue
                    Write-Host '[PRESERVED] Local Git repository and deployed configuration because main was already pushed.' -ForegroundColor Yellow
                } elseif ($localGitCreatedByThisRun -and -not $remoteCreatedByThisRun) {
                    & $cleanupScript -Root $root -RemoveLocalGitRepository -ConfirmLocalPath $root
                } else {
                    & $cleanupScript -Root $root
                }
            }
        }
    } catch {
        Write-Warning "Automatic local rollback failed: $($_.Exception.Message)"
    }
    if ($remoteCreatedByThisRun) {
        Write-Warning "This run created the remote repository. It was not deleted automatically. To remove it explicitly, run: .\Remove-Failed-Deployment.ps1 -Root `$PWD -Repository '$Repository' -RemoveRemoteRepository -ConfirmRepository '$Repository'"
    }
    throw $failure
}
