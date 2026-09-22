#Requires -Version 5.1
<#
.SYNOPSIS
    Prepara la laptop para ejecutar DocuHub: instala lo necesario y genera el
    archivo .env con claves seguras.

.DESCRIPTION
    Es idempotente: si algo ya está instalado, lo salta. No sobrescribe un .env
    existente (para no perder las claves, que dejarían ilegibles los tokens de
    Google guardados).

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\scripts\setup-windows.ps1
#>

[CmdletBinding()]
param(
    [switch]$SkipDocker,
    [string]$BaseUrl = 'https://docs.constructorapesam.com'
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot

function Write-Step($text) { Write-Host "`n=== $text ===" -ForegroundColor Cyan }
function Write-Ok($text)   { Write-Host "  [ok] $text" -ForegroundColor Green }
function Write-Warn2($text){ Write-Host "  [!]  $text" -ForegroundColor Yellow }

# Una terminal abierta antes de instalar algo conserva el PATH viejo y no
# encuentra lo recién instalado. Se recarga desde el registro.
function Sync-Path {
    $env:Path = [System.Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' +
                [System.Environment]::GetEnvironmentVariable('Path', 'User')
}
Sync-Path

function Test-Command($name) {
    $null -ne (Get-Command $name -ErrorAction SilentlyContinue)
}

function Install-IfMissing($command, $wingetId, $label) {
    if (Test-Command $command) {
        Write-Ok "$label ya está instalado"
        return
    }
    Write-Host "  Instalando $label…"
    winget install --id $wingetId -e --accept-package-agreements --accept-source-agreements --silent --disable-interactivity
    $code = $LASTEXITCODE
    Sync-Path

    # winget no distingue "instalado" de "ya lo tenías" en el código de salida:
    # -1978335189 (0x8A15002B) y 43 significan "no hay actualización aplicable",
    # que en la práctica quiere decir que el paquete ya estaba puesto.
    if ($code -eq 0) {
        Write-Ok "$label instalado"
    } elseif ($code -eq -1978335189 -or $code -eq 43 -or (Test-Command $command)) {
        Write-Ok "$label ya estaba instalado"
    } else {
        Write-Warn2 "No se pudo instalar $label automáticamente (código $code). Instálalo a mano."
    }
}

# ---------------------------------------------------------------- requisitos --

Write-Step 'Comprobando requisitos'

if (-not (Test-Command 'winget')) {
    throw 'winget no está disponible. Actualiza "Instalador de aplicaciones" desde la Microsoft Store.'
}

Install-IfMissing 'git'         'Git.Git'              'Git'
Install-IfMissing 'gh'          'GitHub.cli'           'GitHub CLI'
Install-IfMissing 'node'        'OpenJS.NodeJS.LTS'    'Node.js LTS'
Install-IfMissing 'go'          'GoLang.Go'            'Go'
Install-IfMissing 'cloudflared' 'Cloudflare.cloudflared' 'cloudflared'

if (-not $SkipDocker) {
    Install-IfMissing 'docker' 'Docker.DockerDesktop' 'Docker Desktop'

    # Docker en Windows corre sobre WSL2. Si falta, el servicio arranca igual
    # y todo comando falla con un error 500 del API, que despista mucho.
    # enable-docker.ps1 (como administrador) lo deja resuelto.
    $wslOk = $false
    try {
        & wsl.exe --status 2>&1 | Out-Null
        $wslOk = ($LASTEXITCODE -eq 0)
    } catch { }

    if (-not $wslOk) {
        Write-Warn2 'Falta WSL2, que es el motor de Docker en Windows. Ejecuta como administrador:'
        Write-Warn2 '    .\scripts\enable-docker.ps1'
    } else {
        Write-Ok 'WSL2 disponible'
    }
}

# ------------------------------------------------------------------- .env ---

Write-Step 'Configuración (.env)'

$envPath = Join-Path $root 'deploy\.env'
$examplePath = Join-Path $root 'deploy\.env.example'

if (Test-Path $envPath) {
    Write-Ok '.env ya existe — no se toca (contiene las claves en uso)'
} else {
    if (-not (Test-Path $examplePath)) { throw "No se encontró $examplePath" }

    # Clave de 32 bytes en hexadecimal para AES-256-GCM.
    $bytes = New-Object byte[] 32
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
    $encKey = -join ($bytes | ForEach-Object { '{0:x2}' -f $_ })

    # Contraseña de Postgres: 24 bytes en base64 sin caracteres problemáticos.
    $pwBytes = New-Object byte[] 24
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($pwBytes)
    $dbPass = [Convert]::ToBase64String($pwBytes) -replace '[+/=]', 'x'

    $content = Get-Content $examplePath -Raw
    $content = $content -replace 'APP_ENCRYPTION_KEY=', "APP_ENCRYPTION_KEY=$encKey"
    $content = $content -replace 'POSTGRES_PASSWORD=', "POSTGRES_PASSWORD=$dbPass"
    $content = $content -replace 'APP_BASE_URL=.*', "APP_BASE_URL=$BaseUrl"
    Set-Content -Path $envPath -Value $content -Encoding utf8

    Write-Ok "Creado deploy\.env con claves nuevas"
    Write-Warn2 'Guarda una copia de APP_ENCRYPTION_KEY en un lugar seguro:'
    Write-Warn2 'si se pierde, hay que volver a conectar la cuenta de Google.'
}

Write-Host ''
Write-Host 'Falta completar a mano en deploy\.env:' -ForegroundColor Yellow
Write-Host '  GOOGLE_CLIENT_ID y GOOGLE_CLIENT_SECRET  -> docs\02-GOOGLE-DRIVE-SETUP.md'
Write-Host '  CLOUDFLARE_TUNNEL_TOKEN (si usas el túnel en contenedor)'

# -------------------------------------------------------------- siguiente ---

Write-Step 'Siguientes pasos'
Write-Host @"
  1. Completa deploy\.env con las credenciales de Google.
  2. Levanta la plataforma:
        cd deploy
        docker compose up -d --build
  3. Mira el log para ver la contraseña del administrador:
        docker compose logs app
  4. Abre http://localhost:8080 y conecta Google Drive.
  5. Para que la laptop no se apague nunca:
        .\scripts\keep-alive.ps1          (como administrador)
        .\scripts\install-tasks.ps1       (como administrador)
  6. Para publicarlo en el dominio:
        .\scripts\setup-tunnel.ps1
"@
