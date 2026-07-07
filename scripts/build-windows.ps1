param(
    [switch]$PortableClean,
    [switch]$Zip
)

$ErrorActionPreference = "Stop"

trap {
    Write-Host ""
    Write-Host "Erro ao gerar EXE:" -ForegroundColor Red
    Write-Host $_.Exception.Message -ForegroundColor Red
    Write-Host ""
    Read-Host "Pressione ENTER para fechar"
    exit 1
}

$ProjectRoot = Split-Path -Parent $PSScriptRoot
Set-Location $ProjectRoot

Write-Host ""
Write-Host "DelayEngine - gerar EXE" -ForegroundColor Green

foreach ($processName in @("DelayEngine", "DelayEngineTray", "mediamtx", "ffmpeg")) {
    Get-Process -Name $processName -ErrorAction SilentlyContinue | ForEach-Object {
        try {
            Stop-Process -Id $_.Id -Force -ErrorAction Stop
        } catch {
        }
    }
}

$go = Get-Command go -ErrorAction SilentlyContinue
if ($null -eq $go) {
    throw "Go nao encontrado no PATH. Instale o Go ou use o ambiente onde go test ja funciona."
}

New-Item -ItemType Directory -Force -Path (Join-Path $ProjectRoot "dist") | Out-Null

Write-Host "Validando projeto..."
& $go.Source test ./...
if ($LASTEXITCODE -ne 0) {
    throw "Testes falharam."
}

Write-Host "Compilando dist\DelayEngine.exe..."
$env:GOOS = "windows"
$env:GOARCH = "amd64"
& $go.Source build -trimpath -ldflags="-s -w" -o ".\dist\DelayEngine.exe" ".\cmd\delayengine"
if ($LASTEXITCODE -ne 0) {
    throw "Build falhou."
}

Write-Host "Compilando dist\DelayEngineTray.exe..."
& $go.Source build -trimpath -ldflags="-s -w -H=windowsgui" -o ".\dist\DelayEngineTray.exe" ".\cmd\delayengine-tray"
if ($LASTEXITCODE -ne 0) {
    throw "Build do tray falhou."
}

Write-Host ""
Write-Host "EXEs criados:" -ForegroundColor Green
Write-Host (Join-Path $ProjectRoot "dist\DelayEngine.exe") -ForegroundColor Yellow
Write-Host (Join-Path $ProjectRoot "dist\DelayEngineTray.exe") -ForegroundColor Yellow

Write-Host ""
Write-Host "Montando pasta final dist\DelayEngineApp..."
$AppDir = Join-Path $ProjectRoot "dist\DelayEngineApp"
$StateFiles = @(".local-stream-name", "settings.json", ".twitch-stream-key.dpapi", ".mediamtx-path", ".ffmpeg-bin-path")
$StateBackupDir = Join-Path $ProjectRoot "dist\.build-state-backup"
$VideosBackupDir = Join-Path $StateBackupDir "videos"
if (Test-Path -LiteralPath $StateBackupDir) {
    Remove-Item -LiteralPath $StateBackupDir -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $StateBackupDir | Out-Null

if (Test-Path -LiteralPath $AppDir) {
    foreach ($file in $StateFiles) {
        $source = Join-Path $AppDir $file
        if (Test-Path -LiteralPath $source) {
            Copy-Item -LiteralPath $source -Destination (Join-Path $StateBackupDir $file) -Force
        }
    }
    $videosSource = Join-Path $AppDir "videos"
    if (Test-Path -LiteralPath $videosSource) {
        Copy-Item -LiteralPath $videosSource -Destination $VideosBackupDir -Recurse -Force
    }
}

if (Test-Path -LiteralPath $AppDir) {
    try {
        Remove-Item -LiteralPath $AppDir -Recurse -Force
    }
    catch {
        Write-Host "A pasta DelayEngineApp esta em uso. Vou reparar e copiar os arquivos por cima." -ForegroundColor Yellow
    }
}
New-Item -ItemType Directory -Force -Path $AppDir | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $AppDir "logs") | Out-Null

Copy-Item -LiteralPath (Join-Path $ProjectRoot "dist\DelayEngineTray.exe") -Destination (Join-Path $AppDir "DelayEngine.exe") -Force

foreach ($file in @("LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.md", "README.md", "SECURITY.md")) {
    $source = Join-Path $ProjectRoot $file
    if (Test-Path -LiteralPath $source) {
        Copy-Item -LiteralPath $source -Destination (Join-Path $AppDir $file) -Force
    }
}

