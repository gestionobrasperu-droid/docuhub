#Requires -Version 5.1
<#
.SYNOPSIS
    Diagnóstico completo de la cadena dominio -> Cloudflare -> túnel -> aplicación.

.DESCRIPTION
    Revisa cada eslabón por separado y dice cuál está roto. Ejecútalo cuando
    algo no responda: te ahorra adivinar.

.EXAMPLE
    .\tunnel-status.ps1
#>
[CmdletBinding()]
param(
    [string] $Domain     = 'constructorapesam.com',
    [string] $TunnelName = 'docuhub'
)

function Write-Head ($t) {
    Write-Host ""
    Write-Host "-- $t " -ForegroundColor Cyan -NoNewline
    Write-Host ("-" * [Math]::Max(0, 58 - $t.Length)) -ForegroundColor DarkGray
}
function Write-Ok   ($t) { Write-Host "  [OK]   $t" -ForegroundColor Green }
function Write-Warn ($t) { Write-Host "  [!]    $t" -ForegroundColor Yellow }
function Write-Fail ($t) { Write-Host "  [X]    $t" -ForegroundColor Red }
function Write-Info ($t) { Write-Host "         $t" -ForegroundColor Gray }

Write-Host ""
Write-Host "===========================================================" -ForegroundColor White
Write-Host "  Diagnostico del tunel  -  $Domain" -ForegroundColor White
Write-Host "  $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')" -ForegroundColor DarkGray
Write-Host "===========================================================" -ForegroundColor White

# ── 1. Binario ───────────────────────────────────────────────────────────────
Write-Head "1. cloudflared"
$cf = (Get-Command cloudflared.exe -ErrorAction SilentlyContinue).Source
if (-not $cf) {
    foreach ($p in @("${env:ProgramFiles(x86)}\cloudflared\cloudflared.exe",
                     "$env:ProgramFiles\cloudflared\cloudflared.exe")) {
        if (Test-Path $p) { $cf = $p; break }
    }
}
if ($cf) { Write-Ok (& $cf --version); Write-Info $cf }
else     { Write-Fail "No instalado. winget install --id Cloudflare.cloudflared"; exit 1 }

# ── 2. Nameservers del dominio ───────────────────────────────────────────────
Write-Head "2. Nameservers de $Domain"
$ns = Resolve-DnsName -Name $Domain -Type NS -Server 8.8.8.8 -ErrorAction SilentlyContinue |
      Where-Object { $_.Type -eq 'NS' }
if (-not $ns) {
    Write-Fail "No se pudieron resolver los NS."
} else {
    $onCf = $false
    foreach ($r in $ns) {
        Write-Info $r.NameHost
        if ($r.NameHost -match 'ns\.cloudflare\.com$') { $onCf = $true }
    }
    if ($onCf) { Write-Ok "El dominio esta en Cloudflare." }
    else       { Write-Fail "El dominio NO esta en Cloudflare todavia. El tunel no podra crear DNS." }
}

# ── 3. Autenticación ─────────────────────────────────────────────────────────
Write-Head "3. Credenciales locales"
$cfDir = Join-Path $env:USERPROFILE '.cloudflared'
if (Test-Path (Join-Path $cfDir 'cert.pem')) { Write-Ok "cert.pem presente." }
else { Write-Fail "Falta cert.pem. Ejecuta: cloudflared tunnel login" }

$configPath = Join-Path $cfDir 'config.yml'
if (Test-Path $configPath) {
    Write-Ok "config.yml presente."
    & $cf tunnel ingress validate --config $configPath | Out-Null
    if ($LASTEXITCODE -eq 0) { Write-Ok "Sintaxis valida." } else { Write-Fail "Sintaxis INVALIDA." }
} else {
    Write-Fail "Falta config.yml. Ejecuta: .\setup-tunnel.ps1"
}

# ── 4. Túnel en Cloudflare ───────────────────────────────────────────────────
Write-Head "4. Tunel '$TunnelName' en Cloudflare"
$uuid = $null
$listJson = & $cf tunnel list --output json 2>$null
if ($LASTEXITCODE -eq 0 -and $listJson) {
    $t = ($listJson | Out-String | ConvertFrom-Json) |
         Where-Object { $_.name -eq $TunnelName -and -not $_.deleted_at }
    if ($t) {
        $uuid = $t[0].id
        Write-Ok "Existe. UUID: $uuid"
        $conns = $t[0].connections
        if ($conns -and $conns.Count -gt 0) {
            Write-Ok "$($conns.Count) conexion(es) activa(s) hacia Cloudflare:"
            foreach ($c in $conns) { Write-Info "$($c.colo_name)  (origen $($c.origin_ip))" }
        } else {
            Write-Fail "Sin conexiones activas: el tunel no esta corriendo."
        }
    } else { Write-Fail "No existe el tunel '$TunnelName'." }
} else { Write-Warn "No pude listar tuneles (revisa cert.pem)." }

