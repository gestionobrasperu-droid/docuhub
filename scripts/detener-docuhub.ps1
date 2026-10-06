#Requires -Version 5.1
<#
.SYNOPSIS
    Detiene DocuHub de forma ordenada.

.DESCRIPTION
    Para el acceso directo del escritorio. Detener no borra nada: la base de
    datos vive en un volumen de Docker que sobrevive, y los archivos están en
    Google Drive. Al volver a iniciar, todo sigue donde estaba.

    Avisa antes si hay subidas en curso, porque cortarlas obliga a repetirlas.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\scripts\detener-docuhub.ps1
#>

[CmdletBinding()]
param(
    [switch]$Forzar
)

$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent $PSScriptRoot

$Host.UI.RawUI.WindowTitle = 'DocuHub - detener'

function Fin($codigo) {
    Write-Host ''
    Write-Host 'Pulsa una tecla para cerrar esta ventana...' -ForegroundColor DarkGray
    $null = $Host.UI.RawUI.ReadKey('NoEcho,IncludeKeyDown')
    exit $codigo
}

Clear-Host
Write-Host ''
Write-Host '  ========================================' -ForegroundColor Blue
Write-Host '    Detener DocuHub' -ForegroundColor White
Write-Host '  ========================================' -ForegroundColor Blue
Write-Host ''

$docker = 'C:\Program Files\Docker\Docker\resources\bin\docker.exe'
if (-not (Test-Path $docker)) { $docker = (Get-Command docker -ErrorAction SilentlyContinue).Source }
if (-not $docker) {
    Write-Host '   No se encontró Docker.' -ForegroundColor Red
    Fin 1
}

& $docker info --format '{{.ServerVersion}}' 2>$null | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Host '   El motor de Docker ya está apagado: no hay nada que detener.' -ForegroundColor Yellow
    Fin 0
}

# Cortar una subida a medias obliga a repetirla desde el principio, así que
# merece la pena preguntar antes.
if (-not $Forzar) {
    try {
        $r = Invoke-WebRequest -Uri 'http://localhost:8080/healthz' -TimeoutSec 5 -UseBasicParsing
        if ($r.StatusCode -eq 200) {
            Write-Host '   La plataforma está en marcha y accesible.' -ForegroundColor Yellow
            Write-Host '   Si alguien está subiendo un archivo ahora mismo, perderá el avance.'
            Write-Host ''
            $resp = Read-Host '   ¿Detener de todas formas? (s/N)'
            if ($resp -notmatch '^[sSyY]') {
                Write-Host ''
                Write-Host '   Cancelado. No se ha tocado nada.' -ForegroundColor Green
                Fin 0
            }
        }
    } catch { }
}

Write-Host ''
Write-Host '   Deteniendo contenedores...' -ForegroundColor Cyan
Push-Location (Join-Path $root 'deploy')
try {
    & $docker compose stop 2>&1 | ForEach-Object { Write-Host "     $_" -ForegroundColor DarkGray }
} finally {
    Pop-Location
}

Write-Host ''
Write-Host '   DocuHub detenido.' -ForegroundColor Green
Write-Host ''
Write-Host '   Los datos siguen intactos: la base está en un volumen de Docker y' -ForegroundColor DarkGray
Write-Host '   los archivos en Google Drive. Al volver a iniciar, todo sigue igual.' -ForegroundColor DarkGray
Write-Host ''
Write-Host '   Quien entre a docs.constructorapesam.com mientras tanto verá la' -ForegroundColor DarkGray
Write-Host '   página de "fuera de servicio".' -ForegroundColor DarkGray

Fin 0
