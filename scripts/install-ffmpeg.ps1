param()

$ErrorActionPreference = "Stop"

trap {
    Write-Host ""
    Write-Host "Erro ao instalar/localizar FFmpeg:" -ForegroundColor Red
    Write-Host $_.Exception.Message -ForegroundColor Red
    Write-Host ""
    Read-Host "Pressione ENTER para fechar"
    exit 1
}

function Write-Step {
    param([string]$Text)
    Write-Host ""
    Write-Host "== $Text ==" -ForegroundColor Cyan
}

function Find-Tool {
    param([string]$ExeName)

    $command = Get-Command $ExeName -ErrorAction SilentlyContinue
    if ($null -ne $command) {
        return $command.Source
    }

    $candidateDirs = @(
        (Join-Path $ProjectRoot "tools\ffmpeg\bin"),
        (Join-Path $ProjectRoot "tools\ffmpeg"),
        "$env:LOCALAPPDATA\Microsoft\WinGet\Packages",
        "$env:ProgramFiles\ffmpeg\bin",
        "${env:ProgramFiles(x86)}\ffmpeg\bin"
    )

    foreach ($dir in $candidateDirs) {
        if ([string]::IsNullOrWhiteSpace($dir) -or -not (Test-Path -LiteralPath $dir)) {
            continue
        }

        $found = Get-ChildItem -LiteralPath $dir -Recurse -Filter $ExeName -File -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($null -ne $found) {
            return $found.FullName
        }
    }

    return ""
}

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$PathFile = Join-Path $ProjectRoot ".ffmpeg-bin-path"
Set-Location $ProjectRoot

Write-Host ""
Write-Host "DelayEngine - instalar/localizar FFmpeg" -ForegroundColor Green
Write-Host "O FFmpeg e usado somente para preparar videos de loading offline."

$ffmpegPath = Find-Tool "ffmpeg.exe"
$ffprobePath = Find-Tool "ffprobe.exe"

if ([string]::IsNullOrWhiteSpace($ffmpegPath) -or [string]::IsNullOrWhiteSpace($ffprobePath)) {
    Write-Step "Instalando pelo winget"
    $winget = Get-Command winget -ErrorAction SilentlyContinue
    if ($null -eq $winget) {
        throw "winget nao encontrado. Instale FFmpeg manualmente ou coloque ffmpeg.exe e ffprobe.exe em tools\ffmpeg\bin."
    }

    & $winget.Source install --id Gyan.FFmpeg -e --source winget

    $ffmpegPath = Find-Tool "ffmpeg.exe"
    $ffprobePath = Find-Tool "ffprobe.exe"
}

if ([string]::IsNullOrWhiteSpace($ffmpegPath)) {
    throw "ffmpeg.exe nao encontrado depois da instalacao."
}
if ([string]::IsNullOrWhiteSpace($ffprobePath)) {
    throw "ffprobe.exe nao encontrado depois da instalacao."
}

$binDir = Split-Path -Parent $ffmpegPath
Set-Content -LiteralPath $PathFile -Value $binDir -NoNewline

Write-Step "Pronto"
Write-Host "FFmpeg encontrado em:" -ForegroundColor Green
Write-Host $ffmpegPath -ForegroundColor Yellow
Write-Host "FFprobe encontrado em:" -ForegroundColor Green
Write-Host $ffprobePath -ForegroundColor Yellow
Write-Host ""
Write-Host "Caminho salvo em .ffmpeg-bin-path. O conversor e o gerenciador vao usar esse caminho automaticamente."
Write-Host ""
Read-Host "Pressione ENTER para fechar"
