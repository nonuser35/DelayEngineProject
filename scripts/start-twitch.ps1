param(
    [string]$InputUrl = "",
    [string]$TwitchServer = "rtmp://live.twitch.tv:1935/app",
    [string]$HttpAddr = ":8080",
    [switch]$ResetKey,
    [switch]$SkipInputPrompt
)

$ErrorActionPreference = "Stop"

function Write-Step {
    param([string]$Text)
    Write-Host ""
    Write-Host "== $Text ==" -ForegroundColor Cyan
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

    $alphabet = "23456789abcdefghjkmnpqrstuvwxyz"
    $token = -join (1..4 | ForEach-Object { $alphabet[(Get-Random -Minimum 0 -Maximum $alphabet.Length)] })
    $name = "live/delayengine-$token"
    Set-Content -LiteralPath $sourceFile -Value $name -NoNewline
    return "rtmp://127.0.0.1:1935/$name"
}

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$KeyFile = Join-Path $ProjectRoot ".twitch-stream-key"
$ReadyVideosDir = Join-Path $ProjectRoot "videos\ready"
$LiveVideosDir = Join-Path $ProjectRoot "videos\live"
$LiveSlatePath = Join-Path $LiveVideosDir "loading.flv"
New-Item -ItemType Directory -Force -Path $ReadyVideosDir | Out-Null
New-Item -ItemType Directory -Force -Path $LiveVideosDir | Out-Null
Set-Location $ProjectRoot
if ([string]::IsNullOrWhiteSpace($InputUrl) -or $InputUrl.EndsWith("/live/teste")) {
    $InputUrl = Get-DefaultInputUrl
}

Write-Host ""
Write-Host "DelayEngine - teste para Twitch" -ForegroundColor Green
Write-Host "Este assistente publica o stream do MediaMTX na Twitch sem mostrar sua stream key nos logs."
Write-Host ""

Write-Step "1. Confira a live de entrada"
Write-Host "Antes de continuar, deixe sua fonte publicando no MediaMTX."
Write-Host "Entrada atual: $InputUrl"
if (-not $SkipInputPrompt) {
    $customInput = Read-Host "Digite outra entrada RTMP ou aperte ENTER para usar esta"
    if ($customInput.Trim() -ne "") {
        $InputUrl = $customInput.Trim()
    }
} else {
    Write-Host "Entrada definida pelo iniciador geral."
}

Write-Step "2. Stream key da Twitch"
if ($ResetKey -and (Test-Path $KeyFile)) {
    Remove-Item -LiteralPath $KeyFile -Force
}

$streamKey = ""
if (Test-Path $KeyFile) {
    $streamKey = (Get-Content -LiteralPath $KeyFile -Raw).Trim()
    if (-not [string]::IsNullOrWhiteSpace($streamKey)) {
        Write-Host "Usando stream key salva em .twitch-stream-key."
        Write-Host "Para trocar a chave, rode: .\scripts\start-twitch.ps1 -ResetKey"
    }
}

if ([string]::IsNullOrWhiteSpace($streamKey)) {
    Write-Host "Cole sua stream key uma vez. Ela sera salva localmente para os proximos testes."
    Write-Host "A chave nao sera exibida na tela. Nao cole essa chave em prints ou chats."
    $streamKey = Read-SecretText "Stream key"
    if ([string]::IsNullOrWhiteSpace($streamKey)) {
        throw "Stream key vazia. Abra o painel da Twitch e copie a chave antes de rodar de novo."
    }

    Set-Content -LiteralPath $KeyFile -Value $streamKey -NoNewline
    Write-Host "Stream key salva em .twitch-stream-key."
}

Write-Step "3. Preparando ambiente"
$env:DELAYENGINE_INPUT_URL = $InputUrl
$env:DELAYENGINE_OUTPUT_URL = "$TwitchServer/$streamKey"
$env:DELAYENGINE_HTTP_ADDR = $HttpAddr
$env:DELAYENGINE_DELAY_ENABLED = "false"
$env:DELAYENGINE_FIXED_DELAY = "0s"

