[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$TargetRoot,
    [ValidatePattern('^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$')]
    [string]$Repository = 'MuRongMoQing/phylang-registry',
    [ValidateRange(1,10)]
    [int]$PushAttempts = 5,
    [switch]$SkipHealthCheck
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$ExpectedOwner = 'MuRongMoQing'
$SourceRoot = (Resolve-Path -LiteralPath $PSScriptRoot).Path
$TargetRoot = (Resolve-Path -LiteralPath $TargetRoot).Path
$BackupRoot = Join-Path $TargetRoot '.phylang-r9-repair-backup'
$DiagnosticRoot = Join-Path ([IO.Path]::GetTempPath()) ('phylang-r9-network-diagnostics-' + [Guid]::NewGuid().ToString('N'))
$CommitPushed = $false
$CommitCreated = $false
$OriginalHead = $null
$NewCommit = $null
$NewFiles = @()
$GitTransportOverrides = @()

function Write-Step { param([string]$Message) Write-Host "`n=== $Message ===" -ForegroundColor Cyan }
function Invoke-Native {
    param([string]$FilePath,[string[]]$Arguments=@(),[int[]]$AllowedExitCodes=@(0),[switch]$Capture)
    $old = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        if ($Capture) { $output=@(& $FilePath @Arguments 2>&1); $code=$LASTEXITCODE }
        else { & $FilePath @Arguments; $code=$LASTEXITCODE; $output=@() }
    } finally { $ErrorActionPreference=$old }
    if ($AllowedExitCodes -notcontains $code) {
        throw "Native command failed ($code): $FilePath $($Arguments -join ' ')`n$(($output | Out-String).Trim())"
    }
    [PSCustomObject]@{ExitCode=$code;Output=$output}
}
function Invoke-NativeResult {
    param([string]$FilePath,[string[]]$Arguments=@())
    $old = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        $output=@(& $FilePath @Arguments 2>&1)
        $code=$LASTEXITCODE
    } finally { $ErrorActionPreference=$old }
    [PSCustomObject]@{ExitCode=$code;Output=$output;Text=(($output | Out-String).Trim())}
}
function Test-RetryableTransportFailure {
    param([string]$Text)
    return $Text -match '(?i)(TLS connect error|unexpected eof|connection reset|recv failure|failed to connect|could not resolve host|timed out|operation was too slow|HTTP (500|502|503|504)|remote end hung up unexpectedly)'
}
function Save-NetworkDiagnostic {
    param([string]$Name,[object]$Result)
    New-Item -ItemType Directory -Force -Path $DiagnosticRoot | Out-Null
    $path = Join-Path $DiagnosticRoot $Name
    $safe = [string]$Result.Text
    $safe = $safe -replace '(?i)(authorization:\s*basic\s+)[^\r\n]+','$1<redacted>'
    $safe = $safe -replace '(?i)(https?://)[^/@\s:]+:[^/@\s]+@','$1<redacted>@'
    [IO.File]::WriteAllText($path, $safe + "`r`n", (New-Object Text.UTF8Encoding($true)))
    return $path
}
function Test-GitTransportMode {
    param([string]$Label,[string[]]$Overrides)
    $args = @($Overrides + @('ls-remote','origin','refs/heads/main'))
    $result = Invoke-NativeResult git $args
    $log = Save-NetworkDiagnostic ("ls-remote-{0}.log" -f $Label) $result
    if ($result.ExitCode -eq 0) {
        Write-Host "[PASS] Git HTTPS transport mode '$Label' can read origin/main."
        return [PSCustomObject]@{Success=$true;Overrides=$Overrides;Log=$log}
    }
    Write-Warning "Git transport mode '$Label' failed. Diagnostic: $log"
    return [PSCustomObject]@{Success=$false;Overrides=$Overrides;Log=$log;Text=$result.Text}
}
function Select-GitTransportMode {
    Write-Step 'Select verified Git HTTPS transport mode'
    Remove-Item -LiteralPath $DiagnosticRoot -Recurse -Force -ErrorAction SilentlyContinue
    $modes = @(
        [PSCustomObject]@{Label='default';Overrides=@()},
        [PSCustomObject]@{Label='http11';Overrides=@('-c','http.version=HTTP/1.1')},
        [PSCustomObject]@{Label='schannel-http11';Overrides=@('-c','http.sslBackend=schannel','-c','http.version=HTTP/1.1')}
    )
    foreach ($mode in $modes) {
        $test = Test-GitTransportMode -Label $mode.Label -Overrides $mode.Overrides
        if ($test.Success) { return @($mode.Overrides) }
    }
    throw "All read-only Git HTTPS transport checks failed. No repository files were modified. Review logs in $DiagnosticRoot."
}
function Push-WithRetry {
    param([string[]]$Overrides,[int]$Attempts)
    $delays = @(0,2,5,10,20,30,45,60,90,120)
    for ($attempt=1; $attempt -le $Attempts; $attempt++) {
        if ($attempt -gt 1) { Start-Sleep -Seconds $delays[[Math]::Min($attempt-1,$delays.Count-1)] }
        Write-Host "Git push attempt $attempt of $Attempts..."
        $args = @($Overrides + @('push','origin','main'))
        $result = Invoke-NativeResult git $args
        $log = Save-NetworkDiagnostic ("push-attempt-{0}.log" -f $attempt) $result
        if ($result.ExitCode -eq 0) {
            Write-Host "[PASS] Repair commit pushed on attempt $attempt."
            return
        }
        if (-not (Test-RetryableTransportFailure $result.Text)) {
            throw "Git push failed with a non-retryable error. Diagnostic: $log`n$($result.Text)"
        }
        Write-Warning "Retryable Git transport failure on attempt $attempt. Diagnostic: $log"
    }
    throw "Git push failed after $Attempts retryable transport attempts. The local repair commit was preserved. Diagnostics: $DiagnosticRoot"
}
function Copy-PayloadFile {
    param([string]$RelativePath)
    $source = Join-Path $SourceRoot $RelativePath
    $target = Join-Path $TargetRoot $RelativePath
    if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { throw "R9 payload file missing: $RelativePath" }
    $backup = Join-Path $BackupRoot $RelativePath
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $backup),(Split-Path -Parent $target) | Out-Null
    if (Test-Path -LiteralPath $target -PathType Leaf) {
        Copy-Item -LiteralPath $target -Destination $backup -Force
    } else {
        $script:NewFiles += $target
    }
    Copy-Item -LiteralPath $source -Destination $target -Force
}
function Restore-Backup {
    if (-not (Test-Path -LiteralPath $BackupRoot -PathType Container)) { return }
    Get-ChildItem -LiteralPath $BackupRoot -File -Recurse | ForEach-Object {
        $relative = $_.FullName.Substring($BackupRoot.Length).TrimStart('\','/')
        $target = Join-Path $TargetRoot $relative
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $target) | Out-Null
        Copy-Item -LiteralPath $_.FullName -Destination $target -Force
    }
    foreach ($newFile in $script:NewFiles) {
        if (Test-Path -LiteralPath $newFile -PathType Leaf) { Remove-Item -LiteralPath $newFile -Force }
    }
}

