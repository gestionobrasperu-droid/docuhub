#Requires -Version 5.1
<#
.SYNOPSIS
    Publica una aplicación más por el túnel ya existente.

.DESCRIPTION
    Un solo túnel puede servir tantas aplicaciones como quieras: cada una en su
    subdominio, apuntando a un puerto distinto de la laptop. Este script:
      1. Inserta la regla de ingress en config.yml (antes del 404 final)
      2. Valida la sintaxis
      3. Crea el registro DNS en Cloudflare
      4. Reinicia el servicio para aplicar el cambio

    Es idempotente: si el subdominio ya está configurado, actualiza su destino.

.EXAMPLE
    .\add-app.ps1 -Subdomain api -Port 3000
    Publica https://api.constructorapesam.com -> http://localhost:3000

.EXAMPLE
    .\add-app.ps1 -Subdomain panel -Port 5173
    Típico para un frontend en Vite durante el desarrollo.

.EXAMPLE
    .\add-app.ps1 -Subdomain interno -Service "https://localhost:8443" -NoTLSVerify
    Destino HTTPS con certificado autofirmado.

.EXAMPLE
    .\add-app.ps1 -Subdomain ssh -Service "ssh://localhost:22"
    cloudflared también tunneliza TCP/SSH/RDP, no solo HTTP.
#>
[CmdletBinding(DefaultParameterSetName = 'Port')]
param(
    [Parameter(Mandatory, Position = 0)]
    [ValidatePattern('^[a-z0-9]([a-z0-9-]*[a-z0-9])?$')]
    [string] $Subdomain,

    [Parameter(Mandatory, ParameterSetName = 'Port')]
    [ValidateRange(1, 65535)]
    [int]    $Port,

    [Parameter(Mandatory, ParameterSetName = 'Service')]
    [string] $Service,

    [string] $Domain     = 'constructorapesam.com',
    [string] $TunnelName = 'docuhub',
    [switch] $NoTLSVerify,
    [switch] $Remove
)

$ErrorActionPreference = 'Stop'

function Write-Ok   ($t) { Write-Host "OK  $t" -ForegroundColor Green }
function Write-Warn ($t) { Write-Host "!   $t" -ForegroundColor Yellow }
function Write-Fail ($t) { Write-Host "X   $t" -ForegroundColor Red }

# ── Localizar cloudflared y la configuración ─────────────────────────────────
$cf = (Get-Command cloudflared.exe -ErrorAction SilentlyContinue).Source
if (-not $cf) {
    foreach ($p in @("${env:ProgramFiles(x86)}\cloudflared\cloudflared.exe",
                     "$env:ProgramFiles\cloudflared\cloudflared.exe")) {
        if (Test-Path $p) { $cf = $p; break }
    }
}
if (-not $cf) { Write-Fail "cloudflared no esta instalado."; exit 1 }

$configPath = Join-Path $env:USERPROFILE '.cloudflared\config.yml'
if (-not (Test-Path $configPath)) {
    Write-Fail "No existe $configPath. Ejecuta primero setup-tunnel.ps1."
    exit 1
}

$fqdn  = "$Subdomain.$Domain"
$lines = [System.Collections.Generic.List[string]](Get-Content $configPath)

# Respaldo antes de tocar nada.
$backup = "$configPath.bak-{0:yyyyMMdd-HHmmss}" -f (Get-Date)
Copy-Item $configPath $backup

# ── Quitar el bloque existente de este hostname, si lo hay ───────────────────
# Un bloque va desde su línea "- hostname: X" hasta la línea anterior al
# siguiente "- hostname:" / "- service:" de primer nivel.
$out      = [System.Collections.Generic.List[string]]::new()
$skipping = $false
$found    = $false

foreach ($line in $lines) {
    if ($line -match '^\s*-\s*hostname:\s*(\S+)\s*$') {
        if ($Matches[1] -eq $fqdn) { $skipping = $true; $found = $true; continue }
        $skipping = $false
    } elseif ($line -match '^\s*-\s*service:') {
        $skipping = $false
    }
    if (-not $skipping) { $out.Add($line) }
}

