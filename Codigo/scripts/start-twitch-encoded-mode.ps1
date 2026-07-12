param(
    [string]$InputUrl = "",
    [string]$LocalOutputUrl = "rtmp://127.0.0.1:1935/live/delayengine-out",
    [string]$TwitchServer = "rtmp://live.twitch.tv:1935/app",
    [string]$StreamKey = "",
    [int]$Width = 1920,
    [int]$Height = 1080,
    [int]$Fps = 30,
    [string]$VideoBitrate = "4500k",
    [string]$AudioBitrate = "160k"
)

$ErrorActionPreference = "Stop"

trap {
    Write-Host ""
    Write-Host "Erro ao iniciar modo recodificado:" -ForegroundColor Red
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

function Find-LocalTool {
    param([string]$ExeName)

    $command = Get-Command $ExeName -ErrorAction SilentlyContinue
    if ($null -ne $command) {
        return $command.Source
    }

    $savedPathFile = Join-Path $ProjectRoot ".ffmpeg-bin-path"
    if (Test-Path -LiteralPath $savedPathFile) {
        $savedDir = (Get-Content -LiteralPath $savedPathFile -Raw).Trim()
        if (-not [string]::IsNullOrWhiteSpace($savedDir)) {
            $candidate = Join-Path $savedDir $ExeName
            if (Test-Path -LiteralPath $candidate) {
                return $candidate
            }
        }
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
            Set-Content -LiteralPath $savedPathFile -Value $found.DirectoryName -NoNewline
            return $found.FullName
        }
    }

    return ""
}

function Read-SecretText {
    param([string]$Prompt)
    $secure = Read-Host $Prompt -AsSecureString
    $ptr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
    try {
        return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr)
    }
    finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr)
    }
}

function Get-DefaultInputUrl {
    $sourceFile = Join-Path $ProjectRoot ".local-stream-name"
    if (Test-Path -LiteralPath $sourceFile) {
        $saved = (Get-Content -LiteralPath $sourceFile -Raw).Trim().Trim("/")
        if (-not [string]::IsNullOrWhiteSpace($saved) -and $saved -ne "live/teste") {
            return "rtmp://127.0.0.1:1935/$saved"
        }
    }
    $name = "live/delayengine"
    Set-Content -LiteralPath $sourceFile -Value $name -NoNewline
    return "rtmp://127.0.0.1:1935/$name"
}

function Test-TcpPort {
    param([string]$HostName, [int]$Port)
    $client = New-Object System.Net.Sockets.TcpClient
    try {
        $async = $client.BeginConnect($HostName, $Port, $null, $null)
        if (-not $async.AsyncWaitHandle.WaitOne(500)) {
            return $false
        }
        $client.EndConnect($async)
        return $true
    }
    catch {
        return $false
    }
    finally {
        $client.Close()
    }
}

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$KeyFile = Join-Path $ProjectRoot ".twitch-stream-key"
Set-Location $ProjectRoot
New-Item -ItemType Directory -Force -Path (Join-Path $ProjectRoot "logs") | Out-Null

Write-Host ""
Write-Host "DelayEngine - modo Twitch com recodificacao" -ForegroundColor Green
Write-Host "Fluxo: OBS/Streamlabs -> MediaMTX -> DelayEngine local -> FFmpeg -> Twitch"
Write-Host "Objetivo: testar se a Twitch para de acumular delay ao sair do delay manual."

$ffmpegPath = Find-LocalTool "ffmpeg.exe"
if ([string]::IsNullOrWhiteSpace($ffmpegPath)) {
    throw "FFmpeg nao encontrado. Esperado em tools\ffmpeg\bin ou no PATH."
}

if ([string]::IsNullOrWhiteSpace($InputUrl)) {
    $InputUrl = Get-DefaultInputUrl
}

Write-Step "1. Conferir MediaMTX"
if (-not (Test-TcpPort -HostName "127.0.0.1" -Port 1935)) {
    Write-Host "MediaMTX nao parece estar rodando ainda." -ForegroundColor Yellow
    Write-Host "Abra o DelayEngine normal ou rode start-all.ps1 primeiro para subir o MediaMTX."
    Write-Host "Depois volte aqui."
    Read-Host "Pressione ENTER para continuar mesmo assim"
} else {
    Write-Host "MediaMTX RTMP detectado em 127.0.0.1:1935." -ForegroundColor Green
}

