#Requires -Version 5.1
<#
.SYNOPSIS
    Crea los accesos directos de DocuHub en el escritorio.

.DESCRIPTION
    Tres iconos, para no tener que recordar ningún comando:

      Iniciar DocuHub   arranca Docker, levanta la plataforma y abre el navegador
      Detener DocuHub   la para de forma ordenada
      Estado de DocuHub informe de disponibilidad de los últimos días

    No requiere permisos de administrador: escribe solo en el escritorio del
    usuario actual.

.PARAMETER Quitar
    Elimina los accesos directos en lugar de crearlos.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\scripts\crear-accesos-escritorio.ps1
#>

[CmdletBinding()]
param(
    [switch]$Quitar
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$escritorio = [Environment]::GetFolderPath('Desktop')
$ps = "$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe"

# Iconos del propio Windows: no hace falta distribuir ningún .ico.
$accesos = @(
    @{ Nombre = 'Iniciar DocuHub'
       Script = 'iniciar-docuhub.ps1'
       Icono  = "$env:SystemRoot\System32\shell32.dll,137"   # play / flecha verde
       Desc   = 'Arranca la plataforma documental y abre el navegador' }

    @{ Nombre = 'Detener DocuHub'
       Script = 'detener-docuhub.ps1'
       Icono  = "$env:SystemRoot\System32\shell32.dll,131"   # stop
       Desc   = 'Detiene la plataforma de forma ordenada' }

    @{ Nombre = 'Estado de DocuHub'
       Script = 'uptime-report.ps1'
       Icono  = "$env:SystemRoot\System32\shell32.dll,43"    # informe
       Desc   = 'Cuántas horas al día ha estado disponible la plataforma'
       Args   = '-Days 14'
       Pausa  = $true }
)

if ($Quitar) {
    foreach ($a in $accesos) {
        $lnk = Join-Path $escritorio ($a.Nombre + '.lnk')
        if (Test-Path $lnk) {
            Remove-Item $lnk -Force
            Write-Host "  eliminado: $($a.Nombre)" -ForegroundColor Yellow
        }
    }
    return
}

$shell = New-Object -ComObject WScript.Shell

foreach ($a in $accesos) {
    $ruta = Join-Path $escritorio ($a.Nombre + '.lnk')
    $destino = Join-Path $root ('scripts\' + $a.Script)

    if (-not (Test-Path $destino)) {
        Write-Host "  [!] falta $($a.Script), se omite" -ForegroundColor Yellow
        continue
    }

    $argumentos = "-NoProfile -ExecutionPolicy Bypass -File `"$destino`""
    if ($a.Args) { $argumentos += ' ' + $a.Args }
    # Los informes se leen: la ventana debe quedarse abierta al terminar.
    if ($a.Pausa) {
        $argumentos = "-NoProfile -ExecutionPolicy Bypass -NoExit -File `"$destino`""
        if ($a.Args) { $argumentos += ' ' + $a.Args }
    }

    $lnk = $shell.CreateShortcut($ruta)
    $lnk.TargetPath = $ps
    $lnk.Arguments = $argumentos
    $lnk.WorkingDirectory = $root
    $lnk.IconLocation = $a.Icono
    $lnk.Description = $a.Desc
    $lnk.WindowStyle = 1
    $lnk.Save()

    Write-Host "  [ok] $($a.Nombre)" -ForegroundColor Green
}

Write-Host ''
Write-Host "Accesos creados en: $escritorio" -ForegroundColor Cyan
Write-Host ''
Write-Host 'Para quitarlos:  .\scripts\crear-accesos-escritorio.ps1 -Quitar' -ForegroundColor DarkGray
