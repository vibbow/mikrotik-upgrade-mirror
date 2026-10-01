@echo off
rem Double-click to walk the Winbox address book and upgrade routers from the mirror (asks before each step).
rem Settings (address book path, mirror address) are at the top of upgrade-routers.ps1.
rem Extra arguments are passed through, e.g.  upgrade-routers.bat -DryRun   or   -Only 192.168.1.1
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0upgrade-routers.ps1" %*
echo.
pause
