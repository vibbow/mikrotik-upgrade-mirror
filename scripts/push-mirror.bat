@echo off
rem Double-click to download the latest RouterOS packages and push them to the mirror server.
rem Settings (server, proxy) are at the top of push-mirror.ps1. Extra arguments are passed through,
rem e.g.  push-mirror.bat -Force
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0push-mirror.ps1" %*
echo.
pause