Write-Step "2. Entrada e saida local"
Write-Host "Entrada do OBS/Streamlabs:"
Write-Host $InputUrl -ForegroundColor Yellow
Write-Host "Saida local do DelayEngine para o FFmpeg:"
Write-Host $LocalOutputUrl -ForegroundColor Yellow

Write-Step "3. Twitch"
if (Test-Path -LiteralPath $KeyFile) {
    $saved = (Get-Content -LiteralPath $KeyFile -Raw).Trim()
    if (-not [string]::IsNullOrWhiteSpace($saved)) {
        $StreamKey = $saved
        Write-Host "Usando chave salva em .twitch-stream-key."
    }
}
if ([string]::IsNullOrWhiteSpace($StreamKey)) {
    $StreamKey = Read-SecretText "Cole a stream key da Twitch"
    if ([string]::IsNullOrWhiteSpace($StreamKey)) {
        throw "Stream key vazia."
    }
    Set-Content -LiteralPath $KeyFile -Value $StreamKey -NoNewline
    Write-Host "Stream key salva para os proximos testes."
}

Write-Step "4. Iniciando DelayEngine local"
Start-Process powershell -ArgumentList @(
    "-NoExit",
    "-ExecutionPolicy", "Bypass",
    "-Command", "cd '$ProjectRoot'; .\scripts\start-tray.ps1 -InputUrl '$InputUrl' -OutputUrl '$LocalOutputUrl' -SkipInputPrompt"
)

Write-Host "Aguarde o painel abrir e o OBS/Streamlabs estar transmitindo para a entrada local."
Read-Host "Quando o DelayEngine estiver publicando a saida local, pressione ENTER para iniciar o FFmpeg"

Write-Step "5. Iniciando FFmpeg para Twitch"
$outputUrl = ($TwitchServer.TrimEnd("/") + "/" + $StreamKey)
$gop = [Math]::Max(1, $Fps)
$scaleFilter = "scale={0}:{1}:force_original_aspect_ratio=decrease,pad={0}:{1}:(ow-iw)/2:(oh-ih)/2,format=yuv420p" -f $Width, $Height
$ffmpegLog = Join-Path $ProjectRoot "logs\ffmpeg-encoded-twitch.log"

$args = @(
    "-hide_banner",
    "-loglevel", "info",
    "-rtmp_live", "live",
    "-readrate", "1.0",
    "-readrate_catchup", "1.0",
    "-i", $LocalOutputUrl,
    "-map", "0:v:0",
    "-map", "0:a:0?",
    "-vf", $scaleFilter,
    "-r", "$Fps",
    "-c:v", "libx264",
    "-preset", "ultrafast",
    "-tune", "zerolatency",
    "-profile:v", "high",
    "-g", "$gop",
    "-keyint_min", "$gop",
    "-sc_threshold", "0",
    "-b:v", $VideoBitrate,
    "-maxrate", $VideoBitrate,
    "-bufsize", $VideoBitrate,
    "-c:a", "aac",
    "-b:a", $AudioBitrate,
    "-ar", "48000",
    "-ac", "2",
    "-max_interleave_delta", "0",
    "-muxdelay", "0",
    "-muxpreload", "0",
    "-flush_packets", "1",
    "-f", "flv",
    $outputUrl
)

Write-Host "FFmpeg vai recodificar com keyframe de 1s."
Write-Host "Log: $ffmpegLog" -ForegroundColor Yellow
$quotedArgs = ($args | ForEach-Object { '"' + ($_ -replace '"', '\"') + '"' }) -join " "
Start-Process powershell -ArgumentList @(
    "-NoExit",
    "-ExecutionPolicy", "Bypass",
    "-Command", "& '$ffmpegPath' $quotedArgs 2>&1 | Tee-Object -FilePath '$ffmpegLog' -Append"
)

Write-Host ""
Write-Host "Modo recodificado iniciado." -ForegroundColor Green
Write-Host "Use o painel do DelayEngine para adicionar/tirar delay."
Write-Host "A Twitch recebe o FFmpeg, nao o DelayEngine direto."
Write-Host ""
Read-Host "Pressione ENTER para fechar este launcher"


