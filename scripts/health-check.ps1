#Requires -Version 5.1
<#
.SYNOPSIS
    Watchdog: comprueba que DocuHub responde y lo reinicia si no.

.DESCRIPTION
    Pensado para ejecutarse cada 5 minutos desde el Programador de tareas
    (lo instala install-tasks.ps1). En cada pasada:
      1. Consulta /healthz
      2. Si falla, reintenta; si sigue fallando, reinicia los contenedores
      3. Registra el resultado y la temperatura en un log rotado

    El log sirve para ver tendencias: si la temperatura sube mes a mes, toca
    limpiar el ventilador antes de que la laptop empiece a apagarse sola.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\scripts\health-check.ps1
#>

[CmdletBinding()]
param(
    [string]$Url = 'http://localhost:8080/healthz',
    [int]$TimeoutSeconds = 15,
    [switch]$NoRestart
)

$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent $PSScriptRoot
$logDir = Join-Path $root 'logs'
if (-not (Test-Path $logDir)) { New-Item -ItemType Directory -Path $logDir -Force | Out-Null }
$logFile = Join-Path $logDir 'health.log'

function Write-Log($level, $message) {
    $line = '{0} [{1}] {2}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $level, $message
    Add-Content -Path $logFile -Value $line -Encoding utf8
    Write-Host $line
}

# Rotación simple: por encima de 5 MB se archiva y se empieza de cero.
if ((Test-Path $logFile) -and ((Get-Item $logFile).Length -gt 5MB)) {
    Move-Item $logFile "$logFile.1" -Force
}

function Test-Health {
    try {
        $res = Invoke-WebRequest -Uri $Url -TimeoutSec $TimeoutSeconds -UseBasicParsing
        return $res.StatusCode -eq 200
    } catch {
        return $false
    }
}

function Get-CpuTemperature {
    try {
        $t = Get-CimInstance -Namespace 'root/WMI' -ClassName MSAcpi_ThermalZoneTemperature -ErrorAction Stop |
             Select-Object -First 1
        if ($t) { return [math]::Round(($t.CurrentTemperature / 10) - 273.15, 1) }
    } catch {
        # Muchos equipos no exponen esta clase; no es un fallo del watchdog.
    }
    return $null
}

# Un hueco en el log significa que el equipo estuvo apagado o suspendido. Se
# anota explícitamente para poder medir la disponibilidad real, que es la
# pregunta de fondo cuando el "servidor" es una laptop: no si el servicio
# responde ahora, sino cuántas horas al día está disponible para los demás.
if (Test-Path $logFile) {
    $ultima = Get-Content $logFile -Tail 1 -ErrorAction SilentlyContinue
    if ($ultima -match '^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})') {
        try {
            $antes = [datetime]::ParseExact($Matches[1], 'yyyy-MM-dd HH:mm:ss', $null)
            $hueco = (Get-Date) - $antes
            if ($hueco.TotalMinutes -gt 15) {
                Write-Log 'GAP' ('el equipo estuvo {0:N1} h sin registrar (apagado o suspendido): la plataforma no estuvo disponible' -f $hueco.TotalHours)
            }
        } catch {
            # Una línea con formato raro no debe tumbar el watchdog.
        }
    }
}

$healthy = Test-Health

if ($healthy) {
    $temp = Get-CpuTemperature
    $disk = Get-PSDrive -Name C | Select-Object -ExpandProperty Free
    $extra = 'disco_libre={0}GB' -f [math]::Round($disk / 1GB, 1)
    if ($null -ne $temp) { $extra += " temp=${temp}C" }
    Write-Log 'OK' "servicio respondiendo; $extra"

    if ($disk -lt 5GB) {
        Write-Log 'WARN' 'Queda menos de 5 GB libres en C:. Limpia logs antiguos o imágenes de Docker.'
    }
    exit 0
}

Write-Log 'WARN' 'el servicio no respondió; reintentando en 20 s'
Start-Sleep -Seconds 20

if (Test-Health) {
    Write-Log 'OK' 'respondió en el reintento; no se reinicia nada'
    exit 0
}

if ($NoRestart) {
    Write-Log 'ERROR' 'el servicio sigue caído (reinicio desactivado por parámetro)'
    exit 1
}

Write-Log 'ERROR' 'el servicio sigue caído; reiniciando contenedores'

Push-Location (Join-Path $root 'deploy')
try {
    docker compose up -d 2>&1 | ForEach-Object { Write-Log 'DOCKER' $_ }
    Start-Sleep -Seconds 45

    if (Test-Health) {
        Write-Log 'OK' 'recuperado tras reiniciar los contenedores'
    } else {
        Write-Log 'ERROR' 'sigue sin responder tras el reinicio: revisa "docker compose logs app"'
        # Un problema del motor de Docker suele arreglarse reiniciando el servicio.
        $dockerSvc = Get-Service -Name 'com.docker.service' -ErrorAction SilentlyContinue
        if ($dockerSvc -and $dockerSvc.Status -ne 'Running') {
            Write-Log 'WARN' 'el servicio de Docker está detenido; intentando iniciarlo'
            Start-Service -Name 'com.docker.service' -ErrorAction SilentlyContinue
        }
    }
} finally {
    Pop-Location
}

# El túnel también puede caerse por su cuenta.
$tunnel = Get-Service -Name 'cloudflared' -ErrorAction SilentlyContinue
if ($tunnel -and $tunnel.Status -ne 'Running') {
    Write-Log 'WARN' 'el túnel de Cloudflare estaba detenido; iniciándolo'
    Start-Service -Name 'cloudflared' -ErrorAction SilentlyContinue
}
