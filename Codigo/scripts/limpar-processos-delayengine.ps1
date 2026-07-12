param(
    [switch]$Silent
)

$ErrorActionPreference = "SilentlyContinue"

$names = @(
    "DelayEngine",
    "DelayEngineTray",
    "mediamtx",
    "ffmpeg"
)

$stopped = @()

foreach ($name in $names) {
    $processes = Get-Process -Name $name -ErrorAction SilentlyContinue
    foreach ($process in $processes) {
        try {
            Stop-Process -Id $process.Id -Force -ErrorAction Stop
            $stopped += "$($process.ProcessName) ($($process.Id))"
        } catch {
            if (-not $Silent) {
                Write-Host "Nao foi possivel finalizar $($process.ProcessName) ($($process.Id))."
            }
        }
    }
}

if (-not $Silent) {
    if ($stopped.Count -eq 0) {
        Write-Host "Nenhum processo do DelayEngine estava aberto."
    } else {
        Write-Host "Processos finalizados:"
        foreach ($item in $stopped) {
            Write-Host "- $item"
        }
    }
    Write-Host ""
    Write-Host "Pronto. Agora voce pode abrir o DelayEngine novamente."
    Write-Host "Pressione ENTER para fechar."
    [void][System.Console]::ReadLine()
}
