param(
    [string]$InputPath = "",
    [string]$OutputPath = "",
    [int]$Width = 0,
    [int]$Height = 0,
    [int]$Fps = 0,
    [string]$VideoBitrate = "",
    [string]$AudioBitrate = "160k",
    [int]$DurationSeconds = 0,
    [switch]$Activate
)

$ErrorActionPreference = "Stop"

trap {
    Write-Host ""
    Write-Host "Erro ao preparar o video:" -ForegroundColor Red
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

function Read-Default {
    param(
        [string]$Prompt,
        [string]$Default
    )
    $value = Read-Host "$Prompt [$Default]"
    if ([string]::IsNullOrWhiteSpace($value)) {
        return $Default
    }
    return $value.Trim()
}

function Convert-YesNo {
    param(
        [string]$Value,
        [bool]$Default
    )
    if ([string]::IsNullOrWhiteSpace($Value)) {
        return $Default
    }
    switch ($Value.Trim().ToLowerInvariant()) {
        "s" { return $true }
        "sim" { return $true }
        "y" { return $true }
        "yes" { return $true }
        "n" { return $false }
        "nao" { return $false }
        "no" { return $false }
        default { return $Default }
    }
}

function Get-NextReadyVideoPath {
    param(
        [string]$Directory,
        [int]$Duration
    )

    $index = 1
    while ($true) {
        $candidate = Join-Path $Directory ("video{0}x{1}s.flv" -f $index, $Duration)
        if (-not (Test-Path -LiteralPath $candidate)) {
            return $candidate
        }
        $index++
    }
}

function Get-MediaProbe {
    param([string]$Path)

    $ffprobePath = Find-LocalTool "ffprobe.exe"
    if ([string]::IsNullOrWhiteSpace($ffprobePath)) {
        return $null
    }

    try {
        $json = & $ffprobePath -v error -show_streams -show_format -of json $Path
        if ([string]::IsNullOrWhiteSpace($json)) {
            return $null
        }
        return $json | ConvertFrom-Json
    }
    catch {
        return $null
    }
}

function Convert-RateToFps {
    param([string]$Rate)

    if ([string]::IsNullOrWhiteSpace($Rate)) {
        return 0
    }
    if ($Rate.Contains("/")) {
        $parts = $Rate.Split("/")
        $num = [double]$parts[0]
        $den = [double]$parts[1]
        if ($den -eq 0) {
            return 0
        }
        return [Math]::Round($num / $den, 2)
    }
    return [double]$Rate
}

function Get-FirstStream {
    param(
        [object]$Probe,
        [string]$CodecType
    )

    if ($null -eq $Probe -or $null -eq $Probe.streams) {
        return $null
    }

    foreach ($stream in @($Probe.streams)) {
        if ($stream.codec_type -eq $CodecType) {
            return $stream
        }
    }
    return $null
}

function Show-MediaProbe {
    param(
        [string]$Title,
        [string]$Path
    )

    $probe = Get-MediaProbe -Path $Path
    Write-Step $Title
    if ($null -eq $probe) {
        Write-Host "Nao consegui medir codec/resolucao automaticamente porque o ffprobe nao esta no PATH." -ForegroundColor Yellow
        Write-Host "O video ainda foi preparado, mas vale conferir se combina com a live."
        return
    }

    $video = Get-FirstStream -Probe $probe -CodecType "video"
    $audio = Get-FirstStream -Probe $probe -CodecType "audio"
    $duration = 0.0
    $bitrateKbps = 0.0
    if ($null -ne $probe.format) {
        if ($probe.format.duration) {
            $duration = [double]$probe.format.duration
        }
        if ($probe.format.bit_rate) {
            $bitrateKbps = [Math]::Round(([double]$probe.format.bit_rate) / 1000, 0)
        }
    }

    if ($null -ne $video) {
        $fps = Convert-RateToFps $video.avg_frame_rate
        if ($fps -le 0) {
            $fps = Convert-RateToFps $video.r_frame_rate
        }
        Write-Host ("Video:   {0} {1}x{2} {3}fps" -f $video.codec_name, $video.width, $video.height, $fps)
        if ($video.profile) {
            Write-Host ("Perfil:  {0}" -f $video.profile)
        }
        if ($video.has_b_frames -ne $null) {
            Write-Host ("B-frames: {0}" -f $video.has_b_frames)
        }
    } else {
        Write-Host "Video:   nao encontrado" -ForegroundColor Yellow
    }

    if ($null -ne $audio) {
        Write-Host ("Audio:   {0} {1} Hz {2} canais" -f $audio.codec_name, $audio.sample_rate, $audio.channels)
    } else {
        Write-Host "Audio:   nao encontrado; a Twitch pode estranhar troca sem audio." -ForegroundColor Yellow
    }

    if ($duration -gt 0) {
        Write-Host ("Duracao: {0:N2}s" -f $duration)
    }
    if ($bitrateKbps -gt 0) {
        Write-Host ("Bitrate: {0:N0} kbps total" -f $bitrateKbps)
    }
}

$ProjectRoot = Split-Path -Parent $PSScriptRoot
Set-Location $ProjectRoot

$ReadyDir = Join-Path $ProjectRoot "videos\ready"
$LiveDir = Join-Path $ProjectRoot "videos\live"
$LiveSlatePath = Join-Path $LiveDir "loading.flv"
New-Item -ItemType Directory -Force -Path $ReadyDir | Out-Null
New-Item -ItemType Directory -Force -Path $LiveDir | Out-Null

Write-Host ""
Write-Host "DelayEngine - preparar video loading" -ForegroundColor Green
Write-Host "O video sera convertido para FLV H264/AAC, sem cortar a imagem."
Write-Host "Se for maior que a duracao escolhida, sera cortado. Se for menor, sera repetido."
Write-Host "Os videos prontos saem como video1x30s.flv, video2x15s.flv, etc."
Write-Host ""

if ([string]::IsNullOrWhiteSpace($InputPath)) {
    $InputPath = Read-Host "Arraste o MP4/video aqui e aperte ENTER"
    $InputPath = $InputPath.Trim('"').Trim()
}

$ffmpegPath = Find-LocalTool "ffmpeg.exe"
if ([string]::IsNullOrWhiteSpace($ffmpegPath)) {
    throw "FFmpeg nao encontrado. Rode scripts\install-ffmpeg.cmd, depois abra o conversor de novo."
}

if (-not (Test-Path -LiteralPath $InputPath)) {
    throw "Video de entrada nao encontrado: $InputPath"
}

$resolvedInput = (Resolve-Path -LiteralPath $InputPath).Path
$probe = Get-MediaProbe -Path $resolvedInput
if ($null -ne $probe -and $probe.streams.Count -gt 0) {
    $stream = Get-FirstStream -Probe $probe -CodecType "video"
}
if ($null -ne $stream) {
    $inputFps = Convert-RateToFps $stream.avg_frame_rate
    if ($inputFps -le 0) {
        $inputFps = Convert-RateToFps $stream.r_frame_rate
    }

    Write-Step "Video de entrada"
    Write-Host ("Resolucao: {0}x{1}" -f $stream.width, $stream.height)
    Write-Host ("FPS:       {0}" -f $inputFps)
}

if ($Width -le 0 -or $Height -le 0 -or $Fps -le 0 -or [string]::IsNullOrWhiteSpace($VideoBitrate)) {
    Write-Step "Formato do loading"
    Write-Host "Escolha o formato que vai ser gerado."
    Write-Host "Use o mesmo tamanho/FPS da live no OBS. O bitrate aqui e do video de loading."
    Write-Host ""
    Write-Host "1 - 1080p / 30fps / 4000k - recomendado para sua live atual"
    Write-Host "2 - 1080p / 30fps / 4500k - mais qualidade"
    Write-Host "3 - 1080p / 60fps / 5500k"
    Write-Host "4 - 720p  / 30fps / 3000k"
    Write-Host "5 - 720p  / 60fps / 4000k"
    Write-Host "6 - Customizado: escolher tamanho, FPS e bitrate"
    $choice = Read-Default "Digite uma opcao" "1"

    switch ($choice) {
        "2" {
            $Width = 1920
            $Height = 1080
            $Fps = 30
            $VideoBitrate = "4500k"
        }
        "3" {
            $Width = 1920
            $Height = 1080
            $Fps = 60
            $VideoBitrate = "5500k"
        }
        "4" {
            $Width = 1280
            $Height = 720
            $Fps = 30
            $VideoBitrate = "3000k"
        }
        "5" {
            $Width = 1280
            $Height = 720
            $Fps = 60
            $VideoBitrate = "4000k"
        }
        "6" {
            $Width = [int](Read-Default "Largura" "1920")
            $Height = [int](Read-Default "Altura" "1080")
            $Fps = [int](Read-Default "FPS" "30")
            $VideoBitrate = Read-Default "Bitrate de video" "4500k"
        }
        default {
            $Width = 1920
            $Height = 1080
            $Fps = 30
            $VideoBitrate = "4000k"
        }
    }
}

if ($DurationSeconds -le 0) {
    Write-Step "Tempo do loading"
    Write-Host "Esse e o tempo final do video de loading."
    Write-Host "Para armar delay de 30s, deixe 30s. Se o video original for menor, ele sera repetido."
    $DurationSeconds = [int](Read-Default "Duracao final em segundos" "30")
}
if ($DurationSeconds -le 0) {
    throw "Duracao invalida."
}

if ([string]::IsNullOrWhiteSpace($OutputPath)) {
    $OutputPath = Get-NextReadyVideoPath -Directory $ReadyDir -Duration $DurationSeconds
}

$resolvedOutput = $OutputPath
if (-not [System.IO.Path]::IsPathRooted($resolvedOutput)) {
    $resolvedOutput = Join-Path $ProjectRoot $resolvedOutput
}

$gop = [Math]::Max(1, $Fps * 2)
$bufSize = if ($VideoBitrate.EndsWith("k")) {
    $kbps = [int]($VideoBitrate.TrimEnd("k"))
    "$([Math]::Max([int]($kbps * 1.5), 1000))k"
} else {
    "6000k"
}
$x264Params = "keyint=${gop}:min-keyint=${gop}:scenecut=0:bframes=0:force-cfr=1:nal-hrd=cbr"
$vf = "scale=${Width}:${Height}:force_original_aspect_ratio=decrease,pad=${Width}:${Height}:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=${Fps},format=yuv420p"

Write-Step "Resumo"
Write-Host "Entrada: $resolvedInput"
Write-Host "Saida:   $resolvedOutput"
Write-Host "Video:   ${Width}x${Height} ${Fps}fps $VideoBitrate"
Write-Host "Audio:   AAC $AudioBitrate"
Write-Host "Tempo:   ${DurationSeconds}s"
Write-Host "Nome:    $([System.IO.Path]::GetFileName($resolvedOutput))"
Write-Host "Padrao:  CFR, sem B-frames, keyframe a cada 2s, H264/AAC FLV"

Write-Step "Convertendo"
& $ffmpegPath `
    -y `
    -stream_loop -1 `
    -i $resolvedInput `
    -t $DurationSeconds `
    -map 0:v:0 `
    -map 0:a:0? `
    -vf $vf `
    -r $Fps `
    -vsync cfr `
    -c:v libx264 `
    -preset veryfast `
    -tune zerolatency `
    -profile:v high `
    -level 4.2 `
    -pix_fmt yuv420p `
    -b:v $VideoBitrate `
    -maxrate $VideoBitrate `
    -bufsize $bufSize `
    -bf 0 `
    -g $gop `
    -keyint_min $gop `
    -sc_threshold 0 `
    -force_key_frames "expr:gte(t,n_forced*2)" `
    -x264-params $x264Params `
    -c:a aac `
    -b:a $AudioBitrate `
    -ar 48000 `
    -ac 2 `
    -f flv `
    $resolvedOutput

if ($LASTEXITCODE -ne 0) {
    throw "FFmpeg terminou com erro."
}

Write-Step "Pronto"
Write-Host "Video convertido salvo em:"
Write-Host $resolvedOutput -ForegroundColor Yellow
Show-MediaProbe -Title "Diagnostico do video pronto" -Path $resolvedOutput

$shouldActivate = $Activate.IsPresent
if (-not $shouldActivate) {
    $answer = Read-Host "Quer ativar este video agora na pasta da live? S/n"
    $shouldActivate = Convert-YesNo $answer $true
}

if ($shouldActivate) {
    Copy-Item -LiteralPath $resolvedOutput -Destination $LiveSlatePath -Force
    Write-Host ""
    Write-Host "Video ativo para a live:" -ForegroundColor Green
    Write-Host $LiveSlatePath -ForegroundColor Yellow
} else {
    Write-Host ""
    Write-Host "Para usar depois, copie ou arraste esse arquivo para:"
    Write-Host $LiveSlatePath -ForegroundColor Yellow
}
