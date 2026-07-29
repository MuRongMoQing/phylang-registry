@echo off
setlocal
cd /d "%~dp0"
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0Resume-GitHub-Deployment-Windows10.ps1"
set "EXITCODE=%ERRORLEVEL%"
echo.
if not "%EXITCODE%"=="0" echo [FAIL] Resume exited with code %EXITCODE%.
pause
exit /b %EXITCODE%
