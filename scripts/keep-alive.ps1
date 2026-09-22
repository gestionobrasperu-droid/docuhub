#Requires -Version 5.1
<#
.SYNOPSIS
    Configura Windows para que la laptop funcione como servidor 24/7.

.DESCRIPTION
    Ejecutar UNA VEZ como administrador. Aplica:
      - nunca suspender ni hibernar estando enchufada
      - cerrar la tapa no apaga nada
      - el disco no se duerme
      - horas activas amplias para que Windows Update no reinicie de noche
      - la pantalla sí se apaga (ahorra energía y no afecta al servicio)

    La pantalla apagada es intencional: reduce el consumo de ~8 W y alarga la
    vida del panel. Los servicios siguen corriendo igual.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\scripts\keep-alive.ps1
#>

[CmdletBinding()]
param(
    [int]$MonitorTimeoutMinutes = 10
)

$ErrorActionPreference = 'Stop'

$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
    ).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
    throw 'Este script necesita PowerShell como administrador (clic derecho -> Ejecutar como administrador).'
}

function Write-Ok($t) { Write-Host "  [ok] $t" -ForegroundColor Green }

Write-Host "`n=== Energía ===" -ForegroundColor Cyan

powercfg /change standby-timeout-ac 0
Write-Ok 'No se suspende estando enchufada'

powercfg /change hibernate-timeout-ac 0
Write-Ok 'No hiberna estando enchufada'

powercfg /change disk-timeout-ac 0
Write-Ok 'Los discos no se duermen'

powercfg /change monitor-timeout-ac $MonitorTimeoutMinutes
Write-Ok "La pantalla se apaga tras $MonitorTimeoutMinutes minutos"

# Cerrar la tapa: 0 = no hacer nada. Es lo que permite dejarla cerrada en un
# rincón ventilado sin cortar el servicio.
powercfg /setacvalueindex SCHEME_CURRENT 4f971e89-eebd-4455-a8de-9e59040e7347 5ca83367-6e45-459f-a27b-476b1d01c936 0
powercfg /setactive SCHEME_CURRENT
Write-Ok 'Cerrar la tapa ya no suspende el equipo'

# Hibernación completamente fuera: libera varios GB y evita estados raros
# tras un corte de energía.
powercfg /hibernate off
Write-Ok 'Hibernación desactivada (libera espacio en disco)'

Write-Host "`n=== Windows Update ===" -ForegroundColor Cyan

$uxPath = 'HKLM:\SOFTWARE\Microsoft\WindowsUpdate\UX\Settings'
if (-not (Test-Path $uxPath)) { New-Item -Path $uxPath -Force | Out-Null }
Set-ItemProperty -Path $uxPath -Name 'ActiveHoursStart' -Value 6 -Type DWord
Set-ItemProperty -Path $uxPath -Name 'ActiveHoursEnd' -Value 23 -Type DWord
Write-Ok 'Horas activas 06:00–23:00: Windows no reiniciará dentro de esa franja'

Write-Host "`n=== Estado de la batería ===" -ForegroundColor Cyan
try {
    $bat = Get-CimInstance -ClassName Win32_Battery -ErrorAction Stop
    if ($bat) {
        Write-Host "  Carga actual: $($bat.EstimatedChargeRemaining)%"
        Write-Host "  Estado: $($bat.BatteryStatus) (2 = conectada a la red)"
    }
} catch {
    Write-Host '  Sin batería detectada (equipo de escritorio o batería retirada).'
}

Write-Host @"

--------------------------------------------------------------------
Falta un paso que solo se hace desde la BIOS (F10 al encender, en HP):

  1. Power -> "Restore on AC Power Loss"  ->  Power On
     Para que arranque sola cuando vuelva la luz tras un corte largo.

  2. Advanced -> Built-in Device Options -> HP Battery Health Manager
     ->  "Maximize my battery health"
     Limita la carga al ~80%. Sin esto, una batería enchufada al 100%
     todo el día se hincha en uno o dos años.
--------------------------------------------------------------------
"@ -ForegroundColor Yellow
