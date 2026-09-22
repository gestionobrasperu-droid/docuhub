#Requires -Version 5.1
<#
.SYNOPSIS
    Registra en el Programador de tareas de Windows todo lo que debe ocurrir
    solo: arranque, watchdog y respaldo diario.

.DESCRIPTION
    Ejecutar como administrador. Crea tres tareas:

      DocuHub-Arranque   al encender la laptop, levanta los contenedores
      DocuHub-Watchdog   cada 5 minutos, comprueba y recupera el servicio
      DocuHub-Respaldo   cada día a las 03:15, respalda la base

    Las tareas corren como SYSTEM, así funcionan aunque nadie inicie sesión.
    Excepción: Docker Desktop necesita una sesión de usuario activa; si usas
    Docker Desktop en vez de Docker Engine en WSL2, activa el inicio de sesión
    automático de Windows o deja la sesión abierta.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\scripts\install-tasks.ps1
#>

[CmdletBinding()]
param(
    [switch]$Remove
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot

$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
    ).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
    throw 'Este script necesita PowerShell como administrador.'
}

$tasks = @('DocuHub-Arranque', 'DocuHub-Watchdog', 'DocuHub-Respaldo')

if ($Remove) {
    foreach ($t in $tasks) {
        if (Get-ScheduledTask -TaskName $t -ErrorAction SilentlyContinue) {
            Unregister-ScheduledTask -TaskName $t -Confirm:$false
            Write-Host "  eliminada: $t" -ForegroundColor Yellow
        }
    }
    return
}

function Register-DocuHubTask {
    param(
        [string]$Name,
        [string]$Arguments,
        $Trigger,
        [string]$Description
    )

    if (Get-ScheduledTask -TaskName $Name -ErrorAction SilentlyContinue) {
        Unregister-ScheduledTask -TaskName $Name -Confirm:$false
    }

    $action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument $Arguments
    $principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
    $settings = New-ScheduledTaskSettingsSet `
        -AllowStartIfOnBatteries `
        -DontStopIfGoingOnBatteries `
        -StartWhenAvailable `
        -RestartCount 3 `
        -RestartInterval (New-TimeSpan -Minutes 5) `
        -ExecutionTimeLimit (New-TimeSpan -Hours 2)

    Register-ScheduledTask -TaskName $Name -Action $action -Trigger $Trigger `
        -Principal $principal -Settings $settings -Description $Description | Out-Null

    Write-Host "  [ok] $Name" -ForegroundColor Green
}

Write-Host "`n=== Registrando tareas ===" -ForegroundColor Cyan

# 1) Arranque: los contenedores tienen restart:unless-stopped, pero si Docker
#    tarda en levantar tras un corte de luz, esto lo asegura.
$startupScript = "Start-Sleep -Seconds 90; Set-Location '$root\deploy'; docker compose up -d"
Register-DocuHubTask -Name 'DocuHub-Arranque' `
    -Arguments "-NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -Command `"$startupScript`"" `
    -Trigger (New-ScheduledTaskTrigger -AtStartup) `
    -Description 'Levanta DocuHub al encender la laptop (espera 90 s a que Docker esté listo).'

# 2) Watchdog cada 5 minutos, indefinidamente.
$watchTrigger = New-ScheduledTaskTrigger -Once -At (Get-Date).AddMinutes(2) `
    -RepetitionInterval (New-TimeSpan -Minutes 5)
Register-DocuHubTask -Name 'DocuHub-Watchdog' `
    -Arguments "-NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File `"$root\scripts\health-check.ps1`"" `
    -Trigger $watchTrigger `
    -Description 'Comprueba /healthz cada 5 minutos y reinicia el servicio si no responde.'

# 3) Respaldo diario de madrugada, cuando nadie usa la plataforma.
Register-DocuHubTask -Name 'DocuHub-Respaldo' `
    -Arguments "-NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File `"$root\scripts\backup.ps1`"" `
    -Trigger (New-ScheduledTaskTrigger -Daily -At '03:15') `
    -Description 'Respaldo diario de la base de datos de DocuHub.'

Write-Host @"

Listo. Para comprobarlas:
    Get-ScheduledTask -TaskName 'DocuHub-*' | Format-Table TaskName, State

Para ejecutar una ahora mismo y ver si funciona:
    Start-ScheduledTask -TaskName 'DocuHub-Watchdog'
    Get-Content logs\health.log -Tail 20

Para quitarlas todas:
    .\scripts\install-tasks.ps1 -Remove
"@ -ForegroundColor Cyan
