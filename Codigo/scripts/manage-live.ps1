param(
    [string]$ApiBase = "http://127.0.0.1:8080",
    [int]$ViewerLatencySeconds = 8
)

$ErrorActionPreference = "Stop"

trap {
    Write-Host ""
    Write-Host "Erro:" -ForegroundColor Red
    Write-Host $_.Exception.Message -ForegroundColor Red
    Write-Host ""
    Read-Host "Pressione ENTER para continuar"
    continue
}

function Write-Step {
    param([string]$Text)
    Write-Host ""
    Write-Host "== $Text ==" -ForegroundColor Cyan
}

function Find-LocalTool {
    param([string]$ExeName)

    $projectRoot = Get-ProjectRoot
    $command = Get-Command $ExeName -ErrorAction SilentlyContinue
    if ($null -ne $command) {
        return $command.Source
    }

    $savedPathFile = Join-Path $projectRoot ".ffmpeg-bin-path"
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
        (Join-Path $projectRoot "tools\ffmpeg\bin"),
        (Join-Path $projectRoot "tools\ffmpeg"),
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

function Read-MenuDefault {
    param(
        [string]$Prompt,
        [string]$Default
    )
    return Read-Default "$Prompt (0 para voltar)" $Default
}

function Wait-ViewerLatency {
    param([string]$Reason)

    if ($ViewerLatencySeconds -le 0) {
        return
    }

    Write-Host ""
    Write-Host ("Aguardando {0}s de latencia normal do player: {1}" -f $ViewerLatencySeconds, $Reason) -ForegroundColor Yellow
    for ($remaining = $ViewerLatencySeconds; $remaining -gt 0; $remaining--) {
        Write-Progress -Activity "Sincronizando com o que aparece na live" -Status ("{0}s restantes" -f $remaining) -PercentComplete ((($ViewerLatencySeconds - $remaining) / $ViewerLatencySeconds) * 100)
        Start-Sleep -Seconds 1
    }
    Write-Progress -Activity "Sincronizando com o que aparece na live" -Completed
}

function Get-ProjectRoot {
    return (Split-Path -Parent $PSScriptRoot)
}

function Get-LiveSlatePath {
    return (Join-Path (Get-ProjectRoot) "videos\live\loading.flv")
}

function Get-ReadyDir {
    return (Join-Path (Get-ProjectRoot) "videos\ready")
}

function Ensure-VideoDirs {
    New-Item -ItemType Directory -Force -Path (Get-ReadyDir) | Out-Null
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent (Get-LiveSlatePath)) | Out-Null
}

