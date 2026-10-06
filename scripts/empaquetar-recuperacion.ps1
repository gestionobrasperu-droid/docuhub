#Requires -Version 5.1
<#
.SYNOPSIS
    Empaqueta y cifra todo lo que hace falta para revivir DocuHub en otro equipo.

.DESCRIPTION
    El repositorio tiene el codigo, pero no las llaves: estan fuera a proposito,
    porque lo que entra en Git se queda en su historial para siempre y se filtra
    entero si el repositorio cambia de visibilidad o entra alguien nuevo.

    Este script reune en un solo archivo cifrado lo que el codigo no puede
    reconstruir:

      deploy\.env                        claves de cifrado, base de datos y Google
      %USERPROFILE%\.cloudflared\*.yml   configuracion del tunel
      %USERPROFILE%\.cloudflared\*.json  credenciales del tunel

    El resultado, recuperacion.enc, va cifrado con AES-256 y una contrasena que
    eliges tu. Ese archivo SI se puede subir al repositorio: sin la contrasena
    no es mas que ruido.

    Guarda la contrasena en el gestor de contrasenas de la empresa, NO en el
    repositorio. Si la pierdes, el paquete no sirve para nada.

.PARAMETER Salida
    Donde dejar el paquete. Por defecto, deploy\recuperacion.enc

.EXAMPLE
    .\scripts\empaquetar-recuperacion.ps1
#>

[CmdletBinding()]
param(
    [string]$Salida
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
if (-not $Salida) { $Salida = Join-Path $root 'deploy\recuperacion.enc' }

function Ok($t)    { Write-Host "  [ok] $t" -ForegroundColor Green }
function Aviso($t) { Write-Host "  [!]  $t" -ForegroundColor Yellow }

Write-Host ''
Write-Host '=== Paquete de recuperacion de DocuHub ===' -ForegroundColor Cyan
Write-Host ''

# --------------------------------------------------------------- reunir ----

$temp = Join-Path $env:TEMP ("docuhub-rec-" + (Get-Date -Format 'yyyyMMddHHmmss'))
New-Item -ItemType Directory -Path $temp -Force | Out-Null

$encontrados = 0

$env_ = Join-Path $root 'deploy\.env'
if (Test-Path $env_) {
    Copy-Item $env_ (Join-Path $temp 'env.txt')
    Ok 'deploy\.env'
    $encontrados++
} else {
    Aviso 'no se encontro deploy\.env'
}

$cf = Join-Path $env:USERPROFILE '.cloudflared'
if (Test-Path $cf) {
    $destCf = Join-Path $temp 'cloudflared'
    New-Item -ItemType Directory -Path $destCf -Force | Out-Null
    foreach ($patron in @('*.yml', '*.json', '*.pem')) {
        foreach ($f in (Get-ChildItem -Path $cf -Filter $patron -File -ErrorAction SilentlyContinue)) {
            Copy-Item $f.FullName (Join-Path $destCf $f.Name)
            Ok "cloudflared\$($f.Name)"
            $encontrados++
        }
    }
} else {
    Aviso 'no se encontro la carpeta .cloudflared'
}

if ($encontrados -eq 0) {
    Remove-Item $temp -Recurse -Force
    throw 'No hay nada que empaquetar.'
}

# Una nota dentro del paquete, para quien lo abra dentro de dos anos.
$nota = @"
Paquete de recuperacion de DocuHub
Generado el $(Get-Date -Format 'yyyy-MM-dd HH:mm')

Contiene lo que el repositorio no puede reconstruir:

  env.txt              -> va en deploy\.env
  cloudflared\*        -> van en %USERPROFILE%\.cloudflared\

Para revivir la plataforma en otro equipo:
  1. git clone https://github.com/gestionobrasperu-droid/docuhub
  2. Coloca los archivos de este paquete en su sitio (arriba)
  3. Restaura el ultimo respaldo de backups\ (ver docs/04-OPERACION.md)
  4. cd deploy; docker compose up -d --build

APP_ENCRYPTION_KEY es la pieza critica: sin ella, los tokens de Google
guardados en la base son ilegibles y hay que volver a conectar la cuenta.
"@
Set-Content -Path (Join-Path $temp 'LEEME.txt') -Value $nota -Encoding utf8

# ---------------------------------------------------------------- cifrar ---

$zip = "$temp.zip"
Compress-Archive -Path (Join-Path $temp '*') -DestinationPath $zip -Force
Remove-Item $temp -Recurse -Force

Write-Host ''
Write-Host '  Elige una contrasena para el paquete.' -ForegroundColor Cyan
Write-Host '  Guardala en el gestor de contrasenas de la empresa: sin ella el' -ForegroundColor DarkGray
Write-Host '  paquete no se puede abrir, y ese es justamente el punto.' -ForegroundColor DarkGray
Write-Host ''
$clave1 = Read-Host '  Contrasena' -AsSecureString
$clave2 = Read-Host '  Repitela  ' -AsSecureString

$c1 = [Runtime.InteropServices.Marshal]::PtrToStringAuto([Runtime.InteropServices.Marshal]::SecureStringToBSTR($clave1))
$c2 = [Runtime.InteropServices.Marshal]::PtrToStringAuto([Runtime.InteropServices.Marshal]::SecureStringToBSTR($clave2))

if ($c1 -ne $c2) { Remove-Item $zip -Force; throw 'Las contrasenas no coinciden.' }
if ($c1.Length -lt 10) { Remove-Item $zip -Force; throw 'Usa al menos 10 caracteres.' }

# AES-256 con clave derivada por PBKDF2. La sal y el vector de inicializacion
# viajan al principio del archivo, que es lo normal: no son secretos, solo
# evitan que dos paquetes con la misma contrasena se cifren igual.
$sal = New-Object byte[] 16
[System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($sal)

$derivada = New-Object System.Security.Cryptography.Rfc2898DeriveBytes($c1, $sal, 200000)
$aes = [System.Security.Cryptography.Aes]::Create()
$aes.KeySize = 256
$aes.Key = $derivada.GetBytes(32)
$aes.GenerateIV()

$datos = [System.IO.File]::ReadAllBytes($zip)
$cifrador = $aes.CreateEncryptor()
$cifrado = $cifrador.TransformFinalBlock($datos, 0, $datos.Length)

$salidaBytes = New-Object System.IO.MemoryStream
$salidaBytes.Write([System.Text.Encoding]::ASCII.GetBytes('DOCUHUB1'), 0, 8)
$salidaBytes.Write($sal, 0, 16)
$salidaBytes.Write($aes.IV, 0, 16)
$salidaBytes.Write($cifrado, 0, $cifrado.Length)
[System.IO.File]::WriteAllBytes($Salida, $salidaBytes.ToArray())

$aes.Dispose()
Remove-Item $zip -Force

$kb = [math]::Round((Get-Item $Salida).Length / 1KB, 1)
Write-Host ''
Ok "$Salida ($kb KB)"
Write-Host ''
Write-Host '  Ya puedes subirlo:' -ForegroundColor Cyan
Write-Host '     git add deploy/recuperacion.enc'
Write-Host '     git commit -m "Paquete de recuperacion cifrado"'
Write-Host '     git push'
Write-Host ''
Write-Host '  Para abrirlo cuando haga falta:' -ForegroundColor Cyan
Write-Host '     .\scripts\abrir-recuperacion.ps1'
Write-Host ''
