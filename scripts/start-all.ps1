param(
    [string]$MediaMTXPath = "",
    [string]$InputUrl = "",
    [string]$SourcePath = "",
    [string]$MediaMTXRTMPHost = "127.0.0.1",
    [int]$MediaMTXRTMPPort = 1935,
    [switch]$ResetMediaMTXPath
)

$ErrorActionPreference = "Stop"

trap {
    Write-Host ""
    Write-Host "Erro ao iniciar ambiente:" -ForegroundColor Red
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

function Wait-User {
    param([string]$Text)
    Write-Host ""
    Read-Host $Text
}

function Test-TcpPort {
    param(
        [string]$HostName,
        [int]$Port
    )

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

function Wait-TcpPort {
    param(
        [string]$HostName,
        [int]$Port,
        [int]$TimeoutSeconds = 20
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        if (Test-TcpPort -HostName $HostName -Port $Port) {
            return $true
        }
        Start-Sleep -Milliseconds 500
    }
    return $false
}

function Normalize-SourcePath {
    param([string]$Path)

    $normalized = $Path.Trim().Trim("/")
    if ([string]::IsNullOrWhiteSpace($normalized)) {
        return Get-DefaultSourcePath
    }
    return $normalized
}

function Get-DefaultSourcePath {
    $sourceFile = Join-Path $ProjectRoot ".local-stream-name"
    if (Test-Path -LiteralPath $sourceFile) {
        $saved = (Get-Content -LiteralPath $sourceFile -Raw).Trim().Trim("/")
        if (-not [string]::IsNullOrWhiteSpace($saved) -and $saved -ne "live/teste") {
            return $saved
        }
    }

    $alphabet = "23456789abcdefghjkmnpqrstuvwxyz"
    $token = -join (1..4 | ForEach-Object { $alphabet[(Get-Random -Minimum 0 -Maximum $alphabet.Length)] })
    $name = "live/delayengine-$token"
    Set-Content -LiteralPath $sourceFile -Value $name -NoNewline
    return $name
}

function Write-SourceGuide {
    param(
        [string]$Path,
        [string]$FullInputUrl
    )

    $segments = $Path.Split("/")
    $streamKey = $segments[$segments.Length - 1]
    $serverPath = $Path
    if ($segments.Length -gt 1) {
        $serverPath = ($segments[0..($segments.Length - 2)] -join "/")
    } else {
        $serverPath = "live"
    }

    $serverUrl = "rtmp://${MediaMTXRTMPHost}:${MediaMTXRTMPPort}/${serverPath}"
    $guidePath = Join-Path $ProjectRoot "OBS-Streamlabs-config.txt"

    $guide = @"
DelayEngine - Configuracao OBS / Streamlabs

Neste fluxo o OBS/Streamlabs NAO envia direto para a Twitch.
Ele envia para o MediaMTX local.
Depois o DelayEngine pega essa live local e envia para a Twitch.

Nao coloque sua chave da Twitch no OBS/Streamlabs.
Sua chave da Twitch fica somente no DelayEngine.

OBS / Streamlabs:
Service/Servico: Custom / Personalizado
Server/Servidor: $serverUrl
Stream Key/Chave de transmissao: $streamKey

Importante:
- No campo Server/Servidor, cole somente: $serverUrl
- No campo Stream Key/Chave, cole somente: $streamKey
- Nao cole servidor/chave juntos no campo Servidor.

Recomendado para voltar ao vivo com menos atraso:
- Resolucao igual ao video de loading ativo.
- FPS igual ao video de loading ativo.
- Controle de taxa: CBR.
- Keyframe interval: 1s.
- Audio: 48 kHz.
- B-frames: 0, se existir essa opcao.

Entrada DelayEngine:
$FullInputUrl
"@

    Set-Content -LiteralPath $guidePath -Value $guide

    Write-Step "3. Configurar OBS / Streamlabs"
    Write-Host "No OBS ou Streamlabs, abra:"
    Write-Host "Configuracoes > Transmissao / Stream" -ForegroundColor Yellow
    Write-Host ""
    Write-Host "Preencha assim:"
    Write-Host "Servico:              Custom / Personalizado"
    Write-Host "Servidor / Server:    $serverUrl" -ForegroundColor Yellow
    Write-Host "Chave / Stream key:   $streamKey" -ForegroundColor Yellow
    Write-Host ""
    Write-Host "Importante:"
    Write-Host "- Nao coloque sua chave da Twitch no OBS/Streamlabs."
    Write-Host "- A chave aqui e somente '$streamKey'."
    Write-Host "- Nao cole '$FullInputUrl' no campo Servidor se o OBS mostrar campos separados."
    Write-Host "- O OBS/Streamlabs publica no MediaMTX local."
    Write-Host "- O DelayEngine envia para a Twitch."
    Write-Host "- Para sair do delay mais perto do ao vivo, use Keyframe interval: 1s no OBS/Streamlabs."
    Write-Host ""
    Write-Host "Depois clique em 'Iniciar transmissao' no OBS/Streamlabs."
    Write-Host ""
    Write-Host "Guia salvo em:"
    Write-Host $guidePath -ForegroundColor Yellow
}

function Find-LocalMediaMTX {
    $candidates = @(
        (Join-Path $ProjectRoot "tools\mediamtx\mediamtx.exe"),
        (Join-Path $ProjectRoot "mediamtx\mediamtx.exe"),
        (Join-Path $ProjectRoot "bin\mediamtx.exe")
    )

    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath $candidate) {
            return $candidate
        }
    }

    $found = Get-ChildItem -LiteralPath $ProjectRoot -Recurse -Filter mediamtx.exe -File -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -ne $found) {
        return $found.FullName
    }

    return ""
}

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$MediaMTXPathFile = Join-Path $ProjectRoot ".mediamtx-path"
Set-Location $ProjectRoot
New-Item -ItemType Directory -Force -Path (Join-Path $ProjectRoot "tools\mediamtx") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $ProjectRoot "logs") | Out-Null