# ── 5. Servicio de Windows ───────────────────────────────────────────────────
Write-Head "5. Servicio de Windows"
$svc = Get-Service -Name cloudflared -ErrorAction SilentlyContinue
if ($svc) {
    if ($svc.Status -eq 'Running') { Write-Ok "cloudflared: Running" } else { Write-Fail "cloudflared: $($svc.Status)" }
    $wmi = Get-CimInstance Win32_Service -Filter "Name='cloudflared'" -ErrorAction SilentlyContinue
    if ($wmi) { Write-Info "Inicio: $($wmi.StartMode)" }
} else {
    Write-Warn "No instalado como servicio (el tunel solo corre si lo lanzas a mano)."
}

# ── 6. Métricas locales ──────────────────────────────────────────────────────
Write-Head "6. Metricas locales (localhost:20241)"
try {
    $m = Invoke-WebRequest -Uri 'http://localhost:20241/metrics' -TimeoutSec 5 -UseBasicParsing -ErrorAction Stop
    $ha = [regex]::Matches($m.Content, 'cloudflared_tunnel_ha_connections\s+(\d+)')
    if ($ha.Count -gt 0) { Write-Ok "Conexiones HA activas: $($ha[0].Groups[1].Value)" }
    else                 { Write-Ok "Endpoint de metricas responde." }
} catch {
    Write-Warn "No responde. Normal si el tunel esta parado."
}

# ── 7. Reglas de ingress y puertos locales ───────────────────────────────────
Write-Head "7. Aplicaciones configuradas"
if (Test-Path $configPath) {
    $cfg   = Get-Content $configPath
    $host_ = $null
    foreach ($l in $cfg) {
        if ($l -match '^\s*-\s*hostname:\s*(\S+)') { $host_ = $Matches[1]; continue }
        if ($l -match '^\s*service:\s*(\S+)' -and $host_) {
            $svcUrl = $Matches[1]
            $state  = ""
            if ($svcUrl -match 'localhost:(\d+)') {
                $port = [int]$Matches[1]
                $lis  = Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue
                if ($lis) { $state = "  [puerto $port ESCUCHANDO]" } else { $state = "  [puerto $port CERRADO -> dara 502]" }
            } elseif ($svcUrl -eq 'hello_world') {
                $state = "  [pagina de prueba interna]"
            }
            if ($state -match 'CERRADO') { Write-Warn "$host_ -> $svcUrl$state" }
            else                         { Write-Ok   "$host_ -> $svcUrl$state" }
            $host_ = $null
        }
    }
}

# ── 8. Prueba real desde internet ────────────────────────────────────────────
Write-Head "8. Respuesta desde internet"
if (Test-Path $configPath) {
    $hosts = Get-Content $configPath |
             ForEach-Object { if ($_ -match '^\s*-\s*hostname:\s*(\S+)') { $Matches[1] } }
    $ProgressPreference = 'SilentlyContinue'
    foreach ($h in $hosts) {
        $dns = Resolve-DnsName -Name $h -Server 8.8.8.8 -ErrorAction SilentlyContinue
        if (-not $dns) { Write-Fail "$h  sin registro DNS"; continue }
        try {
            $r = Invoke-WebRequest -Uri "https://$h" -TimeoutSec 15 -UseBasicParsing -ErrorAction Stop
            Write-Ok "https://$h  ->  HTTP $($r.StatusCode)"
        } catch {
            $code = ""
            if ($_.Exception.Response) { $code = [int]$_.Exception.Response.StatusCode }
            if ($code -eq 502) { Write-Warn "https://$h  ->  502  (el tunel llega, la app local no responde)" }
            elseif ($code)     { Write-Warn "https://$h  ->  HTTP $code" }
            else               { Write-Fail "https://$h  ->  $($_.Exception.Message)" }
        }
    }
}

Write-Host ""
Write-Host "===========================================================" -ForegroundColor White
Write-Host ""
