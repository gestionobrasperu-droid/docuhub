#Requires -Version 5.1
<#
.SYNOPSIS
    Arranca DocuHub con doble clic y deja la plataforma lista para usar.

.DESCRIPTION
    Pensado para el acceso directo del escritorio: lo ejecuta una persona, no
    el Programador de tareas, así que va contando lo que hace y, si algo falla,
    dice qué pasa y qué hacer en lugar de cerrarse sin más.

    Secuencia:
      1. Arranca Docker Desktop si no está en marcha y espera a su motor
      2. Levanta los contenedores
      3. Espera a que la plataforma responda de verdad (no solo que exista)
      4. Comprueba el túnel y abre el navegador

.PARAMETER NoAbrir
    No abrir el navegador al terminar.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\scripts\iniciar-docuhub.ps1
#>

[CmdletBinding()]
param(
    [switch]$NoAbrir,
    [int]$EsperaMinutos = 5
)

$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent $PSScriptRoot
$url = 'https://docs.constructorapesam.com'
$local = 'http://localhost:8080'

$Host.UI.RawUI.WindowTitle = 'DocuHub - iniciando'

function Paso($texto) { Write-Host "`n>> $texto" -ForegroundColor Cyan }
function Ok($texto)   { Write-Host "   [ok] $texto" -ForegroundColor Green }
function Aviso($texto){ Write-Host "   [!]  $texto" -ForegroundColor Yellow }
function Falla($texto){ Write-Host "   [X]  $texto" -ForegroundColor Red }

function Fin($codigo) {
    Write-Host ''
    Write-Host 'Pulsa una tecla para cerrar esta ventana...' -ForegroundColor DarkGray
    $null = $Host.UI.RawUI.ReadKey('NoEcho,IncludeKeyDown')
    exit $codigo
}

Clear-Host
Write-Host ''
Write-Host '  ========================================' -ForegroundColor Blue
Write-Host '    DocuHub - Constructora Pesam' -ForegroundColor White
Write-Host '  ========================================' -ForegroundColor Blue

# ------------------------------------------------------------------ Docker --

$docker = 'C:\Program Files\Docker\Docker\resources\bin\docker.exe'
if (-not (Test-Path $docker)) {
    $docker = (Get-Command docker -ErrorAction SilentlyContinue).Source
}
if (-not $docker) {
    Falla 'No se encontró Docker. Ejecuta scripts\setup-windows.ps1 para instalarlo.'
    Fin 1
}

Paso 'Comprobando el motor de Docker'
& $docker info --format '{{.ServerVersion}}' 2>$null | Out-Null
if ($LASTEXITCODE -eq 0) {
    Ok 'ya estaba en marcha'
} else {
    Aviso 'apagado: arrancando Docker Desktop (tarda entre 1 y 3 minutos)'
    $exe = 'C:\Program Files\Docker\Docker\Docker Desktop.exe'
    if (Test-Path $exe) { Start-Process $exe } else { Aviso 'no se encontró Docker Desktop.exe' }

    $limite = (Get-Date).AddMinutes($EsperaMinutos)
    $girando = '|/-\'
    $i = 0
    while ((Get-Date) -lt $limite) {
        Start-Sleep -Seconds 3
        & $docker info --format '{{.ServerVersion}}' 2>$null | Out-Null
        if ($LASTEXITCODE -eq 0) { break }
        Write-Host ("`r   esperando al motor... {0}" -f $girando[$i % 4]) -NoNewline
        $i++
    }
    Write-Host "`r                                   `r" -NoNewline

    & $docker info --format '{{.ServerVersion}}' 2>$null | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Falla "el motor de Docker no respondió en $EsperaMinutos minutos."
        Write-Host '        Abre Docker Desktop a mano y mira si pide alguna confirmación.'
        Fin 1
    }
    Ok 'motor listo'
}

# ------------------------------------------------------------ contenedores --

Paso 'Levantando la plataforma'
Push-Location (Join-Path $root 'deploy')
try {
    $salida = & $docker compose up -d 2>&1
    if ($LASTEXITCODE -ne 0) {
        Falla 'docker compose falló:'
        $salida | ForEach-Object { Write-Host "        $_" }
        Fin 1
    }
    Ok 'contenedores en marcha'
} finally {
    Pop-Location
}

# -------------------------------------------------------------- disponible --

Paso 'Esperando a que la plataforma responda'
$listo = $false
for ($i = 0; $i -lt 40; $i++) {
    Start-Sleep -Seconds 3
    try {
        $r = Invoke-WebRequest -Uri "$local/healthz" -TimeoutSec 5 -UseBasicParsing
        if ($r.StatusCode -eq 200) { $listo = $true; break }
    } catch { }
}
if (-not $listo) {
    Falla 'la plataforma no respondió en 2 minutos.'
    Write-Host '        Mira el detalle con:  cd deploy; docker compose logs app'
    Fin 1
}
Ok 'responde en localhost:8080'

# ------------------------------------------------------------------ túnel --

Paso 'Comprobando el acceso desde internet'
$svc = Get-Service -Name cloudflared -ErrorAction SilentlyContinue
if (-not $svc) {
    Aviso 'el túnel no está instalado: la plataforma solo funciona en esta laptop'
} elseif ($svc.Status -ne 'Running') {
    Aviso 'el túnel estaba parado: intentando iniciarlo'
    try {
        Start-Service cloudflared -ErrorAction Stop
        Start-Sleep -Seconds 5
        Ok 'túnel iniciado'
    } catch {
        Aviso 'no se pudo iniciar (hace falta ser administrador). Ejecuta: Start-Service cloudflared'
    }
}

try {
    $r = Invoke-WebRequest -Uri "$url/healthz" -TimeoutSec 20 -UseBasicParsing
    if ($r.StatusCode -eq 200) { Ok "accesible en $url" }
} catch {
    Aviso "todavía no responde en $url (el túnel tarda unos segundos en reconectar)"
}

# ----------------------------------------------------------------- resumen --

Write-Host ''
Write-Host '  ----------------------------------------' -ForegroundColor DarkGray
Write-Host '   DocuHub está en marcha' -ForegroundColor Green
Write-Host '  ----------------------------------------' -ForegroundColor DarkGray
Write-Host "   Interno : $local"
Write-Host "   Externo : $url"
Write-Host ''
Write-Host '   Mientras esta laptop esté encendida, la plataforma está disponible' -ForegroundColor DarkGray
Write-Host '   para todos. Al apagarla, deja de estarlo.' -ForegroundColor DarkGray

if (-not $NoAbrir) {
    Start-Process $url
}

Write-Host ''
Write-Host '  Esta ventana se cierra sola en 15 segundos.' -ForegroundColor DarkGray
Start-Sleep -Seconds 15
