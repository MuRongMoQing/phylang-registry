@echo off
setlocal
cd /d "%~dp0"
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0Build-Registry-Windows.ps1" %*
exit /b %ERRORLEVEL%
