@echo off
setlocal
cd /d "%~dp0"
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0Remove-Failed-Deployment.ps1"
set "RC=%ERRORLEVEL%"
if not "%RC%"=="0" echo [FAIL] Cleanup exited with code %RC%.
pause
exit /b %RC%
