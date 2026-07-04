@echo off
setlocal
cd /d "%~dp0.."
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install-ffmpeg.ps1"
if errorlevel 1 (
  echo.
  echo O instalador terminou com erro.
  pause
  exit /b 1
)
