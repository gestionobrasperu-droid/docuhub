#Requires -Version 5.1
<#
.SYNOPSIS
    Respaldo de la base de datos de DocuHub.

.DESCRIPTION
    Los archivos ya están a salvo en Google Drive. Lo que hay que respaldar es
    la base: usuarios, permisos, enlaces, bitácora y —lo más importante— la
    relación entre cada archivo y su id en Drive. Sin eso, los archivos siguen
    existiendo pero la plataforma no sabría de quién son ni quién puede verlos.

    Hace un pg_dump comprimido dentro del contenedor, lo guarda en backups\ y
    borra los más antiguos.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\scripts\backup.ps1 -KeepDays 30
#>

[CmdletBinding()]
param(
    [int]$KeepDays = 30,
    [string]$OutputDir
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
if (-not $OutputDir) { $OutputDir = Join-Path $root 'backups' }
if (-not (Test-Path $OutputDir)) { New-Item -ItemType Directory -Path $OutputDir -Force | Out-Null }

$stamp = Get-Date -Format 'yyyy-MM-dd_HHmm'
$outFile = Join-Path $OutputDir "docuhub_$stamp.sql"

Push-Location (Join-Path $root 'deploy')
try {
    # Lee el usuario/base del .env para no depender de valores fijos.
    $envFile = Join-Path (Get-Location) '.env'
    $dbUser = 'docuhub'
    $dbName = 'docuhub'
    if (Test-Path $envFile) {
        foreach ($line in Get-Content $envFile) {
            if ($line -match '^\s*POSTGRES_USER\s*=\s*(.+)$') { $dbUser = $Matches[1].Trim() }
            if ($line -match '^\s*POSTGRES_DB\s*=\s*(.+)$')   { $dbName = $Matches[1].Trim() }
        }
    }

    Write-Host "Respaldando la base $dbName…"
    docker compose exec -T db pg_dump -U $dbUser -d $dbName --clean --if-exists |
        Out-File -FilePath $outFile -Encoding utf8

    if ($LASTEXITCODE -ne 0) { throw "pg_dump falló con código $LASTEXITCODE" }

    $size = (Get-Item $outFile).Length
    if ($size -lt 1KB) { throw "El respaldo salió vacío ($size bytes): revisa que el contenedor db esté arriba." }

    # Comprimir: un dump de texto se reduce a la décima parte.
    $zipFile = "$outFile.zip"
    Compress-Archive -Path $outFile -DestinationPath $zipFile -Force
    Remove-Item $outFile -Force

    $zipSize = [math]::Round((Get-Item $zipFile).Length / 1MB, 2)
    Write-Host "  [ok] $zipFile ($zipSize MB)" -ForegroundColor Green
} finally {
    Pop-Location
}

# Limpieza de respaldos viejos.
$cutoff = (Get-Date).AddDays(-$KeepDays)
$old = Get-ChildItem -Path $OutputDir -Filter 'docuhub_*.zip' | Where-Object { $_.LastWriteTime -lt $cutoff }
foreach ($f in $old) {
    Remove-Item $f.FullName -Force
    Write-Host "  eliminado respaldo antiguo: $($f.Name)"
}

Write-Host @"

Recomendación: copia la carpeta backups\ fuera de la laptop (un disco externo,
o la misma cuenta de Google Drive en una carpeta distinta a DocuHub).
Un respaldo que vive solo en el equipo que puede fallar no es un respaldo.

Para restaurar:
  Expand-Archive backups\docuhub_FECHA.sql.zip -DestinationPath .
  Get-Content docuhub_FECHA.sql | docker compose exec -T db psql -U $dbUser -d $dbName
"@ -ForegroundColor Cyan
