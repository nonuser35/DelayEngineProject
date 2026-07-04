@echo off
setlocal
cd /d "%~dp0.."
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0start-all.ps1"
if errorlevel 1 (
  echo.
  echo O iniciador terminou com erro.
  pause
  exit /b 1
)
