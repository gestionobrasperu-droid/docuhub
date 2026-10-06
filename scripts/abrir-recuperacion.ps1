#Requires -Version 5.1
<#
.SYNOPSIS
    Abre el paquete de recuperacion cifrado.

.DESCRIPTION
    El reverso de empaquetar-recuperacion.ps1. Pide la contrasena, descifra el
    paquete y deja los archivos en una carpeta para que los coloques en su
    sitio.

    No los coloca solo a proposito: sobrescribir un .env que ya existe, en un
    equipo que quiza ya esta funcionando, es la clase de automatismo que acaba
    mal. El script te dice exactamente donde va cada cosa.

.PARAMETER Paquete
    Archivo a abrir. Por defecto, deploy\recuperacion.enc

.PARAMETER Destino
    Donde dejar lo descifrado. Por defecto, una carpeta nueva en el escritorio.

.EXAMPLE
    .\scripts\abrir-recuperacion.ps1
#>

[CmdletBinding()]
param(
    [string]$Paquete,
    [string]$Destino
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
if (-not $Paquete) { $Paquete = Join-Path $root 'deploy\recuperacion.enc' }
if (-not $Destino) {
    $Destino = Join-Path ([Environment]::GetFolderPath('Desktop')) ('DocuHub-recuperacion-' + (Get-Date -Format 'yyyyMMdd-HHmm'))
}

if (-not (Test-Path $Paquete)) {
    throw "No se encontro $Paquete"
}

Write-Host ''
Write-Host '=== Abrir el paquete de recuperacion ===' -ForegroundColor Cyan
Write-Host ''

$bytes = [System.IO.File]::ReadAllBytes($Paquete)
if ($bytes.Length -lt 40) { throw 'El archivo esta incompleto.' }

$marca = [System.Text.Encoding]::ASCII.GetString($bytes, 0, 8)
if ($marca -ne 'DOCUHUB1') {
    throw 'Ese archivo no es un paquete de recuperacion de DocuHub.'
}

$sal = $bytes[8..23]
$iv = $bytes[24..39]
$cifrado = $bytes[40..($bytes.Length - 1)]

$clave = Read-Host '  Contrasena del paquete' -AsSecureString
$c = [Runtime.InteropServices.Marshal]::PtrToStringAuto([Runtime.InteropServices.Marshal]::SecureStringToBSTR($clave))

$derivada = New-Object System.Security.Cryptography.Rfc2898DeriveBytes($c, $sal, 200000)
$aes = [System.Security.Cryptography.Aes]::Create()
$aes.KeySize = 256
$aes.Key = $derivada.GetBytes(32)
$aes.IV = $iv

try {
    $descifrador = $aes.CreateDecryptor()
    $plano = $descifrador.TransformFinalBlock($cifrado, 0, $cifrado.Length)
} catch {
    $aes.Dispose()
    throw 'Contrasena incorrecta, o el archivo esta danado.'
}
$aes.Dispose()

$zip = Join-Path $env:TEMP ('docuhub-rec-' + (Get-Date -Format 'HHmmss') + '.zip')
[System.IO.File]::WriteAllBytes($zip, $plano)

New-Item -ItemType Directory -Path $Destino -Force | Out-Null
Expand-Archive -Path $zip -DestinationPath $Destino -Force
Remove-Item $zip -Force

Write-Host ''
Write-Host "  [ok] descifrado en: $Destino" -ForegroundColor Green
Write-Host ''
Write-Host '  Donde va cada cosa:' -ForegroundColor Cyan
Write-Host '    env.txt         ->  deploy\.env  (del repositorio clonado)'
Write-Host '    cloudflared\*   ->  %USERPROFILE%\.cloudflared\'
Write-Host ''
Write-Host '  Despues:  cd deploy; docker compose up -d --build' -ForegroundColor DarkGray
Write-Host ''
Write-Host '  Borra esa carpeta cuando termines: contiene las claves en claro.' -ForegroundColor Yellow
Write-Host ''