foreach ($dir in @("web", "tools", "assets")) {
    $source = Join-Path $ProjectRoot $dir
    if (Test-Path -LiteralPath $source) {
        Copy-Item -LiteralPath $source -Destination (Join-Path $AppDir $dir) -Recurse -Force
    }
}

$AppScriptsDir = Join-Path $AppDir "scripts"
New-Item -ItemType Directory -Force -Path $AppScriptsDir | Out-Null
foreach ($file in @("prepare-slate.cmd", "prepare-slate.ps1", "install-ffmpeg.cmd", "install-ffmpeg.ps1", "limpeza-de-dados.cmd", "limpeza-de-dados.ps1")) {
    $source = Join-Path $ProjectRoot "scripts\$file"
    if (Test-Path -LiteralPath $source) {
        Copy-Item -LiteralPath $source -Destination (Join-Path $AppScriptsDir $file) -Force
    }
}

$cleanCmd = @"
@echo off
setlocal
cd /d "%~dp0"
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\limpeza-de-dados.ps1"
echo.
pause
"@
Set-Content -LiteralPath (Join-Path $AppDir "limpeza-de-dados.cmd") -Value $cleanCmd -Encoding ASCII

$ShortcutPath = Join-Path $AppDir "DelayEngine.lnk"
$ShortcutIcon = Join-Path $AppDir "assets\branding\delayengine-app-icon.ico"
if (Test-Path -LiteralPath $ShortcutIcon) {
    try {
        $wsh = New-Object -ComObject WScript.Shell
        $shortcut = $wsh.CreateShortcut($ShortcutPath)
        $shortcut.TargetPath = Join-Path $AppDir "DelayEngine.exe"
        $shortcut.WorkingDirectory = $AppDir
        $shortcut.IconLocation = $ShortcutIcon
        $shortcut.Description = "Abrir DelayEngine"
        $shortcut.Save()
    }
    catch {
        Write-Host "Nao foi possivel criar o atalho com icone personalizado: $_" -ForegroundColor Yellow
    }
}

if (Test-Path -LiteralPath $VideosBackupDir) {
    Copy-Item -LiteralPath $VideosBackupDir -Destination (Join-Path $AppDir "videos") -Recurse -Force
}
else {
    $source = Join-Path $ProjectRoot "videos"
    if (Test-Path -LiteralPath $source) {
        Copy-Item -LiteralPath $source -Destination (Join-Path $AppDir "videos") -Recurse -Force
    }
}

$DefaultVideoDir = Join-Path $AppDir "videos\default"
New-Item -ItemType Directory -Force -Path $DefaultVideoDir | Out-Null
$LiveLoading = Join-Path $AppDir "videos\live\loading.flv"
$DefaultLoading = Join-Path $DefaultVideoDir "loading.flv"
if (Test-Path -LiteralPath $LiveLoading) {
    Copy-Item -LiteralPath $LiveLoading -Destination $DefaultLoading -Force
}

foreach ($file in $StateFiles) {
    $source = Join-Path $ProjectRoot $file
    if (Test-Path -LiteralPath $source) {
        Copy-Item -LiteralPath $source -Destination (Join-Path $AppDir $file) -Force
    }
}

foreach ($file in $StateFiles) {
    $source = Join-Path $StateBackupDir $file
    if (Test-Path -LiteralPath $source) {
        Copy-Item -LiteralPath $source -Destination (Join-Path $AppDir $file) -Force
        Copy-Item -LiteralPath $source -Destination (Join-Path $ProjectRoot $file) -Force
    }
}

