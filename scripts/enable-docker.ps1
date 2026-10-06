#Requires -Version 5.1
<#
.SYNOPSIS
    Deja Docker Desktop listo para trabajar 24/7 en esta laptop.

.DESCRIPTION
    Docker Desktop en Windows necesita WSL2 como motor. Si WSL no está
    instalado, el backend de Docker arranca igual y el servicio figura como
    activo, pero cualquier comando falla con un error 500 del API — un fallo
    que despista bastante.

    Este script, ejecutado como administrador:
      1. Habilita las características de Windows que WSL2 necesita
      2. Instala WSL sin distribución (Docker trae la suya)
      3. Pone el servicio de Docker en arranque automático
      4. Deja Docker Desktop arrancando con el equipo

    Tras instalar WSL por primera vez, Windows pide reiniciar. Es normal y
    solo pasa una vez.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\scripts\enable-docker.ps1
#>

[CmdletBinding()]
param(
    [string]$LogFile = "$env:TEMP\docuhub-enable-docker.log"
)

$ErrorActionPreference = 'Continue'

function Log($text) {
    $line = '{0}  {1}' -f (Get-Date -Format 'HH:mm:ss'), $text
    Add-Content -Path $LogFile -Value $line -Encoding utf8
    Write-Host $line
}

$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
    ).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
    throw 'Este script necesita PowerShell como administrador.'
}

Set-Content -Path $LogFile -Value '' -Encoding utf8
Log '=== Preparando Docker ==='

# --- 1. Características de Windows -----------------------------------------
$needsReboot = $false
foreach ($feature in @('VirtualMachinePlatform', 'Microsoft-Windows-Subsystem-Linux')) {
    $state = (Get-WindowsOptionalFeature -Online -FeatureName $feature -ErrorAction SilentlyContinue).State
    if ($state -eq 'Enabled') {
        Log "$feature ya estaba habilitada"
        continue
    }
    Log "Habilitando $feature..."
    $res = Enable-WindowsOptionalFeature -Online -FeatureName $feature -All -NoRestart -ErrorAction SilentlyContinue
    if ($res.RestartNeeded) { $needsReboot = $true }
    Log "$feature habilitada"
}

# --- 2. WSL ------------------------------------------------------------------
# --no-distribution: Docker Desktop crea sus propias distros (docker-desktop),
# no hace falta Ubuntu ni ninguna otra.
Log 'Instalando WSL (sin distribucion)...'
& wsl.exe --install --no-distribution
Log "wsl --install termino con codigo $LASTEXITCODE"

& wsl.exe --update
Log "wsl --update termino con codigo $LASTEXITCODE"

# --- 3. Servicio de Docker ---------------------------------------------------
$svc = Get-Service -Name 'com.docker.service' -ErrorAction SilentlyContinue
if ($svc) {
    Set-Service -Name 'com.docker.service' -StartupType Automatic
    Log 'Servicio com.docker.service -> arranque automatico'
    if ($svc.Status -ne 'Running' -and -not $needsReboot) {
        try {
            Start-Service -Name 'com.docker.service' -ErrorAction Stop
            Log 'Servicio iniciado'
        } catch {
            Log "No se pudo iniciar el servicio ahora: $($_.Exception.Message)"
        }
    }
} else {
    Log 'AVISO: no se encontro com.docker.service. Reinstala Docker Desktop.'
}

# --- 4. Arranque con el equipo -----------------------------------------------
# En un equipo que hace de servidor, Docker Desktop debe subir solo.
$run = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run'
$exe = 'C:\Program Files\Docker\Docker\Docker Desktop.exe'
if (Test-Path $exe) {
    Set-ItemProperty -Path $run -Name 'DockerDesktop' -Value "`"$exe`" -Autostart" -ErrorAction SilentlyContinue
    Log 'Docker Desktop arrancara con el equipo'
}

if ($needsReboot) {
    Log 'RESULTADO: REINICIO NECESARIO'
} else {
    Log 'RESULTADO: LISTO'
}
