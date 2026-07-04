$ErrorActionPreference = "Stop"

trap {
    Write-Host ""
    Write-Host "Erro ao limpar dados:" -ForegroundColor Red
    Write-Host $_.Exception.Message -ForegroundColor Red
    exit 1
}

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$AppRoot = Split-Path -Parent $ScriptDir
if ((Split-Path -Leaf $AppRoot) -eq "dist") {
    $AppRoot = Join-Path $AppRoot "DelayEngineApp"
}

function Get-FullPath {
    param([string]$Path)
    return [System.IO.Path]::GetFullPath($Path)
}

function Assert-InAppRoot {
    param([string]$Path)
    $root = (Get-FullPath $AppRoot).TrimEnd('\') + '\'
    $full = Get-FullPath $Path
    if (-not $full.StartsWith($root, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Caminho fora da pasta do app: $full"
    }
}

function Remove-AppFile {
    param([string]$RelativePath)
    $path = Join-Path $AppRoot $RelativePath
    Assert-InAppRoot $path
    if (Test-Path -LiteralPath $path) {
        Remove-Item -LiteralPath $path -Force
    }
}

function Clear-AppDirectoryFiles {
    param(
        [string]$RelativePath,
        [string[]]$KeepNames = @()
    )
    $path = Join-Path $AppRoot $RelativePath
    Assert-InAppRoot $path
    New-Item -ItemType Directory -Force -Path $path | Out-Null
    Get-ChildItem -LiteralPath $path -Force -File | Where-Object {
        $KeepNames -notcontains $_.Name
    } | ForEach-Object {
        Remove-Item -LiteralPath $_.FullName -Force
    }
}

function Reset-AppDirectory {
    param([string]$RelativePath)
    $path = Join-Path $AppRoot $RelativePath
    Assert-InAppRoot $path
    if (Test-Path -LiteralPath $path) {
        Remove-Item -LiteralPath $path -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $path | Out-Null
}

function Write-JsonNoBom {
    param(
        [string]$Path,
        [object]$Value
    )
    $json = $Value | ConvertTo-Json -Depth 10
    $encoding = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($Path, $json, $encoding)
}

Write-Host ""
Write-Host "DelayEngine - limpeza de dados do usuario" -ForegroundColor Cyan
Write-Host "Pasta: $AppRoot"
Write-Host ""
Write-Host "Feche o DelayEngine antes de limpar, para ele nao regravar dados antigos." -ForegroundColor Yellow
$confirm = Read-Host "Limpar chave Twitch, configs locais, logs e videos convertidos? (S/N)"
if ($confirm -notmatch '^[sS]') {
    Write-Host "Limpeza cancelada."
    exit 0
}

foreach ($file in @(
    ".twitch-stream-key",
    ".twitch-stream-key.dpapi",
    ".local-stream-name",
    ".mediamtx-path",
    ".ffmpeg-bin-path"
)) {
    Remove-AppFile $file
}

Clear-AppDirectoryFiles "logs" @(".gitkeep")
Clear-AppDirectoryFiles "videos\ready" @(".gitkeep")
Reset-AppDirectory "runtime"
Reset-AppDirectory "tmp"

$defaultLoading = Join-Path $AppRoot "videos\default\loading.flv"
$liveLoading = Join-Path $AppRoot "videos\live\loading.flv"
Assert-InAppRoot $defaultLoading
Assert-InAppRoot $liveLoading
if (Test-Path -LiteralPath $defaultLoading) {
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $liveLoading) | Out-Null
    Copy-Item -LiteralPath $defaultLoading -Destination $liveLoading -Force
}

$settingsPath = Join-Path $AppRoot "settings.json"
Assert-InAppRoot $settingsPath
$cleanSettings = [ordered]@{
    ok = $true
    mode = "twitch"
    saveStreamKey = $false
    localSourcePath = ""
    inputUrl = ""
    outputMode = "encoded"
    outputUrl = "rtmp://127.0.0.1:1935/live/delayengine-out"
    encodedLocalOutputUrl = "rtmp://127.0.0.1:1935/live/delayengine-out"
    twitchServer = "rtmp://live.twitch.tv:1935/app"
    encodedWidth = 1920
    encodedHeight = 1080
    encodedFps = 30
    encodedVideoBitrate = "4500k"
    encodedAudioBitrate = "160k"
    mediaMtxPath = ""
    activeLoadingPath = ""
    defaultDelaySeconds = 30
    returnLoadingSeconds = 6
    viewerLatencySeconds = 0
    obs = [ordered]@{
        server = ""
        streamKey = ""
        fullUrl = ""
    }
    streamKeySaved = $false
}
Write-JsonNoBom $settingsPath $cleanSettings

Write-Host ""
Write-Host "Limpeza concluida." -ForegroundColor Green
Write-Host "- Chave da Twitch removida"
Write-Host "- Configuracoes locais zeradas"
Write-Host "- Logs apagados"
Write-Host "- Videos convertidos em videos\ready apagados"
Write-Host "- Loading padrao restaurado quando videos\default\loading.flv existir"
