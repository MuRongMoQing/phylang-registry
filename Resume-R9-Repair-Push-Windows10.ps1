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
$ErrorActionPreference='Stop'
$TargetRoot=(Resolve-Path -LiteralPath $TargetRoot).Path
$DiagnosticRoot=Join-Path ([IO.Path]::GetTempPath()) ('phylang-r9-network-diagnostics-' + [Guid]::NewGuid().ToString('N'))
function Invoke-Result { param([string[]]$Arguments) $old=$ErrorActionPreference; try{$ErrorActionPreference='Continue';$o=@(& git @Arguments 2>&1);$c=$LASTEXITCODE}finally{$ErrorActionPreference=$old}; [pscustomobject]@{ExitCode=$c;Text=(($o|Out-String).Trim());Output=$o} }
function Save-Log { param([string]$Name,[object]$Result) New-Item -ItemType Directory -Force -Path $DiagnosticRoot|Out-Null; $p=Join-Path $DiagnosticRoot $Name; $t=$Result.Text -replace '(?i)(authorization:\s*basic\s+)[^\r\n]+','$1<redacted>' -replace '(?i)(https?://)[^/@\s:]+:[^/@\s]+@','$1<redacted>@'; [IO.File]::WriteAllText($p,$t+"`r`n",(New-Object Text.UTF8Encoding($true)));$p }
function Retryable([string]$t){$t -match '(?i)(TLS connect error|unexpected eof|connection reset|recv failure|failed to connect|could not resolve host|timed out|operation was too slow|HTTP (500|502|503|504)|remote end hung up unexpectedly)'}
function Select-Mode {
    $modes=@(@(),@('-c','http.version=HTTP/1.1'),@('-c','http.sslBackend=schannel','-c','http.version=HTTP/1.1'))
    $names=@('default','http11','schannel-http11')
    for($i=0;$i -lt $modes.Count;$i++){ $r=Invoke-Result @($modes[$i]+@('ls-remote','origin','refs/heads/main')); Save-Log "resume-ls-remote-$($names[$i]).log" $r|Out-Null; if($r.ExitCode -eq 0){Write-Host "[PASS] Transport mode $($names[$i])";return @($modes[$i])} }
    throw "All Git HTTPS transport modes failed. See $DiagnosticRoot"
}
Set-Location -LiteralPath $TargetRoot
if(-not(Test-Path .git -PathType Container)){throw 'TargetRoot is not a Git repository.'}
& gh auth setup-git --hostname github.com
if($LASTEXITCODE -ne 0){throw 'gh auth setup-git failed.'}
$mode=Select-Mode
$fetch=Invoke-Result @($mode+@('fetch','origin','main'))
if($fetch.ExitCode -ne 0){$log=Save-Log 'resume-fetch.log' $fetch;throw "git fetch origin main failed. $log`n$($fetch.Text)"}
$head=(& git rev-parse HEAD).Trim();$remote=(& git rev-parse origin/main).Trim();$base=(& git merge-base origin/main HEAD).Trim()
if($head -eq $remote){throw 'Local HEAD already equals origin/main; there is no unpushed repair commit.'}
if($base -ne $remote){throw "Local HEAD is not a fast-forward descendant of origin/main. HEAD=$head origin/main=$remote merge-base=$base"}
$ahead=[int]((& git rev-list --count origin/main..HEAD).Trim())
Write-Host "Local repair commit(s) ahead of origin/main: $ahead"
$delays=@(0,2,5,10,20,30,45,60,90,120)
for($attempt=1;$attempt -le $PushAttempts;$attempt++){
    if($attempt -gt 1){Start-Sleep -Seconds $delays[[Math]::Min($attempt-1,$delays.Count-1)]}
    $r=Invoke-Result @($mode+@('push','origin','main'));$log=Save-Log "resume-push-attempt-$attempt.log" $r
    if($r.ExitCode -eq 0){Write-Host "[PASS] Push succeeded on attempt $attempt." -ForegroundColor Green;break}
    if(-not(Retryable $r.Text)){throw "Non-retryable push failure. $log`n$($r.Text)"}
    if($attempt -eq $PushAttempts){throw "Push failed after $PushAttempts retryable attempts. Local commit remains preserved. See $DiagnosticRoot"}
    Write-Warning "Retryable transport failure. $log"
}
& (Join-Path $TargetRoot 'Resume-GitHub-Deployment-Windows10.ps1') -Repository $Repository -Commit $head -SkipHealthCheck:$SkipHealthCheck
if($LASTEXITCODE -ne 0){throw 'Post-push deployment verification failed.'}