if ($PortableClean) {
	foreach ($privateFile in @(".twitch-stream-key.dpapi", ".mediamtx-path", ".ffmpeg-bin-path")) {
		$path = Join-Path $AppDir $privateFile
		if (Test-Path -LiteralPath $path) {
			Remove-Item -LiteralPath $path -Force
		}
	}
	$localStreamNamePath = Join-Path $AppDir ".local-stream-name"
	if (Test-Path -LiteralPath $localStreamNamePath) {
		Remove-Item -LiteralPath $localStreamNamePath -Force
	}

	foreach ($dir in @("logs", "runtime", "tmp")) {
		$path = Join-Path $AppDir $dir
		if (Test-Path -LiteralPath $path) {
			Remove-Item -LiteralPath $path -Recurse -Force
		}
		New-Item -ItemType Directory -Force -Path $path | Out-Null
	}
	$readyDir = Join-Path $AppDir "videos\ready"
	if (Test-Path -LiteralPath $readyDir) {
		Get-ChildItem -LiteralPath $readyDir -Force -File | Where-Object { $_.Name -ne ".gitkeep" } | ForEach-Object {
			Remove-Item -LiteralPath $_.FullName -Force
		}
	}
	if (Test-Path -LiteralPath $DefaultLoading) {
		New-Item -ItemType Directory -Force -Path (Split-Path -Parent $LiveLoading) | Out-Null
		Copy-Item -LiteralPath $DefaultLoading -Destination $LiveLoading -Force
	}

	$settingsPath = Join-Path $AppDir "settings.json"
	if (Test-Path -LiteralPath $settingsPath) {
		try {
			$settings = Get-Content -LiteralPath $settingsPath -Raw | ConvertFrom-Json
			$settings.localSourcePath = ""
			$settings.inputUrl = ""
			$settings.mediaMtxPath = ""
			$settings.activeLoadingPath = ""
			$settings.saveStreamKey = $false
			$settings.streamKeySaved = $false
			$settings.defaultDelaySeconds = 30
			$settings.viewerLatencySeconds = 0
			if ($null -ne $settings.obs) {
				$settings.obs.server = ""
				$settings.obs.streamKey = ""
				$settings.obs.fullUrl = ""
			}
			$settingsJson = $settings | ConvertTo-Json -Depth 8
			$utf8NoBom = New-Object System.Text.UTF8Encoding($false)
			[System.IO.File]::WriteAllText($settingsPath, $settingsJson, $utf8NoBom)
		}
		catch {
			Write-Host "Aviso: nao consegui limpar settings.json do pacote final." -ForegroundColor Yellow
		}
	}
}

$readme = @"
DelayEngine

Abra DelayEngine.exe com dois cliques.

Ele fica perto do relogio do Windows.
Se fechar o navegador, a live continua rodando.

Pelo icone perto do relogio:
- Abrir painel
- Voltar ao vivo agora
- Abrir pasta de logs
- Sair

Arquivos editaveis:
- web\index.html
- videos\live\loading.flv
- videos\ready
- settings.json
- logs
- limpeza-de-dados.cmd

Observacao:
- A chave da Twitch nao vai no pacote. Cada pessoa coloca a propria chave no primeiro uso.
- O MediaMTX vai junto em tools.
- O FFmpeg vai junto em tools\ffmpeg\bin para converter videos e usar o modo Twitch polido.
- Antes de mandar esta pasta para outra pessoa, rode limpeza-de-dados.cmd para zerar dados pessoais.
"@
Set-Content -LiteralPath (Join-Path $AppDir "LEIA-ME.txt") -Value $readme

foreach ($file in @("dist\DelayEngine.exe", "dist\DelayEngineTray.exe")) {
    $path = Join-Path $ProjectRoot $file
    if (Test-Path -LiteralPath $path) {
        Remove-Item -LiteralPath $path -Force
    }
}
if (Test-Path -LiteralPath $StateBackupDir) {
    Remove-Item -LiteralPath $StateBackupDir -Recurse -Force
}

$ZipPath = Join-Path $ProjectRoot "dist\DelayEngineApp-portatil.zip"
if ($PortableClean -and $Zip) {
	if (Test-Path -LiteralPath $ZipPath) {
		Remove-Item -LiteralPath $ZipPath -Force
	}
	Write-Host "Gerando ZIP portatil limpo..."
	Compress-Archive -LiteralPath $AppDir -DestinationPath $ZipPath -Force
}

Write-Host "Pasta final criada:" -ForegroundColor Green
Write-Host $AppDir -ForegroundColor Yellow
if ($PortableClean -and $Zip) {
	Write-Host "ZIP final criado:" -ForegroundColor Green
	Write-Host $ZipPath -ForegroundColor Yellow
}
Write-Host ""
Write-Host "Arquivos que continuam externos e editaveis:"
Write-Host "- web\index.html"
Write-Host "- videos\live\loading.flv"
Write-Host "- videos\ready\*.flv"
Write-Host "- .local-stream-name (criado automaticamente no primeiro uso)"
Write-Host ""
if ($PortableClean) {
	Write-Host "Modo portatil limpo:"
	Write-Host "- sua chave Twitch salva nao foi copiada"
	Write-Host "- caminhos absolutos do seu MediaMTX/FFmpeg nao foram copiados"
}
else {
	Write-Host "Modo local:"
	Write-Host "- configs locais foram preservadas"
	Write-Host "- para gerar pacote limpo para outra pessoa, rode: .\scripts\build-windows.ps1 -PortableClean"
}
Write-Host ""
Read-Host "Pressione ENTER para fechar"