Write-Host "Entrada: $env:DELAYENGINE_INPUT_URL"
Write-Host "Saida:   $TwitchServer/<stream-key-oculta>"
Write-Host "API:     http://127.0.0.1$HttpAddr"
Write-Host "Loading ativo: $LiveSlatePath"
if (-not (Test-Path -LiteralPath $LiveSlatePath)) {
    Write-Host "Aviso: nenhum loading ativo encontrado em videos\live\loading.flv." -ForegroundColor Yellow
    Write-Host "Converta um video e arraste/copie o FLV de videos\ready para videos\live com o nome loading.flv."
}

Write-Step "4. Comandos uteis em outro PowerShell"
Write-Host "Iniciar ambiente completo com MediaMTX + DelayEngine + Gerenciador:"
Write-Host '.\scripts\start-all.cmd' -ForegroundColor Yellow
Write-Host ""
Write-Host "Gerenciador da live:"
Write-Host '.\scripts\manage-live.cmd' -ForegroundColor Yellow
Write-Host "Se quiser ajustar a espera visual do player: .\scripts\manage-live.cmd e opcao 9"
Write-Host ""
Write-Host "Status:"
Write-Host 'Invoke-RestMethod http://127.0.0.1:8080/status' -ForegroundColor Yellow
Write-Host ""
Write-Host "Preparar um MP4 para loading.flv:"
Write-Host '.\scripts\prepare-slate.cmd' -ForegroundColor Yellow
Write-Host "Ou arraste um video em cima de scripts\prepare-slate.cmd."
Write-Host "O assistente pergunta o video, qualidade, duracao e se deve ativar na pasta videos\live."
Write-Host "Se pedir FFmpeg, rode: .\scripts\install-ffmpeg.cmd"
Write-Host ""
Write-Host "Ligar delay de 10 segundos:"
Write-Host 'Invoke-RestMethod -Method Post http://127.0.0.1:8080/delay/on' -ForegroundColor Yellow
Write-Host 'Invoke-RestMethod -Method Post http://127.0.0.1:8080/delay/set -ContentType "application/json" -Body ''{"delay":"10s"}''' -ForegroundColor Yellow
Write-Host ""
Write-Host "Armar delay com video loading FLV de 30 segundos:"
Write-Host "Invoke-RestMethod -Method Post http://127.0.0.1:8080/delay/arm -ContentType `"application/json`" -Body '{`"delay`":`"30s`",`"slate`":`"$($LiveSlatePath.Replace('\','/'))`"}'" -ForegroundColor Yellow
Write-Host ""
Write-Host "Armar delay com video loading FLV de 10 segundos:"
Write-Host "Invoke-RestMethod -Method Post http://127.0.0.1:8080/delay/arm -ContentType `"application/json`" -Body '{`"delay`":`"10s`",`"slate`":`"$($LiveSlatePath.Replace('\','/'))`"}'" -ForegroundColor Yellow
Write-Host ""
Write-Host "Tirar delay e voltar ao tempo real agora:"
Write-Host 'Invoke-RestMethod -Method Post http://127.0.0.1:8080/live/sync' -ForegroundColor Yellow
Write-Host ""
Write-Host "Forcar live ao vivo agora se acumulou delay:"
Write-Host 'Invoke-RestMethod -Method Post http://127.0.0.1:8080/live/sync' -ForegroundColor Yellow

Write-Step "5. Iniciando DelayEngine"
Write-Host "Quando aparecer 'published first audio packet' e 'published first video packet', confira a Twitch."
Write-Host "Para parar, pressione CTRL+C."
Write-Host "Interface web: http://127.0.0.1$HttpAddr/"
Write-Host ""

$exePath = Join-Path $ProjectRoot "dist\DelayEngine.exe"
if (Test-Path -LiteralPath $exePath) {
    & $exePath
} else {
    go run ./cmd/delayengine
}
