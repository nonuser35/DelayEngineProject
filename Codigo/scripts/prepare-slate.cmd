@echo off
setlocal
cd /d "%~dp0.."

if "%~1"=="" (
  powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0prepare-slate.ps1"
) else (
  powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0prepare-slate.ps1" -InputPath "%~1"
)

if errorlevel 1 (
  echo.
  echo O conversor terminou com erro.
  pause
  exit /b 1
)

echo.
echo Conversao finalizada.
pause
