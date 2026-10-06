#Requires -Version 5.1
<#
.SYNOPSIS
    Comprueba que el último respaldo se puede restaurar de verdad.

.DESCRIPTION
    Un respaldo que nunca se ha restaurado no es un respaldo: es un archivo del
    que nadie sabe nada. Este script lo prueba de la única forma que vale —
    restaurándolo — sin tocar la base de producción:

      1. Descomprime el respaldo más reciente
      2. Levanta un PostgreSQL temporal y aislado
      3. Restaura el volcado dentro
      4. Comprueba que lo que importa sigue ahí: las tablas, el administrador
         con su hash intacto y la cuenta de Google con su token cifrado
      5. Destruye el contenedor de prueba

    Si el token de Google no sobreviviera, al recuperar en otro equipo habría
    que volver a conectar la cuenta; si el hash del administrador no
    sobreviviera, nadie podría entrar. Por eso se comprueban esos dos.

    Devuelve código 0 si el respaldo sirve, y distinto de 0 si no, para que el
    Programador de tareas lo marque como fallo.

.PARAMETER Path
    Respaldo concreto a verificar. Por defecto, el más reciente de backups\.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\scripts\verify-backup.ps1
#>

[CmdletBinding()]
param(
    [string]$Path
)

$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent $PSScriptRoot
$logDir = Join-Path $root 'logs'
if (-not (Test-Path $logDir)) { New-Item -ItemType Directory -Path $logDir -Force | Out-Null }
$logFile = Join-Path $logDir 'backup-verify.log'

$contenedor = 'docuhub-restore-test'
$temp = Join-Path $env:TEMP "docuhub-verify-$(Get-Date -Format 'yyyyMMddHHmmss')"

function Log($nivel, $texto) {
    $linea = '{0} [{1}] {2}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $nivel, $texto
    Add-Content -Path $logFile -Value $linea -Encoding utf8
    $color = switch ($nivel) { 'OK' { 'Green' } 'ERROR' { 'Red' } default { 'Gray' } }
    Write-Host $linea -ForegroundColor $color
}

function Limpiar {
    & $docker rm -f $contenedor 2>&1 | Out-Null
    if (Test-Path $temp) { Remove-Item $temp -Recurse -Force -ErrorAction SilentlyContinue }
}

# ---------------------------------------------------------------- requisitos --

$docker = 'C:\Program Files\Docker\Docker\resources\bin\docker.exe'
if (-not (Test-Path $docker)) {
    $docker = (Get-Command docker -ErrorAction SilentlyContinue).Source
}
if (-not $docker) {
    Log 'ERROR' 'no se encontró docker.exe'
    exit 1
}

if (-not $Path) {
    $ultimo = Get-ChildItem -Path (Join-Path $root 'backups') -Filter 'docuhub_*.zip' -ErrorAction SilentlyContinue |
              Sort-Object LastWriteTime -Descending | Select-Object -First 1
    if (-not $ultimo) {
        Log 'ERROR' 'no hay ningún respaldo en backups\. ¿Se está ejecutando la tarea DocuHub-Respaldo?'
        exit 1
    }
    $Path = $ultimo.FullName
}

$edad = [math]::Round(((Get-Date) - (Get-Item $Path).LastWriteTime).TotalDays, 1)
Log 'INFO' "verificando $(Split-Path $Path -Leaf) ($edad días de antigüedad)"

if ($edad -gt 3) {
    Log 'WARN' "el respaldo más reciente tiene $edad días: la tarea diaria no se está ejecutando"
}

# ------------------------------------------------------------------ prueba ---

try {
    New-Item -ItemType Directory -Path $temp -Force | Out-Null
    Expand-Archive -Path $Path -DestinationPath $temp -Force
    $sql = Get-ChildItem -Path $temp -Filter '*.sql' | Select-Object -First 1
    if (-not $sql) {
        Log 'ERROR' 'el archivo comprimido no contiene ningún .sql'
        Limpiar; exit 1
    }

    & $docker rm -f $contenedor 2>&1 | Out-Null
    & $docker run -d --name $contenedor `
        -e POSTGRES_PASSWORD=verify -e POSTGRES_USER=docuhub -e POSTGRES_DB=docuhub `
        postgres:16-alpine 2>&1 | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Log 'ERROR' 'no se pudo levantar el PostgreSQL de prueba'
        Limpiar; exit 1
    }

    # Esperar a que acepte conexiones.
    $listo = $false
    for ($i = 0; $i -lt 30; $i++) {
        Start-Sleep -Seconds 2
        & $docker exec $contenedor pg_isready -U docuhub -d docuhub 2>&1 | Out-Null
        if ($LASTEXITCODE -eq 0) { $listo = $true; break }
    }
    if (-not $listo) {
        Log 'ERROR' 'el PostgreSQL de prueba no arrancó en 60 s'
        Limpiar; exit 1
    }

    & $docker cp $sql.FullName "${contenedor}:/tmp/dump.sql" 2>&1 | Out-Null
    $salida = & $docker exec $contenedor psql -U docuhub -d docuhub -q -f /tmp/dump.sql 2>&1
    $fallos = @($salida | Select-String -Pattern 'ERROR' -SimpleMatch)
    if ($fallos.Count -gt 0) {
        Log 'ERROR' "la restauración dio $($fallos.Count) errores: $($fallos[0])"
        Limpiar; exit 1
    }

    # --- Comprobaciones de contenido -------------------------------------
    $consulta = @"
SELECT
  (SELECT count(*) FROM users),
  (SELECT count(*) FROM users WHERE password_hash LIKE '`$argon2id`$%'),
  (SELECT count(*) FROM drive_accounts),
  (SELECT count(*) FROM drive_accounts WHERE length(refresh_token_enc) > 20),
  (SELECT count(*) FROM files),
  (SELECT count(*) FROM audit_log);
"@
    $fila = & $docker exec $contenedor psql -U docuhub -d docuhub -t -A -F'|' -c $consulta 2>&1
    $n = ($fila -join '').Trim().Split('|')

    if ($n.Count -lt 6) {
        Log 'ERROR' "no se pudo consultar la base restaurada: $fila"
        Limpiar; exit 1
    }

    $usuarios = [int]$n[0]; $hashes = [int]$n[1]
    $cuentas = [int]$n[2];  $tokens = [int]$n[3]
    $archivos = [int]$n[4]; $bitacora = [int]$n[5]

    $problemas = @()
    if ($usuarios -eq 0) { $problemas += 'no hay usuarios: nadie podría entrar' }
    if ($usuarios -ne $hashes) { $problemas += "$($usuarios - $hashes) usuarios sin hash Argon2id válido" }
    if ($cuentas -gt 0 -and $tokens -lt $cuentas) {
        $problemas += 'alguna cuenta de Google perdió su token: habría que reconectarla'
    }

    if ($problemas.Count -gt 0) {
        foreach ($p in $problemas) { Log 'ERROR' $p }
        Limpiar; exit 1
    }

    Log 'OK' ("respaldo restaurable: {0} usuarios, {1} cuentas de Drive con token, {2} archivos, {3} entradas de bitácora" -f `
        $usuarios, $tokens, $archivos, $bitacora)
    Limpiar
    exit 0

} catch {
    Log 'ERROR' "la verificación falló: $($_.Exception.Message)"
    Limpiar
    exit 1
}
