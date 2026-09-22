#Requires -Version 5.1
<#
.SYNOPSIS
    Crea y configura el túnel de Cloudflare que publica las aplicaciones de la
    laptop en constructorapesam.com, sin IP pública y sin abrir puertos.

.DESCRIPTION
    Es idempotente: puedes ejecutarlo las veces que quieras. Si el túnel ya
    existe lo reutiliza; si el registro DNS ya existe lo deja como está.

    Pasos que ejecuta:
      1. Localiza cloudflared.exe
      2. Autentica contra Cloudflare (abre el navegador — autenticas TÚ)
      3. Crea el túnel si no existe
      4. Genera %USERPROFILE%\.cloudflared\config.yml desde la plantilla
      5. Crea los registros DNS (CNAME) de cada hostname
      6. Opcional: registra cloudflared como servicio de Windows (-InstallService)

.PARAMETER Domain
    Dominio raíz. Debe estar YA añadido a Cloudflare con los nameservers
    apuntando ahí, o el paso 2 no ofrecerá el dominio.

.PARAMETER Hostnames
    Pares "subdominio=destino". El destino es un puerto local, una URL o
    "hello_world" (página de prueba que sirve el propio cloudflared).

.EXAMPLE
    .\setup-tunnel.ps1
    Configuración por defecto: test.* (prueba) y docs.* (DocuHub, puerto 8080).

.EXAMPLE
    .\setup-tunnel.ps1 -Hostnames @{ test='hello_world'; docs=8080; api=3000 } -InstallService
#>
[CmdletBinding()]
param(
    [string]    $Domain      = 'constructorapesam.com',
    [string]    $TunnelName  = 'docuhub',
    [hashtable] $Hostnames   = @{ test = 'hello_world'; docs = 8080 },
    [switch]    $InstallService,
    [switch]    $SkipLogin
)

$ErrorActionPreference = 'Stop'

# ── Utilidades de salida ─────────────────────────────────────────────────────
function Write-Step ($n, $t) { Write-Host ""; Write-Host "[$n] $t" -ForegroundColor Cyan }
function Write-Ok   ($t)     { Write-Host "    OK  $t" -ForegroundColor Green }
function Write-Warn ($t)     { Write-Host "    !   $t" -ForegroundColor Yellow }
function Write-Fail ($t)     { Write-Host "    X   $t" -ForegroundColor Red }

# Un túnel vivo NO trae deleted_at vacío: trae el "tiempo cero" de Go,
# "0001-01-01T00:00:00Z". Filtrar por `-not $_.deleted_at` descarta los túneles
# buenos, así que hay que comprobarlo explícitamente.
function Test-TunnelAlive ($t) {
    return ([string]::IsNullOrEmpty($t.deleted_at) -or $t.deleted_at -match '^0001-01-01')
}

Write-Host ""
Write-Host "===========================================================" -ForegroundColor White
Write-Host "  Tunel de Cloudflare -> $Domain" -ForegroundColor White
Write-Host "===========================================================" -ForegroundColor White

# ── 1. Localizar cloudflared ─────────────────────────────────────────────────
Write-Step 1 "Localizando cloudflared"

$cf = $null
$cmd = Get-Command cloudflared.exe -ErrorAction SilentlyContinue
if ($cmd) {
    $cf = $cmd.Source
} else {
    foreach ($p in @(
        "$env:ProgramFiles(x86)\cloudflared\cloudflared.exe",
        "${env:ProgramFiles(x86)}\cloudflared\cloudflared.exe",
        "$env:ProgramFiles\cloudflared\cloudflared.exe")) {
        if ($p -and (Test-Path $p)) { $cf = $p; break }
    }
}

if (-not $cf) {
    Write-Fail "No encuentro cloudflared.exe."
    Write-Host "    Instalalo con:  winget install --id Cloudflare.cloudflared"
    exit 1
}

Write-Ok "$cf"
Write-Ok (& $cf --version)

# ── 2. Autenticación ─────────────────────────────────────────────────────────
Write-Step 2 "Autenticacion contra Cloudflare"

$cfDir   = Join-Path $env:USERPROFILE '.cloudflared'
$certPem = Join-Path $cfDir 'cert.pem'

if (-not (Test-Path $cfDir)) { New-Item -ItemType Directory -Path $cfDir -Force | Out-Null }

if (Test-Path $certPem) {
    Write-Ok "Ya autenticado (existe cert.pem)."
} elseif ($SkipLogin) {
    Write-Fail "No hay cert.pem y se pidio -SkipLogin. Aborto."
    exit 1
} else {
    Write-Warn "Se abrira el navegador. Inicia sesion en Cloudflare y elige '$Domain'."
    Write-Warn "La contrasena la escribes TU: este script nunca la ve."
    & $cf tunnel login
    if (-not (Test-Path $certPem)) {
        Write-Fail "La autenticacion no genero cert.pem. Revisa que '$Domain' este activo en Cloudflare."
        exit 1
    }
    Write-Ok "Autenticado. cert.pem guardado."
}

