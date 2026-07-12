param(
    [string]$InputUrl = "",
    [string]$TwitchServer = "",
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
    Write-Host "Erro no relay com codificacao:" -ForegroundColor Red
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

function Find-ProjectRoot {
    $root = Split-Path -Parent $PSScriptRoot
    if (Test-Path -LiteralPath (Join-Path $root "settings.json")) {
        return $root
    }
    return $root
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

function Read-Settings {
    $path = Join-Path $ProjectRoot "settings.json"
    if (-not (Test-Path -LiteralPath $path)) {
        return $null
    }
    try {
        return (Get-Content -LiteralPath $path -Raw | ConvertFrom-Json)
    }
    catch {
        return $null
    }
}

function Read-Default {
    param([string]$Prompt, [string]$Default)
    $value = Read-Host "$Prompt [$Default]"
    if ([string]::IsNullOrWhiteSpace($value)) {
        return $Default
    }
    return $value.Trim()
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

$ProjectRoot = Find-ProjectRoot
Set-Location $ProjectRoot

Write-Host ""
Write-Host "DelayEngine - relay experimental com codificacao" -ForegroundColor Green
Write-Host "Este modo usa FFmpeg e recodifica H.264/AAC para testar keyframes de 1 segundo."
Write-Host "Nao substitui o modo leve atual; e apenas um teste separado."

$settings = Read-Settings
if ([string]::IsNullOrWhiteSpace($InputUrl)) {
    $defaultInput = "rtmp://127.0.0.1:1935/live/teste"
    if ($null -ne $settings -and -not [string]::IsNullOrWhiteSpace($settings.inputUrl)) {
        $defaultInput = $settings.inputUrl
    }
    $InputUrl = Read-Default "Entrada local do MediaMTX" $defaultInput
}

if ([string]::IsNullOrWhiteSpace($TwitchServer)) {
    $defaultServer = "rtmp://live.twitch.tv:1935/app"
    if ($null -ne $settings -and -not [string]::IsNullOrWhiteSpace($settings.twitchServer)) {
        $defaultServer = $settings.twitchServer
    }
    $TwitchServer = Read-Default "Servidor da Twitch" $defaultServer
}

if ([string]::IsNullOrWhiteSpace($StreamKey)) {
    $StreamKey = Read-SecretText "Cole a stream key da Twitch"
}

if ([string]::IsNullOrWhiteSpace($StreamKey)) {
    throw "Stream key vazia."
}

$ffmpegPath = Find-LocalTool "ffmpeg.exe"
if ([string]::IsNullOrWhiteSpace($ffmpegPath)) {
    throw "FFmpeg nao encontrado. Coloque o FFmpeg em tools\ffmpeg\bin ou configure pelo conversor de videos do app."
}

$outputUrl = ($TwitchServer.TrimEnd("/") + "/" + $StreamKey)
$gop = [Math]::Max(1, $Fps)

Write-Step "Perfil de teste"
Write-Host ("Entrada:   {0}" -f $InputUrl)
Write-Host ("Saida:     {0}/<chave>" -f $TwitchServer.TrimEnd("/"))
Write-Host ("Video:     {0}x{1} {2}fps {3}" -f $Width, $Height, $Fps, $VideoBitrate)
Write-Host ("Keyframe:  {0}s" -f 1)
Write-Host ("Audio:     AAC {0}" -f $AudioBitrate)

Write-Step "Iniciando"
Write-Host "Deixe o Streamlabs/OBS enviando para o MediaMTX local."
Write-Host "Quando quiser parar, pressione CTRL+C."

$scaleFilter = "scale={0}:{1}:force_original_aspect_ratio=decrease,pad={0}:{1}:(ow-iw)/2:(oh-ih)/2,format=yuv420p" -f $Width, $Height
$args = @(
    "-hide_banner",
    "-loglevel", "info",
    "-rtmp_live", "live",
    "-readrate", "1.0",
    "-readrate_catchup", "1.0",
    "-i", $InputUrl,
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

& $ffmpegPath @args
if ($LASTEXITCODE -ne 0) {
    throw "FFmpeg terminou com erro $LASTEXITCODE."
}