Write-Host ""
Write-Host "DelayEngine - iniciar ambiente completo" -ForegroundColor Green
Write-Host "Este assistente abre uma etapa por vez: MediaMTX, OBS/Streamlabs, DelayEngine e gerenciador."

Write-Step "1. Entrada local da live"
Write-Host "Este e o caminho que OBS/Streamlabs vai publicar no MediaMTX."
if ([string]::IsNullOrWhiteSpace($SourcePath)) {
    $SourcePath = Get-DefaultSourcePath
}
Write-Host "Padrao gerado: $SourcePath"
Write-Host "Esse nome local evita conflito com caminhos antigos de teste e nao e a chave da Twitch."
$customSourcePath = Read-Host "Caminho da live local ou ENTER para usar $SourcePath"
if (-not [string]::IsNullOrWhiteSpace($customSourcePath)) {
    $SourcePath = $customSourcePath
}
$SourcePath = Normalize-SourcePath $SourcePath
if ([string]::IsNullOrWhiteSpace($InputUrl)) {
    $InputUrl = "rtmp://${MediaMTXRTMPHost}:${MediaMTXRTMPPort}/${SourcePath}"
}

if ($ResetMediaMTXPath -and (Test-Path -LiteralPath $MediaMTXPathFile)) {
    Remove-Item -LiteralPath $MediaMTXPathFile -Force
}

if ([string]::IsNullOrWhiteSpace($MediaMTXPath) -and (Test-Path -LiteralPath $MediaMTXPathFile)) {
    $MediaMTXPath = (Get-Content -LiteralPath $MediaMTXPathFile -Raw).Trim()
}

if ([string]::IsNullOrWhiteSpace($MediaMTXPath)) {
    $MediaMTXPath = Find-LocalMediaMTX
    if (-not [string]::IsNullOrWhiteSpace($MediaMTXPath)) {
        Write-Host "MediaMTX encontrado automaticamente:" -ForegroundColor Green
        Write-Host $MediaMTXPath -ForegroundColor Yellow
    }
}

if ([string]::IsNullOrWhiteSpace($MediaMTXPath)) {
    Write-Step "2. Localizar MediaMTX"
    Write-Host "Coloque o MediaMTX em tools\mediamtx\mediamtx.exe para nao precisar informar caminho."
    Write-Host "Ou arraste o mediamtx.exe aqui / cole o caminho completo."
    $MediaMTXPath = (Read-Host "Caminho do mediamtx.exe").Trim('"').Trim()
}

if (-not (Test-Path -LiteralPath $MediaMTXPath)) {
    throw "mediamtx.exe nao encontrado: $MediaMTXPath"
}

Set-Content -LiteralPath $MediaMTXPathFile -Value $MediaMTXPath -NoNewline

Write-Step "2. Subir MediaMTX"
if (Test-TcpPort -HostName $MediaMTXRTMPHost -Port $MediaMTXRTMPPort) {
    Write-Host "MediaMTX parece ja estar rodando em ${MediaMTXRTMPHost}:${MediaMTXRTMPPort}."
} else {
    $mediaMTXDir = Split-Path -Parent $MediaMTXPath
    $mediaMTXLog = Join-Path $ProjectRoot "logs\mediamtx.log"
    Start-Process powershell -ArgumentList @(
        "-NoExit",
        "-ExecutionPolicy", "Bypass",
        "-Command", "cd '$mediaMTXDir'; & '$MediaMTXPath' 2>&1 | Tee-Object -FilePath '$mediaMTXLog' -Append"
    )

    Write-Host "Aguardando RTMP do MediaMTX em ${MediaMTXRTMPHost}:${MediaMTXRTMPPort}..."
    if (-not (Wait-TcpPort -HostName $MediaMTXRTMPHost -Port $MediaMTXRTMPPort -TimeoutSeconds 20)) {
        throw "MediaMTX nao abriu a porta RTMP ${MediaMTXRTMPHost}:${MediaMTXRTMPPort} dentro do tempo esperado."
    }
}

Write-SourceGuide -Path $SourcePath -FullInputUrl $InputUrl
Wait-User "Depois de clicar em INICIAR TRANSMISSAO no OBS/Streamlabs, pressione ENTER aqui"

Write-Step "4. Iniciando DelayEngine"
Start-Process powershell -ArgumentList @(
    "-NoExit",
    "-ExecutionPolicy", "Bypass",
    "-Command", "cd '$ProjectRoot'; .\scripts\start-tray.ps1 -InputUrl '$InputUrl' -SkipInputPrompt"
)

Write-Host ""
Write-Host "Uma nova janela abriu para iniciar o DelayEngine Tray." -ForegroundColor Green
Write-Host "Se ela pedir a stream key da Twitch, cole a chave la."
Write-Host "Depois o painel abre no navegador e o icone fica perto do relogio do Windows."
Wait-User "Quando o painel abrir no navegador, pressione ENTER para continuar"

Write-Host ""
Write-Host "Ambiente iniciado." -ForegroundColor Green
Write-Host "Use o painel web para ligar/tirar delay, escolher videos e ver logs."
Write-Host ""
Read-Host "Pressione ENTER para fechar este launcher"