# ── 3. Crear el túnel ────────────────────────────────────────────────────────
Write-Step 3 "Tunel '$TunnelName'"

$uuid = $null
$listJson = & $cf tunnel list --output json 2>$null
if ($LASTEXITCODE -eq 0 -and $listJson) {
    $existing = ($listJson | Out-String | ConvertFrom-Json) |
                Where-Object { $_.name -eq $TunnelName -and (Test-TunnelAlive $_) }
    if ($existing) { $uuid = $existing[0].id }
}

if ($uuid) {
    Write-Ok "Ya existe. UUID: $uuid"
} else {
    & $cf tunnel create $TunnelName
    $listJson = & $cf tunnel list --output json
    $existing = ($listJson | Out-String | ConvertFrom-Json) |
                Where-Object { $_.name -eq $TunnelName -and (Test-TunnelAlive $_) }
    if (-not $existing) { Write-Fail "No pude crear el tunel."; exit 1 }
    $uuid = $existing[0].id
    Write-Ok "Creado. UUID: $uuid"
}

$credFile = Join-Path $cfDir "$uuid.json"
if (-not (Test-Path $credFile)) {
    Write-Fail "No encuentro el archivo de credenciales: $credFile"
    exit 1
}
Write-Ok "Credenciales: $credFile"

# ── 4. Generar config.yml ────────────────────────────────────────────────────
Write-Step 4 "Generando config.yml"

$logFile    = Join-Path $cfDir 'cloudflared.log'
$configPath = Join-Path $cfDir 'config.yml'

if (Test-Path $configPath) {
    $backup = Join-Path $cfDir ("config.yml.bak-{0:yyyyMMdd-HHmmss}" -f (Get-Date))
    Copy-Item $configPath $backup
    Write-Warn "config.yml anterior respaldado en $backup"
}

# Construye las reglas de ingress a partir del hashtable -Hostnames.
$rules = New-Object System.Text.StringBuilder
foreach ($sub in ($Hostnames.Keys | Sort-Object)) {
    $dest = $Hostnames[$sub]
    if ($dest -is [int] -or $dest -match '^\d+$') { $svc = "http://localhost:$dest" }
    elseif ($dest -eq 'hello_world')              { $svc = 'hello_world' }
    else                                          { $svc = [string]$dest }

    [void]$rules.AppendLine("  - hostname: $sub.$Domain")
    [void]$rules.AppendLine("    service: $svc")
    [void]$rules.AppendLine("")
}

$config = @"
# Generado por scripts\setup-tunnel.ps1 el $(Get-Date -Format 'yyyy-MM-dd HH:mm')
# Para anadir aplicaciones usa scripts\add-app.ps1 (no edites a mano si puedes evitarlo).

tunnel: $uuid
credentials-file: $credFile

metrics: localhost:20241
retries: 5
grace-period: 30s
loglevel: info
logfile: $logFile

originRequest:
  connectTimeout: 30s
  noTLSVerify: false
  keepAliveConnections: 100
  keepAliveTimeout: 90s

ingress:
$($rules.ToString())  # MARCADOR-APPS  (add-app.ps1 inserta encima de esta linea)

  # Regla final obligatoria: lo que no coincida devuelve 404.
  - service: http_status:404
"@

Set-Content -Path $configPath -Value $config -Encoding utf8
Write-Ok "$configPath"

# Valida la sintaxis antes de seguir.
# OJO: --config va ANTES del subcomando `ingress`, no después.
& $cf tunnel --config $configPath ingress validate
if ($LASTEXITCODE -ne 0) { Write-Fail "config.yml invalido. Revisa el archivo."; exit 1 }
Write-Ok "Sintaxis validada."

# ── 5. Registros DNS ─────────────────────────────────────────────────────────
Write-Step 5 "Registros DNS en Cloudflare"

# OJO: nada de `2>&1` sobre un ejecutable nativo. En PowerShell 5.1 eso envuelve
# cada línea de stderr en un ErrorRecord (NativeCommandError) y, con
# $ErrorActionPreference='Stop', aborta el script aunque el comando haya ido bien
# — y cloudflared escribe sus mensajes informativos en stderr.
foreach ($sub in ($Hostnames.Keys | Sort-Object)) {
    $fqdn = "$sub.$Domain"
    & $cf tunnel route dns $TunnelName $fqdn
    if ($LASTEXITCODE -eq 0) {
        Write-Ok "$fqdn -> $uuid.cfargotunnel.com"
    } else {
        Write-Warn "$fqdn no se creo (lo habitual es que el registro ya existiera)."
    }
}

# ── 6. Servicio de Windows ───────────────────────────────────────────────────
Write-Step 6 "Servicio de Windows"

$isAdmin = (New-Object Security.Principal.WindowsPrincipal(
    [Security.Principal.WindowsIdentity]::GetCurrent())
).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

$svc = Get-Service -Name cloudflared -ErrorAction SilentlyContinue

