#Requires -Version 5.1
<#
.SYNOPSIS
    Publica la página de cortesía (Worker de Cloudflare) y su ruta.

.DESCRIPTION
    Acepta dos formas de autenticarse, en este orden:

      1. Un token de API en la variable CLOUDFLARE_API_TOKEN, o en el archivo
         deploy\cloudflare-worker\.token (que Git ignora). Es la vía cómoda:
         no tiene ventana de tiempo y se puede ejecutar cuando se quiera.
      2. La sesión de `wrangler login`, si ya existe.

    Si no encuentra ninguna, explica cómo crear el token en lugar de fallar
    con un error de la herramienta.

    El despliegue crea el Worker y, a la vez, la ruta
    docs.constructorapesam.com/* declarada en wrangler.toml.

.EXAMPLE
    .\scripts\desplegar-cortesia.ps1
#>

[CmdletBinding()]
param(
    [string]$Token
)

$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent $PSScriptRoot
$dir = Join-Path $root 'deploy\cloudflare-worker'

function Paso($t) { Write-Host "`n>> $t" -ForegroundColor Cyan }
function Ok($t)   { Write-Host "   [ok] $t" -ForegroundColor Green }
function Falla($t){ Write-Host "   [X]  $t" -ForegroundColor Red }

if (-not (Test-Path (Join-Path $dir 'cortesia.js'))) {
    Falla "no se encontró $dir\cortesia.js"
    exit 1
}

# --------------------------------------------------------------- credencial --

if (-not $Token) { $Token = $env:CLOUDFLARE_API_TOKEN }
if (-not $Token) {
    $archivo = Join-Path $dir '.token'
    if (Test-Path $archivo) {
        $Token = (Get-Content $archivo -Raw).Trim()
    }
}

$haySesion = $false
if (-not $Token) {
    Paso 'Buscando una sesión de wrangler'
    $quien = & npx --yes wrangler@latest whoami 2>&1 | Out-String
    $haySesion = ($quien -notmatch 'not authenticated')
    if ($haySesion) { Ok 'sesión encontrada' }
}

if (-not $Token -and -not $haySesion) {
    Write-Host ''
    Write-Host '  Falta la credencial de Cloudflare.' -ForegroundColor Yellow
    Write-Host ''
    Write-Host '  Crea un token de API (2 minutos, no caduca mientras no lo borres):'
    Write-Host ''
    Write-Host '   1. https://dash.cloudflare.com/profile/api-tokens'
    Write-Host '   2. Crear token  ->  plantilla "Editar workers de Cloudflare"  ->  Usar plantilla'
    Write-Host '   3. En "Recursos de zona", elige constructorapesam.com'
    Write-Host '   4. Continuar  ->  Crear token  ->  copia el valor'
    Write-Host ''
    Write-Host "   5. Pégalo en este archivo y vuelve a ejecutar este script:"
    Write-Host "      $dir\.token" -ForegroundColor White
    Write-Host ''
    Write-Host '      (Git ignora ese archivo: no se sube a ningún sitio)' -ForegroundColor DarkGray
    Write-Host ''
    Write-Host '   Alternativa sin token:  npx wrangler login'
    Write-Host '   — abre el navegador y hay que autorizar en menos de 2 minutos.' -ForegroundColor DarkGray
    Write-Host ''
    exit 1
}

# ---------------------------------------------------------------- despliegue --

Push-Location $dir
try {
    if ($Token) {
        $env:CLOUDFLARE_API_TOKEN = $Token
        Ok 'usando token de API'
    }

    Paso 'Publicando el Worker y su ruta'
    $salida = & npx --yes wrangler@latest deploy 2>&1
    $salida | ForEach-Object { Write-Host "   $_" -ForegroundColor DarkGray }

    if ($LASTEXITCODE -ne 0) {
        Falla 'el despliegue falló (ver el detalle arriba)'
        exit 1
    }
    Ok 'Worker publicado'
} finally {
    $env:CLOUDFLARE_API_TOKEN = $null
    Pop-Location
}

# --------------------------------------------------------------- comprobación --

Paso 'Comprobando que no rompió nada'
try {
    $r = Invoke-WebRequest -Uri 'https://docs.constructorapesam.com/healthz' -TimeoutSec 20 -UseBasicParsing
    if ($r.StatusCode -eq 200) {
        Ok 'la plataforma sigue respondiendo con normalidad'
    }
} catch {
    Falla 'la plataforma no responde. Si no vuelve en un minuto, quita la ruta del Worker'
    Write-Host '        en el panel de Cloudflare y el tráfico volverá directo al túnel.'
    exit 1
}

Write-Host ''
Write-Host '  Listo. Para verla en acción: detén DocuHub y abre' -ForegroundColor Green
Write-Host '  https://docs.constructorapesam.com en el navegador.' -ForegroundColor Green
Write-Host ''
