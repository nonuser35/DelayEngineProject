@echo off
setlocal
cd /d "%~dp0.."
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0manage-live.ps1"
if errorlevel 1 (
  echo.
  echo O gerenciador terminou com erro.
  pause
  exit /b 1
)