if ($svc) {
    Write-Ok "Ya instalado (estado: $($svc.Status))."

    # Si el ImagePath no lleva --config, el servicio corre sin configuracion y
    # Cloudflare devuelve 530 aunque el servicio figure como 'Running'.
    $have = (Get-ItemProperty -Path 'HKLM:\SYSTEM\CurrentControlSet\Services\cloudflared' `
                              -ErrorAction SilentlyContinue).ImagePath
    if ($have -notmatch '--config') {
        Write-Fail "El servicio esta registrado SIN --config: no enruta nada (error 530)."
        Write-Host  "    Corrigelo con, en PowerShell COMO ADMINISTRADOR:"
        Write-Host  "      .\setup-tunnel.ps1 -InstallService -SkipLogin"
    } elseif ($isAdmin) {
        # Restart-Service puede colgarse: el proceso no siempre atiende el SCM.
        Get-Process cloudflared -ErrorAction SilentlyContinue |
            ForEach-Object { Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue }
        Start-Sleep -Seconds 3
        & sc.exe start cloudflared | Out-Null
        Start-Sleep -Seconds 6
        Write-Ok "Servicio reiniciado (estado: $((Get-Service cloudflared).Status))."
    } else {
        Write-Warn "Para aplicar cambios de config, como administrador:  Restart-Service cloudflared"
    }
} elseif ($InstallService) {
    if (-not $isAdmin) {
        Write-Fail "Instalar el servicio requiere administrador."
        Write-Host "    Abre PowerShell como admin y ejecuta:"
        Write-Host "      & '$cf' service install"
        exit 1
    }
    & $cf service install
    Start-Sleep -Seconds 3
    $svc = Get-Service -Name cloudflared -ErrorAction SilentlyContinue
    if (-not $svc) {
        Write-Fail "El servicio no quedo registrado."
    } else {
        # `cloudflared service install` (2026.9.1) registra el servicio SIN
        # argumentos y no copia la configuracion a
        # C:\Windows\System32\config\systemprofile\.cloudflared. Resultado: el
        # servicio arranca, no encuentra config, no enruta nada, y Cloudflare
        # devuelve 530. Hay que apuntar el ImagePath a la config a mano.
        $key = 'HKLM:\SYSTEM\CurrentControlSet\Services\cloudflared'
        $want = '"' + $cf + '" --config "' + $configPath + '" --no-autoupdate tunnel run'
        $have = (Get-ItemProperty -Path $key -ErrorAction SilentlyContinue).ImagePath

        if ($have -ne $want) {
            Write-Warn "ImagePath sin configuracion. Corrigiendo."
            # El proceso no siempre responde al control de parada del SCM.
            Get-Process cloudflared -ErrorAction SilentlyContinue |
                ForEach-Object { Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue }
            Start-Sleep -Seconds 3
            & sc.exe stop cloudflared | Out-Null
            $n = 0
            while ((Get-Service cloudflared -ErrorAction SilentlyContinue).Status -ne 'Stopped' -and $n -lt 15) {
                Start-Sleep -Seconds 2; $n++
            }
            Set-ItemProperty -Path $key -Name ImagePath -Value $want
            Write-Ok "ImagePath corregido."
        }

        # Reinicio automatico si el proceso muere.
        & sc.exe failure cloudflared reset= 86400 actions= restart/5000/restart/10000/restart/30000 | Out-Null

        Set-Service -Name cloudflared -StartupType Automatic
        if ((Get-Service cloudflared).Status -ne 'Running') { & sc.exe start cloudflared | Out-Null }
        Start-Sleep -Seconds 8
        $st = (Get-Service cloudflared -ErrorAction SilentlyContinue).Status
        if ($st -eq 'Running') { Write-Ok "Servicio instalado, corriendo y en arranque automatico." }
        else                   { Write-Fail "El servicio quedo en estado '$st'." }
    }
} else {
    Write-Warn "No instalado (no se paso -InstallService)."
    Write-Host "    Para probar en primer plano sin instalarlo:"
    Write-Host "      & '$cf' tunnel run $TunnelName"
    Write-Host "    Para instalarlo como servicio 24/7, en PowerShell COMO ADMINISTRADOR:"
    Write-Host "      .\setup-tunnel.ps1 -InstallService -SkipLogin"
}

# ── Resumen ──────────────────────────────────────────────────────────────────
Write-Host ""
Write-Host "===========================================================" -ForegroundColor White
Write-Host "  Listo" -ForegroundColor White
Write-Host "===========================================================" -ForegroundColor White
Write-Host "  Tunel : $TunnelName ($uuid)"
Write-Host "  Config: $configPath"
Write-Host "  Log   : $logFile"
Write-Host "  URLs  :"
foreach ($sub in ($Hostnames.Keys | Sort-Object)) {
    Write-Host ("          https://{0}.{1}  ->  {2}" -f $sub, $Domain, $Hostnames[$sub])
}
Write-Host ""
Write-Host "  Comprueba el estado con:  .\tunnel-status.ps1"
Write-Host ""
