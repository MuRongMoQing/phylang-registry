[CmdletBinding()]
param(
    [string]$TargetRoot = '.',
    [ValidatePattern('^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$')]
    [string]$Repository = 'MuRongMoQing/phylang-registry'
)
Set-StrictMode -Version 2.0
$ErrorActionPreference='Stop'
$TargetRoot=(Resolve-Path -LiteralPath $TargetRoot).Path
$stamp=Get-Date -Format 'yyyyMMdd-HHmmss'
$ReportRoot=Join-Path ([IO.Path]::GetTempPath()) 'PhyLang-GitHub-TLS-Diagnostics'
New-Item -ItemType Directory -Force -Path $ReportRoot | Out-Null
$Report=Join-Path $ReportRoot "github-tls-diagnostic-$stamp.txt"
function Redact([string]$s){$s -replace '(?i)(authorization:\s*basic\s+)[^\r\n]+','$1<redacted>' -replace '(?i)(https?://)[^/@\s:]+:[^/@\s]+@','$1<redacted>@'}
function Run([string]$Title,[scriptblock]$Command){Add-Content -LiteralPath $Report -Value "`r`n=== $Title ===" -Encoding UTF8;$old=$ErrorActionPreference;try{$ErrorActionPreference='Continue';$o=@(& $Command 2>&1);$c=$LASTEXITCODE}catch{$o=@($_.Exception.Message);$c=1}finally{$ErrorActionPreference=$old};Add-Content -LiteralPath $Report -Value (Redact(($o|Out-String).Trim())) -Encoding UTF8;Add-Content -LiteralPath $Report -Value "exit=$c" -Encoding UTF8;[pscustomobject]@{ExitCode=$c;Text=(($o|Out-String).Trim())}}
Set-Location -LiteralPath $TargetRoot
Set-Content -LiteralPath $Report -Value "GitHub HTTPS/TLS diagnostic`r`ntime=$(Get-Date -Format o)`r`nrepository=$Repository`r`ntarget=$TargetRoot" -Encoding UTF8
Run 'git version' {git version}|Out-Null
Run 'gh version' {gh version}|Out-Null
Run 'gh auth status' {gh auth status --active --hostname github.com}|Out-Null
Run 'remote' {git remote -v}|Out-Null
Run 'relevant git config origins' {git config --show-origin --get-regexp '^(http|https)\.|^credential\.'}|Out-Null
Add-Content -LiteralPath $Report -Value "`r`n=== proxy environment (redacted) ===" -Encoding UTF8
foreach($n in 'HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','NO_PROXY','http_proxy','https_proxy','all_proxy','no_proxy'){ $v=[Environment]::GetEnvironmentVariable($n); if($v){Add-Content -LiteralPath $Report -Value ("$n="+(Redact $v)) -Encoding UTF8} }
Run 'DNS github.com' {Resolve-DnsName github.com}|Out-Null
Run 'TCP github.com:443' {Test-NetConnection github.com -Port 443 -InformationLevel Detailed}|Out-Null
Run 'curl HTTPS headers' {curl.exe -I --connect-timeout 20 --max-time 40 https://github.com/}|Out-Null
Run 'GitHub API via gh' {gh api "repos/$Repository" --jq .full_name}|Out-Null
$default=Run 'git ls-remote default' {git ls-remote origin refs/heads/main}
$http11=Run 'git ls-remote HTTP/1.1' {git -c http.version=HTTP/1.1 ls-remote origin refs/heads/main}
$schannel=Run 'git ls-remote Schannel + HTTP/1.1' {git -c http.sslBackend=schannel -c http.version=HTTP/1.1 ls-remote origin refs/heads/main}
Add-Content -LiteralPath $Report -Value "`r`n=== classification ===" -Encoding UTF8
if($default.ExitCode -eq 0){$classification='DEFAULT_GIT_HTTPS_OK'}elseif($http11.ExitCode -eq 0){$classification='HTTP2_OR_ALPN_PATH_SPECIFIC'}elseif($schannel.ExitCode -eq 0){$classification='OPENSSL_BACKEND_PATH_SPECIFIC'}else{$classification='ALL_GIT_HTTPS_MODES_FAILED'}
Add-Content -LiteralPath $Report -Value $classification -Encoding UTF8
Write-Host "Diagnostic report: $Report"
Write-Host "Classification: $classification"
