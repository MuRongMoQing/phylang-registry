@echo off
setlocal
cd /d "%~dp0"
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0Deploy-To-GitHub-Windows10.ps1"
set "RC=%ERRORLEVEL%"
echo.
if not "%RC%"=="0" echo [FAIL] Deployment exited with code %RC%.
pause
exit /b %RC%