if ($Remove) {
    if (-not $found) { Write-Warn "$fqdn no estaba en la configuracion."; exit 0 }
    Set-Content -Path $configPath -Value $out -Encoding utf8
    & $cf tunnel --config $configPath ingress validate
    if ($LASTEXITCODE -ne 0) {
        Write-Fail "config.yml quedo invalido. Restaurando respaldo."
        Copy-Item $backup $configPath -Force
        exit 1
    }
    Write-Ok "$fqdn eliminado de config.yml."
    Write-Warn "El registro DNS sigue en Cloudflare. Borralo desde el panel si ya no lo usas."
} else {
    if ($found) { Write-Warn "$fqdn ya existia: se reemplaza su destino." }

    if ($PSCmdlet.ParameterSetName -eq 'Port') { $svc = "http://localhost:$Port" }
    else                                       { $svc = $Service }

    # ── Insertar el bloque justo encima del MARCADOR-APPS ────────────────────
    # Bucle explícito: List.FindIndex con scriptblock no convierte a
    # Predicate<string> de forma fiable en PowerShell 5.1.
    $marker = -1
    for ($i = 0; $i -lt $out.Count; $i++) {
        if ($out[$i] -match 'MARCADOR-APPS') { $marker = $i; break }
    }
    if ($marker -lt 0) {
        # Sin marcador: lo ponemos antes de la regla final http_status:404.
        for ($i = 0; $i -lt $out.Count; $i++) {
            if ($out[$i] -match '^\s*-\s*service:\s*http_status:404') { $marker = $i; break }
        }
    }
    if ($marker -lt 0) { Write-Fail "config.yml no tiene ni MARCADOR-APPS ni la regla 404 final."; exit 1 }

    $block = [System.Collections.Generic.List[string]]::new()
    $block.Add("  - hostname: $fqdn")
    $block.Add("    service: $svc")
    if ($NoTLSVerify) {
        $block.Add("    originRequest:")
        $block.Add("      noTLSVerify: true")
    }
    $block.Add("")

    $out.InsertRange($marker, $block)
    Set-Content -Path $configPath -Value $out -Encoding utf8

    & $cf tunnel --config $configPath ingress validate
    if ($LASTEXITCODE -ne 0) {
        Write-Fail "config.yml quedo invalido. Restaurando respaldo."
        Copy-Item $backup $configPath -Force
        exit 1
    }
    Write-Ok "config.yml actualizado: $fqdn -> $svc"

    # ── DNS ──────────────────────────────────────────────────────────────────
    # Sin `2>&1`: en PowerShell 5.1 convierte el stderr de un .exe en
    # NativeCommandError y aborta el script aunque el comando funcione.
    & $cf tunnel route dns $TunnelName $fqdn
    if ($LASTEXITCODE -eq 0) { Write-Ok   "DNS creado: $fqdn" }
    else                     { Write-Warn "DNS no creado (lo habitual es que ya existiera): $fqdn" }
}

# ── Aplicar ──────────────────────────────────────────────────────────────────
$svcWin = Get-Service -Name cloudflared -ErrorAction SilentlyContinue
if ($svcWin) {
    $isAdmin = (New-Object Security.Principal.WindowsPrincipal(
        [Security.Principal.WindowsIdentity]::GetCurrent())
    ).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

    if ($isAdmin) {
        Restart-Service cloudflared -Force
        Write-Ok "Servicio cloudflared reiniciado."
    } else {
        Write-Warn "Para aplicarlo, en PowerShell como administrador:  Restart-Service cloudflared"
    }
} else {
    Write-Warn "El servicio no esta instalado; el cambio se aplicara al proximo arranque del tunel."
}

Write-Host ""
Write-Host "Respaldo de la config anterior: $backup" -ForegroundColor DarkGray