try {
    Write-Step 'Validate target repository'
    if ($SourceRoot -eq $TargetRoot) { throw 'TargetRoot must be the existing R5/R6/R7/R8 Git clone, not the extracted R9 source directory.' }
    if (-not (Test-Path -LiteralPath (Join-Path $TargetRoot '.git') -PathType Container)) { throw "Target is not a Git repository: $TargetRoot" }
    if (-not (Get-Command git.exe -ErrorAction SilentlyContinue)) { throw 'Git for Windows is required.' }
    if (-not (Get-Command gh.exe -ErrorAction SilentlyContinue)) { throw 'GitHub CLI is required.' }
    Set-Location -LiteralPath $TargetRoot
    $login = ((Invoke-Native gh @('api','user','--jq','.login') -Capture).Output -join '').Trim()
    if ($login -ine $ExpectedOwner) { throw "GitHub CLI is logged in as '$login'; expected '$ExpectedOwner'." }
    Invoke-Native gh @('auth','setup-git','--hostname','github.com') | Out-Null
    $remote = ((Invoke-Native git @('remote','get-url','origin') -Capture).Output -join '').Trim()
    if ($remote -notmatch 'MuRongMoQing/phylang-registry(?:\.git)?$') { throw "Unexpected origin URL: $remote" }

    # Select a transport using read-only ls-remote before fetch. This ensures a
    # broken default OpenSSL path cannot abort the script before diagnostics.
    $GitTransportOverrides = Select-GitTransportMode
    Invoke-Native git @($GitTransportOverrides + @('fetch','origin','main')) | Out-Null

    $localHead = ((Invoke-Native git @('rev-parse','HEAD') -Capture).Output -join '').Trim()
    $OriginalHead = $localHead
    $remoteHead = ((Invoke-Native git @('rev-parse','origin/main') -Capture).Output -join '').Trim()
    if ($localHead -ne $remoteHead) { throw "Local HEAD ($localHead) is not origin/main ($remoteHead). Synchronize before repair." }

    $dirty = @((Invoke-Native git @('status','--porcelain=v1','--untracked-files=all') -Capture).Output)
    $allowedOrdinaryPaths = @('registry-hosting.json','.github/CODEOWNERS','deployment-report.json')
    $knownBackupResidue = @('.phylang-deployment-backup/CODEOWNERS','.phylang-deployment-backup/registry-hosting.json')
    $unexpected = @()
    foreach ($rawLine in $dirty) {
        $line = [string]$rawLine
        if ($line.Length -lt 4) { $unexpected += $line; continue }
        $status = $line.Substring(0,2)
        $path = $line.Substring(3).Trim('"') -replace '\\','/'
        if ($allowedOrdinaryPaths -contains $path) { continue }
        if ($knownBackupResidue -contains $path -and $status -in @(' D','D ')) { continue }
        $unexpected += $line
    }
    if ($unexpected.Count -gt 0) { throw "Unexpected local changes; repair stopped without modifying files:`n$($unexpected -join "`n")" }

    Invoke-Native git @('restore','--source=origin/main','--','registry-hosting.json','.github/CODEOWNERS') -AllowedExitCodes @(0,1) | Out-Null
    Remove-Item -LiteralPath (Join-Path $TargetRoot 'deployment-report.json') -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath (Join-Path $TargetRoot '.phylang-deployment-backup') -Recurse -Force -ErrorAction SilentlyContinue

    Write-Step 'Apply R9 deterministic package, cleanup, workflow and transport patch'
    Remove-Item -LiteralPath $BackupRoot -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $BackupRoot | Out-Null
    $payload = @(
        '.gitignore','.gitattributes','runtime\portable-go\package.go','runtime\portable-go\package_cross_platform_determinism_test.go',
        '.github\workflows\validate.yml','.github\workflows\deploy.yml','Deploy-To-GitHub-Windows10.ps1',
        'Resume-GitHub-Deployment-Windows10.ps1','Repair-Existing-GitHub-Deployment-Windows10.ps1',
        'Resume-R9-Repair-Push-Windows10.ps1','Diagnose-GitHub-HTTPS-TLS-Windows10.ps1',
        'Test-Deployment-Package.ps1','scripts\github\build-registry.ps1','scripts\github\build-registry.sh',
        'tools\windows-x64\phylang.exe','tools\windows-arm64\phylang.exe','HOTFIX-R7-README.zh-CN.md',
        'HOTFIX-R8-README.zh-CN.md','HOTFIX-R9-README.zh-CN.md','README.md','DEPLOYMENT-GUIDE.zh-CN.md'
    )
    foreach ($file in $payload) { Copy-PayloadFile $file }

    Write-Step 'Verify obsolete deployment backups are ignored and run regressions'
    $utf8Strict = New-Object System.Text.UTF8Encoding -ArgumentList @($false, $true)
    $gitignoreText = [IO.File]::ReadAllText((Join-Path $TargetRoot '.gitignore'), $utf8Strict)
    foreach ($requiredIgnore in @('/.phylang-deployment-backup/','/.phylang-r*-repair-backup/','/.phylang-r9-network-diagnostics/','/deployment-report.json')) {
        if (($gitignoreText -split "`r?`n") -notcontains $requiredIgnore) { throw "Missing required ignore rule: $requiredIgnore" }
    }

    Write-Step 'Run local Go and Windows registry regression tests'
    Push-Location (Join-Path $TargetRoot 'runtime\portable-go')
    try { Invoke-Native go @('test','./...') | Out-Null; Invoke-Native go @('vet','./...') | Out-Null } finally { Pop-Location }
    & (Join-Path $TargetRoot 'Build-Registry-Windows.ps1') -OutDirectory 'build\pages-r9-repair' -Repository $Repository -PagesUrl 'https://murongmoqing.github.io/phylang-registry/' -BuildRuntimeWithGo
    Invoke-Native git @('diff','--exit-code','--',':(glob)**/phylang.lock') | Out-Null
    & (Join-Path $TargetRoot 'Remove-Failed-Deployment.ps1') -Root $TargetRoot -BuildArtifactsOnly

    Write-Step 'Commit and push repair'
    Invoke-Native git @('-c','core.autocrlf=false','-c','core.eol=lf','-c','core.safecrlf=false','add','--all') | Out-Null
    Invoke-Native git @('diff','--cached','--check') | Out-Null
    Invoke-Native git @('commit','-m','Fix deterministic locks, transport recovery, and deployment backups') | Out-Null
    $CommitCreated = $true
    $NewCommit = ((Invoke-Native git @('rev-parse','HEAD') -Capture).Output -join '').Trim()
    Remove-Item -LiteralPath $BackupRoot -Recurse -Force -ErrorAction SilentlyContinue
    Push-WithRetry -Overrides $GitTransportOverrides -Attempts $PushAttempts
    $CommitPushed = $true
    Write-Host "Pushed repair commit: $NewCommit"

    Write-Step 'Continue with validated deployment'
    & (Join-Path $TargetRoot 'Resume-GitHub-Deployment-Windows10.ps1') -Repository $Repository -Commit $NewCommit -SkipHealthCheck:$SkipHealthCheck
    Write-Host "`n[PASS] Existing GitHub deployment was repaired, transport recovery was verified, temporary backups were removed, and publication was verified." -ForegroundColor Green
} catch {
    $failure = $_
    try {
        Set-Location -LiteralPath $TargetRoot
        Remove-Item -LiteralPath (Join-Path $TargetRoot 'build') -Recurse -Force -ErrorAction SilentlyContinue
        if ($CommitCreated -and -not $CommitPushed) {
            Remove-Item -LiteralPath $BackupRoot -Recurse -Force -ErrorAction SilentlyContinue
            Write-Warning "The repair commit was created locally but could not be pushed. It was preserved: $NewCommit"
            Write-Warning "Resume with: .\Resume-R9-Repair-Push-Windows10.ps1 -TargetRoot '$TargetRoot' -Repository '$Repository'"
        } elseif (-not $CommitPushed) {
            if ($OriginalHead) { Invoke-Native git @('reset','--mixed',$OriginalHead) -AllowedExitCodes @(0,1) | Out-Null }
            Restore-Backup
            Remove-Item -LiteralPath $BackupRoot -Recurse -Force -ErrorAction SilentlyContinue
            Write-Warning 'R9 repair failed before commit creation; local patched files were restored and the original HEAD was preserved.'
        } else {
            Write-Warning 'R9 repair commit was already pushed. It was preserved for diagnosis and must not be deleted automatically.'
        }
    } catch { Write-Warning "Repair rollback/preservation failed: $($_.Exception.Message)" }
    throw $failure
}
