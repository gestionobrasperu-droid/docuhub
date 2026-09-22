#Requires -Version 5.1
<#
.SYNOPSIS
    Sube el proyecto a GitHub (github.com/gestionobrasperu-droid/docuhub).

.DESCRIPTION
    Hace la autenticación, crea el repositorio si no existe y sube la rama
    main. Es idempotente: si ya está todo hecho, solo sube lo nuevo.

    El repositorio se crea PRIVADO. DocuHub es infraestructura interna de la
    empresa; aunque el .gitignore excluye el .env, un repositorio privado
    evita que un descuido futuro exponga algo.

.PARAMETER Public
    Crea el repositorio como público en lugar de privado.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\scripts\push-github.ps1
#>

[CmdletBinding()]
param(
    [string]$Owner = 'gestionobrasperu-droid',
    [string]$Name = 'docuhub',
    [switch]$Public
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

function Write-Step($t) { Write-Host "`n=== $t ===" -ForegroundColor Cyan }
function Write-Ok($t)   { Write-Host "  [ok] $t" -ForegroundColor Green }

# Una terminal abierta ANTES de instalar Git o gh conserva el PATH viejo y no
# los encuentra. Se recarga desde el registro para no depender de reiniciar.
$env:Path = [System.Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' +
            [System.Environment]::GetEnvironmentVariable('Path', 'User')

# ----------------------------------------------------------- comprobaciones --

Write-Step 'Comprobando herramientas'
foreach ($cmd in @('git', 'gh')) {
    if (-not (Get-Command $cmd -ErrorAction SilentlyContinue)) {
        throw "Falta $cmd. Ejecuta primero scripts\setup-windows.ps1 y abre una terminal nueva."
    }
}
Write-Ok 'git y gh disponibles'

# Red de seguridad: que no se cuele el archivo de secretos.
if (git ls-files --error-unmatch 'deploy/.env' 2>$null) {
    throw 'deploy/.env está siendo versionado. Quítalo con: git rm --cached deploy/.env'
}
Write-Ok 'deploy\.env no está en el repositorio'

# ------------------------------------------------------------ autenticación --

Write-Step 'Autenticación en GitHub'
gh auth status 2>$null | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Host '  No hay sesión. Se abrirá el navegador para autorizar.' -ForegroundColor Yellow
    Write-Host '  Elige: GitHub.com  ->  HTTPS  ->  Login with a web browser' -ForegroundColor Yellow
    gh auth login --hostname github.com --git-protocol https --web
    if ($LASTEXITCODE -ne 0) { throw 'La autenticación no se completó.' }
}
$account = (gh api user --jq '.login' 2>$null)
Write-Ok "Autenticado como $account"

# Deja que gh gestione las credenciales de git: así el push no pide usuario.
gh auth setup-git 2>$null | Out-Null

# -------------------------------------------------------------- repositorio --

Write-Step 'Repositorio remoto'
$full = "$Owner/$Name"
$visibility = if ($Public) { '--public' } else { '--private' }

gh repo view $full 2>$null | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Host "  Creando $full…"
    gh repo create $full $visibility `
        --description 'Plataforma de gestion documental sobre Google Drive con control de accesos, cuotas y auditoria' `
        --disable-wiki
    if ($LASTEXITCODE -ne 0) { throw "No se pudo crear el repositorio $full" }
    Write-Ok "Creado $full"
} else {
    Write-Ok "$full ya existe"
}

$remoteUrl = "https://github.com/$full.git"
$existing = git remote get-url origin 2>$null
if ($LASTEXITCODE -ne 0) {
    git remote add origin $remoteUrl
    Write-Ok "Remoto origin -> $remoteUrl"
} elseif ($existing -ne $remoteUrl) {
    git remote set-url origin $remoteUrl
    Write-Ok "Remoto origin actualizado -> $remoteUrl"
} else {
    Write-Ok 'Remoto origin ya apuntaba al destino correcto'
}

# --------------------------------------------------------------------- push --

Write-Step 'Subiendo'
$branch = git rev-parse --abbrev-ref HEAD
git push -u origin $branch
if ($LASTEXITCODE -ne 0) { throw 'El push falló. Revisa el mensaje de arriba.' }

Write-Host ''
Write-Ok "Listo: https://github.com/$full"
Write-Host @"

A partir de ahora, para subir cambios:
    git add -A
    git commit -m "que cambiaste"
    git push

Recuerda: deploy\.env NUNCA se sube. Guarda una copia de APP_ENCRYPTION_KEY
en el gestor de contraseñas de la empresa, no en el repositorio.
"@ -ForegroundColor Cyan
