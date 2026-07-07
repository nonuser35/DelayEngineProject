param(
    [string]$InputUrl = "",
    [string]$OutputUrl = "",
    [string]$TwitchServer = "rtmp://live.twitch.tv:1935/app",
    [string]$HttpAddr = ":8080",
    [switch]$ResetKey,
    [switch]$SkipInputPrompt
)

$ErrorActionPreference = "Stop"

trap {
    Write-Host ""
    Write-Host "Erro ao iniciar DelayEngine Tray:" -ForegroundColor Red
    Write-Host $_.Exception.Message -ForegroundColor Red
    Write-Host ""
    if (Test-Path (Join-Path $ProjectRoot "logs\delayengine.log")) {
        Write-Host "Ultimas linhas do log:" -ForegroundColor Yellow
        Get-Content -LiteralPath (Join-Path $ProjectRoot "logs\delayengine.log") -Tail 12
    }
    Write-Host ""
    Read-Host "Pressione ENTER para fechar"
    exit 1
}

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
    $name = "live/delayengine"
    Set-Content -LiteralPath $sourceFile -Value $name -NoNewline
    return "rtmp://127.0.0.1:1935/$name"
}

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$KeyFile = Join-Path $ProjectRoot ".twitch-stream-key"
$TrayExe = Join-Path $ProjectRoot "dist\DelayEngineTray.exe"
Set-Location $ProjectRoot
New-Item -ItemType Directory -Force -Path (Join-Path $ProjectRoot "logs") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $ProjectRoot "videos\ready") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $ProjectRoot "videos\live") | Out-Null
if ([string]::IsNullOrWhiteSpace($InputUrl) -or $InputUrl.EndsWith("/live/teste")) {
    $InputUrl = Get-DefaultInputUrl
}

Write-Host ""
Write-Host "DelayEngine - modo tray" -ForegroundColor Green
Write-Host "Ele fica rodando no Windows e abre o painel no navegador."

Write-Step "1. Entrada da live"
Write-Host "Entrada atual: $InputUrl"
if (-not $SkipInputPrompt) {
    $customInput = Read-Host "Digite outra entrada RTMP ou aperte ENTER para usar esta"
    if ($customInput.Trim() -ne "") {
        $InputUrl = $customInput.Trim()
    }
} else {
    Write-Host "Entrada definida pelo iniciador geral."
}

if ([string]::IsNullOrWhiteSpace($OutputUrl)) {
    Write-Step "2. Stream key da Twitch"
    if ($ResetKey -and (Test-Path -LiteralPath $KeyFile)) {
        Remove-Item -LiteralPath $KeyFile -Force
    }

    $streamKey = ""
    if (Test-Path -LiteralPath $KeyFile) {
        $streamKey = (Get-Content -LiteralPath $KeyFile -Raw).Trim()
        if (-not [string]::IsNullOrWhiteSpace($streamKey)) {
            Write-Host "Usando stream key salva em .twitch-stream-key."
        }
    }

    if ([string]::IsNullOrWhiteSpace($streamKey)) {
        Write-Host "Cole sua stream key uma vez. Ela sera salva localmente."
        $streamKey = Read-SecretText "Stream key"
        if ([string]::IsNullOrWhiteSpace($streamKey)) {
            throw "Stream key vazia."
        }
        Set-Content -LiteralPath $KeyFile -Value $streamKey -NoNewline
        Write-Host "Stream key salva."
    }

    $OutputUrl = "$TwitchServer/$streamKey"
} else {
    Write-Step "2. Saida customizada"
    Write-Host "O DelayEngine vai publicar nesta saida:"
    Write-Host $OutputUrl -ForegroundColor Yellow
    Write-Host "Neste modo ele nao pede a chave da Twitch."
}

if (-not (Test-Path -LiteralPath $TrayExe)) {
    Write-Step "3. Gerando EXE"
    & (Join-Path $PSScriptRoot "build-windows.ps1")
}

Write-Step "3. Iniciando tray"
$env:DELAYENGINE_ROOT = $ProjectRoot
$env:DELAYENGINE_INPUT_URL = $InputUrl
$env:DELAYENGINE_OUTPUT_URL = $OutputUrl
$env:DELAYENGINE_HTTP_ADDR = $HttpAddr
$env:DELAYENGINE_DELAY_ENABLED = "false"
$env:DELAYENGINE_FIXED_DELAY = "0s"

Start-Process -FilePath $TrayExe -WorkingDirectory $ProjectRoot

Write-Host ""
Write-Host "DelayEngine Tray iniciado." -ForegroundColor Green
Write-Host "Procure o icone perto do relogio do Windows. Talvez esteja em '^' / icones ocultos."
Write-Host "Painel: http://127.0.0.1$HttpAddr/"
Write-Host ""
Write-Host "Se nao aparecer, confira logs\delayengine.log."
Write-Host ""
Read-Host "Pressione ENTER para fechar este iniciador"
