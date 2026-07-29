[CmdletBinding()]
param(
    [ValidatePattern('^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$')]
    [string]$Repository = 'MuRongMoQing/phylang-registry',
    [string]$Commit,
    [switch]$SkipDispatch,
    [switch]$SkipHealthCheck
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$ExpectedOwner = 'MuRongMoQing'
$ApiVersion = '2026-03-10'

function Write-Step {
    param([string]$Message)
    Write-Host "`n=== $Message ===" -ForegroundColor Cyan
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

function Configure-Pages {
    $get = Invoke-Native -FilePath gh -Arguments @(
        'api', "repos/$Repository/pages",
        '-H', "X-GitHub-Api-Version: $ApiVersion"
    ) -AllowedExitCodes @(0,1) -Capture
    if ($get.ExitCode -eq 0) {
        Invoke-Native -FilePath gh -Arguments @(
            'api','--method','PUT',"repos/$Repository/pages",
            '-H',"X-GitHub-Api-Version: $ApiVersion",
            '-f','build_type=workflow'
        ) | Out-Null
        return
    }
    $text = ($get.Output | Out-String)
    if ($text -notmatch '404|Not Found') {
        throw "Unable to read Pages settings:`n$text"
    }
    Invoke-Native -FilePath gh -Arguments @(
        'api','--method','POST',"repos/$Repository/pages",
        '-H',"X-GitHub-Api-Version: $ApiVersion",
        '-f','build_type=workflow'
    ) | Out-Null
}

function Get-WorkflowFailureDetails {
    param([string]$RunId)
    $summary = Invoke-Native -FilePath gh -Arguments @(
        'run','view',$RunId,
        '--repo',$Repository,
        '--json','databaseId,workflowName,event,status,conclusion,url,jobs'
    ) -AllowedExitCodes @(0,1) -Capture
    $logs = Invoke-Native -FilePath gh -Arguments @(
        'run','view',$RunId,
        '--repo',$Repository,
        '--log-failed'
    ) -AllowedExitCodes @(0,1) -Capture
    $summaryText = ($summary.Output -join "`n").Trim()
    $logLines = @($logs.Output)
    if ($logLines.Count -gt 200) {
        $logLines = $logLines[($logLines.Count - 200)..($logLines.Count - 1)]
    }
    return ($summaryText + "`n--- failed log tail ---`n" + ($logLines -join "`n")).Trim()
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
                    $statusText = if ($null -ne $run.PSObject.Properties['status']) { [string]$run.status } else { 'unknown' }
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
    $watch = Invoke-Native -FilePath gh -Arguments @(
        'run','watch',$runId,
        '--repo',$Repository,
        '--exit-status',
        '--interval','5'
    ) -AllowedExitCodes @(0,1) -Capture
    if ($watch.ExitCode -ne 0) {
        $details = Get-WorkflowFailureDetails -RunId $runId
        throw "GitHub Actions workflow failed: $Workflow (run $runId).`n$details"
    }
    Write-Host "[PASS] $Workflow run $runId completed successfully." -ForegroundColor Green
}

Write-Step 'Validate authentication and repository'
if (-not (Get-Command gh.exe -ErrorAction SilentlyContinue)) {
    throw 'GitHub CLI gh.exe is required.'
}
$login = ((Invoke-Native -FilePath gh -Arguments @('api','user','--jq','.login') -Capture).Output -join '').Trim()
if ($login -ine $ExpectedOwner) {
    throw "GitHub CLI is logged in as '$login'; expected '$ExpectedOwner'."
}
Invoke-Native -FilePath gh -Arguments @(
    'repo','view',$Repository,
    '--json','nameWithOwner,url,defaultBranchRef'
) | Out-Null
if (-not $Commit) {
    $Commit = ((Invoke-Native -FilePath gh -Arguments @(
        'api',"repos/$Repository/commits/main",'--jq','.sha'
    ) -Capture).Output -join '').Trim()
}
if ($Commit -notmatch '^[0-9a-fA-F]{40}$') {
    throw "Invalid main commit SHA: $Commit"
}
Write-Host "Repository: $Repository"
Write-Host "Commit:     $Commit"

# Validation is a hard gate. Do not dispatch or verify Pages for a commit that
# has not passed both Linux and Windows validation.
Write-Step 'Wait for Linux and Windows validation'
Wait-Workflow -Workflow 'validate.yml' -HeadSha $Commit -Event 'push' -TimeoutMinutes 25

Write-Step 'Ensure GitHub Pages uses Actions'
Configure-Pages

if (-not $SkipDispatch) {
    Write-Step 'Dispatch validated Pages deployment'
    $dispatchDeadline = (Get-Date).AddMinutes(5)
    $dispatched = $false
    while ((Get-Date) -lt $dispatchDeadline) {
        $result = Invoke-Native -FilePath gh -Arguments @(
            'workflow','run','deploy.yml',
            '--repo',$Repository,
            '--ref','main'
        ) -AllowedExitCodes @(0,1) -Capture
        if ($result.ExitCode -eq 0) {
            $dispatched = $true
            break
        }
        Start-Sleep -Seconds 5
    }
    if (-not $dispatched) {
        throw 'GitHub did not accept deploy.yml workflow_dispatch.'
    }
}

Write-Step 'Wait for Pages deployment'
Wait-Workflow -Workflow 'deploy.yml' -HeadSha $Commit -Event 'workflow_dispatch' -TimeoutMinutes 25

if (-not $SkipHealthCheck) {
    Write-Step 'Verify published health endpoint'
    $parts = $Repository.Split('/',2)
    $healthUrl = "https://$($parts[0]).github.io/$($parts[1])/health.json"
    $healthDeadline = (Get-Date).AddMinutes(10)
    $health = $null
    while ((Get-Date) -lt $healthDeadline) {
        try {
            $health = Invoke-RestMethod -Uri $healthUrl -TimeoutSec 20
            if ($health.ok -eq $true) { break }
        } catch {}
        Start-Sleep -Seconds 10
    }
    if (-not $health -or $health.ok -ne $true) {
        throw "Pages health verification failed: $healthUrl"
    }
    Write-Host "Health: $healthUrl"
}

$report = [ordered]@{
    version = '0.6.2'
    deployment_package_revision = 'R7-resume'
    repository = $Repository
    commit = $Commit
    repository_url = "https://github.com/$Repository"
    actions_url = "https://github.com/$Repository/actions"
    pages_url = "https://$($Repository.Split('/')[0]).github.io/$($Repository.Split('/')[1])/"
    completed_utc = [DateTime]::UtcNow.ToString('o')
}
$reportPath = Join-Path $PSScriptRoot 'deployment-report.json'
[IO.File]::WriteAllText(
    $reportPath,
    (($report | ConvertTo-Json) + "`n"),
    (New-Object Text.UTF8Encoding($false))
)
Write-Host "`n[PASS] Existing deployment completed and verified." -ForegroundColor Green
Write-Host "Repository: $($report.repository_url)"
Write-Host "Actions:    $($report.actions_url)"
Write-Host "Pages:      $($report.pages_url)"