function Convert-PathForJson {
    param([string]$Path)
    return $Path.Replace("\", "/")
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

function Show-VideoDiagnostics {
    param([string]$Path)

    Write-Step "Diagnostico do video de loading"
    if (-not (Test-Path -LiteralPath $Path)) {
        Write-Host "Nenhum video ativo encontrado:" -ForegroundColor Yellow
        Write-Host $Path
        return
    }

    $file = Get-Item -LiteralPath $Path
    Write-Host ("Arquivo: {0}" -f $file.FullName) -ForegroundColor Green
    Write-Host ("Tamanho: {0:N2} MB" -f ($file.Length / 1MB))

    $probe = Get-MediaProbe -Path $Path
    if ($null -eq $probe) {
        Write-Host ""
        Write-Host "Nao consegui encontrar ffprobe.exe." -ForegroundColor Yellow
        Write-Host "Rode scripts\install-ffmpeg.cmd ou coloque o FFmpeg em tools\ffmpeg\bin."
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
        Write-Host "Audio:   nao encontrado; isso pode causar loading/engasgo na Twitch." -ForegroundColor Yellow
    }

    if ($duration -gt 0) {
        Write-Host ("Duracao: {0:N2}s" -f $duration)
    }
    if ($bitrateKbps -gt 0) {
        Write-Host ("Bitrate: {0:N0} kbps total" -f $bitrateKbps)
    }

    Write-Host ""
    Write-Host "Para reduzir loading, o ideal e esse video combinar com a live principal:" -ForegroundColor Yellow
    Write-Host "- mesma resolucao"
    Write-Host "- mesmo FPS"
    Write-Host "- audio AAC 48 kHz"
    Write-Host "- bitrate sem picos altos"
    Write-Host "- keyframe/GOP de 2s"
}

function Invoke-Api {
    param(
        [string]$Method,
        [string]$Path,
        [object]$Body = $null
    )

    try {
        $uri = "$ApiBase$Path"
        if ($null -eq $Body) {
            return Invoke-RestMethod -Method $Method -Uri $uri
        }

        $json = $Body | ConvertTo-Json -Compress
        return Invoke-RestMethod -Method $Method -Uri $uri -ContentType "application/json" -Body $json
    }
    catch {
        $response = $_.Exception.Response
        if ($null -ne $response) {
            $stream = $response.GetResponseStream()
            if ($null -ne $stream) {
                $reader = New-Object System.IO.StreamReader($stream)
                $bodyText = $reader.ReadToEnd()
                if (-not [string]::IsNullOrWhiteSpace($bodyText)) {
                    throw $bodyText
                }
            }
        }
        throw
    }
}

function Show-Status {
    Write-Step "Status"
    $status = Invoke-Api -Method Get -Path "/status"
    Write-Host ("OK:           {0}" -f $status.ok)
    Write-Host ("Delay ligado: {0}" -f $status.delayEnabled)
    Write-Host ("Delay alvo:   {0}" -f $status.delay)
    Write-Host ("Buffer:       {0} / {1} pacotes" -f $status.buffer.duration, $status.buffer.packets)
    Write-Host ("Entrada:      audio={0} video={1} conectado={2}" -f $status.input.audioPackets, $status.input.videoPackets, $status.input.connected)
    Write-Host ("Saida:        audio={0} video={1} conectado={2}" -f $status.output.audioPackets, $status.output.videoPackets, $status.output.connected)
}

function List-ReadyVideos {
    Ensure-VideoDirs
    return @(Get-ChildItem -LiteralPath (Get-ReadyDir) -Filter *.flv -File | Sort-Object Name)
}

function Select-ReadyVideo {
    $videos = List-ReadyVideos
    if ($videos.Count -eq 0) {
        Write-Host "Nenhum FLV pronto em videos\ready." -ForegroundColor Yellow
        Write-Host "Use a opcao de converter video primeiro."
        return $null
    }

    Write-Step "Videos prontos"
    for ($i = 0; $i -lt $videos.Count; $i++) {
        Write-Host ("{0} - {1}" -f ($i + 1), $videos[$i].Name)
    }

    $choiceText = Read-MenuDefault "Escolha um video" "1"
    if ($choiceText -eq "0") {
        return $null
    }
    $choice = [int]$choiceText
    if ($choice -lt 1 -or $choice -gt $videos.Count) {
        throw "Opcao invalida."
    }
    return $videos[$choice - 1]
}

function Activate-ReadyVideo {
    $video = Select-ReadyVideo
    if ($null -eq $video) {
        return $null
    }

    $livePath = Get-LiveSlatePath
    Copy-Item -LiteralPath $video.FullName -Destination $livePath -Force
    Write-Host ""
    Write-Host "Video ativo para delay:" -ForegroundColor Green
    Write-Host $livePath -ForegroundColor Yellow
    return $livePath
}

function Arm-Delay {
    param([string]$SlatePath)

    if ([string]::IsNullOrWhiteSpace($SlatePath)) {
        $SlatePath = Get-LiveSlatePath
    }
    if (-not (Test-Path -LiteralPath $SlatePath)) {
        Write-Host "Nenhum video ativo encontrado em videos\live\loading.flv." -ForegroundColor Yellow
        Write-Host "Ative um video pronto ou converta um novo video primeiro."
        return
    }

    $delayText = Read-MenuDefault "Delay em segundos" "30"
    if ($delayText -eq "0") {
        Write-Host "Voltando ao menu."
        return
    }
    $delaySeconds = [int]$delayText
    if ($delaySeconds -lt 0 -or $delaySeconds -gt 60) {
        throw "Delay precisa ficar entre 0 e 60 segundos."
    }

    Write-Step "Armando delay"
    Write-Host "A live vai mostrar o video de loading e voltar ja com delay."
    Write-Host "Aguarde: este comando so termina quando o video de loading acabar."
    Write-Host "Para uma transicao mais lisa, use loading com mesma resolucao/FPS da live e bitrate moderado."
    $body = @{
        delay = ("{0}s" -f $delaySeconds)
        slate = (Convert-PathForJson $SlatePath)
    }
    $status = Invoke-Api -Method Post -Path "/delay/arm" -Body $body
    Write-Host ("Delay ligado: {0}" -f $status.delay) -ForegroundColor Green
    Wait-ViewerLatency "a Twitch/player ainda pode estar mostrando o final do loading"
    Write-Host "Agora a tela do viewer deve estar mais perto da live com delay." -ForegroundColor Green
}

function Disable-Delay {
    Write-Step "Voltando ao vivo"
    Write-Host "Este comando nao usa video de loading. Ele descarta atraso acumulado e volta ao vivo no proximo keyframe."
    $status = Invoke-Api -Method Post -Path "/live/sync"
    Write-Host ("Delay ligado: {0}" -f $status.delayEnabled) -ForegroundColor Green
    Write-Host ("Delay alvo:   {0}" -f $status.delay)
    Wait-ViewerLatency "o player ainda pode estar mostrando alguns segundos antigos"
    Write-Host "Agora a tela do viewer deve estar mais perto do tempo real." -ForegroundColor Green
}

function Sync-LiveNow {
    Write-Step "Atualizar live agora"
    Write-Host "Forcando tempo real: fila atrasada sera descartada e a live volta no proximo keyframe."
    $status = Invoke-Api -Method Post -Path "/live/sync"
    Write-Host ("Delay ligado: {0}" -f $status.delayEnabled) -ForegroundColor Green
    Write-Host ("Delay alvo:   {0}" -f $status.delay)
    Wait-ViewerLatency "o player ainda pode estar mostrando alguns segundos antigos"
    Write-Host "Agora a tela do viewer deve estar mais perto do tempo real." -ForegroundColor Green
}

function Set-ViewerLatency {
    Write-Step "Latencia visual"
    Write-Host "Isso nao muda a live. Serve so para o gerenciador esperar o tempo normal do player/Twitch antes de dizer que voce ja deve estar vendo a mudanca."
    $latencyText = Read-MenuDefault "Segundos de espera visual" ([string]$ViewerLatencySeconds)
    if ($latencyText -eq "0") {
        return
    }
    $script:ViewerLatencySeconds = [int]$latencyText
    if ($script:ViewerLatencySeconds -lt 0) {
        $script:ViewerLatencySeconds = 0
    }
    Write-Host ("Espera visual ajustada para {0}s." -f $script:ViewerLatencySeconds) -ForegroundColor Green
}

function Open-Converter {
    Write-Step "Converter video"
    & (Join-Path $PSScriptRoot "prepare-slate.ps1")
}

function Show-ActiveVideo {
    Ensure-VideoDirs
    $livePath = Get-LiveSlatePath
    Write-Step "Video ativo"
    if (Test-Path -LiteralPath $livePath) {
        $file = Get-Item -LiteralPath $livePath
        Write-Host ("Arquivo: {0}" -f $file.FullName) -ForegroundColor Green
        Write-Host ("Tamanho: {0:N2} MB" -f ($file.Length / 1MB))
    } else {
        Write-Host "Nenhum video ativo em videos\live\loading.flv." -ForegroundColor Yellow
    }
}

function Show-Menu {
    Clear-Host
    Write-Host "DelayEngine - Gerenciador da live" -ForegroundColor Green
    Write-Host ""
    Write-Host "API: $ApiBase"
    Write-Host "Video ativo: $(Get-LiveSlatePath)"
    Write-Host "Espera visual do player: ${ViewerLatencySeconds}s"
    Write-Host ""
    Write-Host "1 - Status"
    Write-Host "2 - Ligar delay usando o video ativo"
    Write-Host "3 - Escolher video pronto e ligar delay"
    Write-Host "4 - Tirar delay e voltar ao vivo agora"
    Write-Host "5 - Converter/preparar novo video"
    Write-Host "6 - Escolher video pronto como ativo"
    Write-Host "7 - Mostrar video ativo"
    Write-Host "8 - Atualizar live agora se acumulou delay"
    Write-Host "9 - Ajustar espera visual do player"
    Write-Host "10 - Diagnosticar video de loading"
    Write-Host "0 - Sair"
    Write-Host ""
}

Ensure-VideoDirs
Set-Location (Get-ProjectRoot)

while ($true) {
    Show-Menu
    $choice = Read-Host "Digite uma opcao"

    switch ($choice) {
        "1" { Show-Status }
        "2" { Arm-Delay -SlatePath (Get-LiveSlatePath) }
        "3" {
            $path = Activate-ReadyVideo
            if ($null -ne $path) {
                Arm-Delay -SlatePath $path
            }
        }
        "4" { Disable-Delay }
        "5" { Open-Converter }
        "6" { Activate-ReadyVideo | Out-Null }
        "7" { Show-ActiveVideo }
        "8" { Sync-LiveNow }
        "9" { Set-ViewerLatency }
        "10" { Show-VideoDiagnostics -Path (Get-LiveSlatePath) }
        "0" { break }
        default { Write-Host "Opcao invalida." -ForegroundColor Yellow }
    }

    if ($choice -ne "0") {
        Write-Host ""
        Read-Host "Pressione ENTER para voltar ao menu"
    }
}
