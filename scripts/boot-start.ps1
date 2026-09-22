#Requires -Version 5.1
<#
.SYNOPSIS
    Levanta DocuHub al encender la laptop. Lo ejecuta la tarea DocuHub-Arranque.

.DESCRIPTION
    Los contenedores tienen restart:unless-stopped, asi que Docker los levanta
    solo. El problema es el arranque en frio tras un corte de luz: Windows
    inicia la tarea antes de que el motor de Docker este listo, y un
    `docker compose up` prematuro falla y no se reintenta.

    Este script espera a que el motor responda (hasta 10 minutos) y entonces
    levanta la pila. Si el motor nunca aparece, lo deja escrito en el log para
    que el watchdog y la persona que revise lo vean.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\scripts\boot-start.ps1
#>

[CmdletBinding()]
param(
    [int]$MaxWaitMinutes = 10
)

$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent $PSScriptRoot
$logDir = Join-Path $root 'logs'
if (-not (Test-Path $logDir)) { New-Item -ItemType Directory -Path $logDir -Force | Out-Null }
$logFile = Join-Path $logDir 'boot.log'

function Log($text) {
    $line = '{0}  {1}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $text
    Add-Content -Path $logFile -Value $line -Encoding utf8
}

Log '--- arranque del equipo: preparando DocuHub ---'

$docker = 'C:\Program Files\Docker\Docker\resources\bin\docker.exe'
if (-not (Test-Path $docker)) {
    $docker = (Get-Command docker -ErrorAction SilentlyContinue).Source
}
if (-not $docker) {
    Log 'ERROR: no se encontro docker.exe'
    exit 1
}

# Docker Desktop necesita una sesion de usuario; si nadie inicio sesion aun,
# el motor tarda. Por eso la espera es generosa.
$deadline = (Get-Date).AddMinutes($MaxWaitMinutes)
$ready = $false
while ((Get-Date) -lt $deadline) {
    & $docker info --format '{{.ServerVersion}}' 2>$null | Out-Null
    if ($LASTEXITCODE -eq 0) { $ready = $true; break }
    Start-Sleep -Seconds 15
}

if (-not $ready) {
    Log "ERROR: el motor de Docker no respondio en $MaxWaitMinutes minutos"
    exit 1
}
Log 'motor de Docker listo'

Push-Location (Join-Path $root 'deploy')
try {
    $out = & $docker compose up -d 2>&1
    Log ("docker compose up -d -> codigo $LASTEXITCODE")
    foreach ($line in $out) { Log "  $line" }
} finally {
    Pop-Location
}

# Comprobacion final: que la aplicacion conteste de verdad, no solo que el
# contenedor exista.
Start-Sleep -Seconds 20
try {
    $res = Invoke-WebRequest -Uri 'http://localhost:8080/healthz' -TimeoutSec 20 -UseBasicParsing
    Log "healthz -> HTTP $($res.StatusCode)"
} catch {
    Log "healthz no respondio: $($_.Exception.Message) (el watchdog lo reintentara)"
}

Log '--- fin ---'
