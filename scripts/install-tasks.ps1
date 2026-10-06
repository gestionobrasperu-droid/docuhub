#Requires -Version 5.1
<#
.SYNOPSIS
    Registra en el Programador de tareas todo lo que debe ocurrir solo:
    arranque, watchdog y respaldo diario.

.DESCRIPTION
    Ejecutar como administrador. Crea tres tareas que corren como SYSTEM, asi
    que funcionan aunque nadie tenga la sesion iniciada:

      DocuHub-Arranque   al encender, espera al motor de Docker y levanta la pila
      DocuHub-Watchdog   cada 5 minutos, comprueba /healthz y recupera el servicio
      DocuHub-Respaldo   cada dia a las 03:15, respalda la base de datos

    Ojo al comprobarlas: una tarea que corre como SYSTEM con nivel mas alto
    NO es visible desde una sesion de PowerShell normal. Get-ScheduledTask
    devuelve cero resultados y schtasks /run responde "Acceso denegado",
    aunque la tarea exista y se este ejecutando puntualmente. Para verlas hay
    que consultar elevado:

        Start-Process powershell -Verb RunAs -ArgumentList '-Command','schtasks /query /fo table /nh | findstr DocuHub'

    La prueba que si funciona sin privilegios es mirar el resultado de su
    trabajo:  Get-Content logs\health.log -Tail 5

    Se usa schtasks.exe en lugar de Register-ScheduledTask porque devuelve el
    resultado de cada alta en el acto, y este script lo verifica antes de dar
    nada por bueno.

    Docker Desktop necesita una sesion de usuario iniciada. Si la laptop se
    reinicia sin que nadie entre, la tarea de arranque esperara al motor y
    acabara fallando: activa el inicio de sesion automatico de Windows, o
    pasa a Docker Engine sobre WSL2, que corre como servicio real.

.PARAMETER Remove
    Elimina las tres tareas en lugar de crearlas.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\scripts\install-tasks.ps1
#>

[CmdletBinding()]
param(
    [switch]$Remove
)

$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent $PSScriptRoot

$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
    ).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
    throw 'Este script necesita PowerShell como administrador.'
}

$names = @('DocuHub-Arranque', 'DocuHub-Watchdog', 'DocuHub-Respaldo')

if ($Remove) {
    foreach ($n in $names) {
        schtasks /delete /tn $n /f 2>&1 | Out-Null
        Write-Host "  eliminada: $n" -ForegroundColor Yellow
    }
    return
}

$ps = "$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe"
function Script-Cmd($file) {
    "`"$ps`" -NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File `"$root\scripts\$file`""
}

$tasks = @(
    @{ Name = 'DocuHub-Arranque'
       Cmd  = (Script-Cmd 'boot-start.ps1')
       # 90 s de margen para que Docker arranque antes que la tarea.
       Args = @('/sc', 'ONSTART', '/delay', '0001:30') }

    @{ Name = 'DocuHub-Watchdog'
       Cmd  = (Script-Cmd 'health-check.ps1')
       Args = @('/sc', 'MINUTE', '/mo', '5') }

    @{ Name = 'DocuHub-Respaldo'
       Cmd  = (Script-Cmd 'backup.ps1')
       Args = @('/sc', 'DAILY', '/st', '03:15') }
)

Write-Host "`n=== Registrando tareas ===" -ForegroundColor Cyan

foreach ($t in $tasks) {
    schtasks /delete /tn $t.Name /f 2>&1 | Out-Null

    $argList = @('/create', '/tn', $t.Name, '/tr', $t.Cmd, '/ru', 'SYSTEM', '/rl', 'HIGHEST', '/f') + $t.Args
    $out = & schtasks @argList 2>&1

    if ($LASTEXITCODE -eq 0) {
        Write-Host "  [ok] $($t.Name)" -ForegroundColor Green
    } else {
        Write-Host "  [!]  $($t.Name): $out" -ForegroundColor Red
    }
}

# schtasks deja dos condiciones de energia que en una laptop equivalen a
# desactivar la tarea: no iniciarla con bateria y detenerla si se pasa a
# bateria. Es lo que hacia fallar el respaldo con 0x800710E0 todos los dias
# que el equipo no estaba enchufado a las 03:15. Tampoco recupera las
# ejecuciones perdidas mientras estuvo apagado, asi que se anade
# StartWhenAvailable. Esto no se puede expresar en la linea de schtasks:
# hay que corregirlo despues.
Write-Host "`n=== Ajustando condiciones de energia ===" -ForegroundColor Cyan
foreach ($t in $tasks) {
    try {
        $ajustes = New-ScheduledTaskSettingsSet `
            -AllowStartIfOnBatteries `
            -DontStopIfGoingOnBatteries `
            -StartWhenAvailable `
            -RestartCount 3 `
            -RestartInterval (New-TimeSpan -Minutes 5) `
            -ExecutionTimeLimit (New-TimeSpan -Hours 2) `
            -MultipleInstances IgnoreNew
        Set-ScheduledTask -TaskName $t.Name -Settings $ajustes -ErrorAction Stop | Out-Null
        Write-Host "  [ok] $($t.Name): funciona con bateria y recupera ejecuciones perdidas" -ForegroundColor Green
    } catch {
        Write-Host "  [!]  $($t.Name): no se pudieron ajustar ($($_.Exception.Message))" -ForegroundColor Red
    }
}

# Verificacion real: que el Programador las devuelva, no que el comando
# anterior dijera que si.
Write-Host "`n=== Comprobacion ===" -ForegroundColor Cyan
$found = schtasks /query /fo csv /nh 2>&1 | Select-String 'DocuHub'
if ($found) {
    $found | ForEach-Object { Write-Host "  $($_.Line)" }
} else {
    Write-Host '  [!]  Ninguna tarea quedo registrada. Revisa el Programador de tareas.' -ForegroundColor Red
}

Write-Host @"

Para ejecutar una ahora y ver si funciona:
    schtasks /run /tn DocuHub-Watchdog
    Get-Content logs\health.log -Tail 20

Para quitarlas:
    .\scripts\install-tasks.ps1 -Remove
"@ -ForegroundColor Cyan
