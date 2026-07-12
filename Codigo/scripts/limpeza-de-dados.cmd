@echo off
setlocal
cd /d "%~dp0.."
powershell -NoProfile -ExecutionPolicy Bypass -File "%CD%\scripts\limpeza-de-dados.ps1" %*
echo.
pause
