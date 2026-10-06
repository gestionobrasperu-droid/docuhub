#Requires -Version 5.1
<#
.SYNOPSIS
    Cuánto tiempo ha estado DocuHub realmente disponible.

.DESCRIPTION
    Cuando el servidor es una laptop, la pregunta importante no es si el
    servicio responde ahora, sino cuántas horas al día lo hace. Este informe
    lee el log del watchdog y responde a eso con números, no con sensaciones.

    El watchdog escribe una línea cada 5 minutos mientras el equipo está
    encendido. Contando líneas y huecos se reconstruye la disponibilidad real.

.PARAMETER Days
    Período a analizar. Por defecto, 7 días.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\scripts\uptime-report.ps1 -Days 30
#>

[CmdletBinding()]
param(
    [int]$Days = 7
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$logFile = Join-Path $root 'logs\health.log'

if (-not (Test-Path $logFile)) {
    Write-Host 'Todavía no hay registros del watchdog.' -ForegroundColor Yellow
    Write-Host 'Comprueba que la tarea DocuHub-Watchdog existe y se ejecuta.'
    return
}

$desde = (Get-Date).AddDays(-$Days)
$marcas = @()
$errores = 0
$huecos = @()

foreach ($linea in Get-Content $logFile) {
    if ($linea -notmatch '^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\s+\[(\w+)\]') { continue }
    try {
        $t = [datetime]::ParseExact($Matches[1], 'yyyy-MM-dd HH:mm:ss', $null)
    } catch {
        continue
    }
    if ($t -lt $desde) { continue }

    $nivel = $Matches[2]
    if ($nivel -eq 'ERROR') { $errores++ }
    if ($nivel -eq 'GAP') { $huecos += $linea }
    if ($nivel -in @('OK', 'WARN', 'ERROR')) { $marcas += $t }
}

if ($marcas.Count -eq 0) {
    Write-Host "No hay registros en los últimos $Days días." -ForegroundColor Yellow
    return
}

# Cada comprobación cubre 5 minutos de funcionamiento.
$minutosEncendida = $marcas.Count * 5
$minutosPeriodo = $Days * 24 * 60
$disponibilidad = [math]::Round(($minutosEncendida / $minutosPeriodo) * 100, 1)
$horasDia = [math]::Round($minutosEncendida / 60 / $Days, 1)

Write-Host ''
Write-Host "=== Disponibilidad de DocuHub — últimos $Days días ===" -ForegroundColor Cyan
Write-Host ''
Write-Host ("  Comprobaciones registradas : {0}" -f $marcas.Count)
Write-Host ("  Tiempo encendida           : {0:N0} h de {1:N0} h posibles" -f ($minutosEncendida / 60), ($minutosPeriodo / 60))
Write-Host ("  Media por día              : {0} h" -f $horasDia)

$color = if ($disponibilidad -ge 95) { 'Green' } elseif ($disponibilidad -ge 60) { 'Yellow' } else { 'Red' }
Write-Host ("  Disponibilidad             : {0}%" -f $disponibilidad) -ForegroundColor $color

if ($errores -gt 0) {
    Write-Host ("  Fallos del servicio        : {0}" -f $errores) -ForegroundColor Yellow
} else {
    Write-Host '  Fallos del servicio        : ninguno' -ForegroundColor Green
}

if ($huecos.Count -gt 0) {
    Write-Host ''
    Write-Host "  Períodos sin servicio ($($huecos.Count)):" -ForegroundColor Yellow
    $huecos | Select-Object -Last 8 | ForEach-Object { Write-Host "    $_" }
}

Write-Host ''
if ($disponibilidad -lt 60) {
    Write-Host 'La laptop pasa apagada o suspendida la mayor parte del tiempo.' -ForegroundColor Yellow
    Write-Host 'Quien intente entrar fuera de ese horario verá la web caída. Si eso' -ForegroundColor Yellow
    Write-Host 'importa, revisa en este orden:' -ForegroundColor Yellow
    Write-Host '  1. Que esté siempre enchufada a la corriente.'
    Write-Host '  2. powercfg /a   — si "En espera moderna" está disponible, Windows'
    Write-Host '     puede suspenderla aunque los tiempos estén en 0.'
    Write-Host '  3. Que nadie la apague al terminar la jornada.'
    Write-Host '  4. BIOS: "Restore on AC Power Loss" en Power On, para que vuelva'
    Write-Host '     sola tras un corte de luz.'
} elseif ($disponibilidad -lt 95) {
    Write-Host 'Hay interrupciones, pero el servicio está disponible la mayor parte' -ForegroundColor Yellow
    Write-Host 'del tiempo. Mira los períodos de arriba para ver si siguen un patrón.' -ForegroundColor Yellow
} else {
    Write-Host 'Funcionamiento continuo. Nada que corregir.' -ForegroundColor Green
}
Write-Host ''
