package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/docuhub/docuhub/internal/crypto"
	"github.com/docuhub/docuhub/internal/davfs"
	"github.com/docuhub/docuhub/internal/models"
	"github.com/docuhub/docuhub/internal/repo"
	"github.com/google/uuid"
	"golang.org/x/net/webdav"
)

// Montaje como unidad de red.
//
// Windows sabe montar WebDAV de serie, sin instalar nada: una letra de unidad
// que por dentro habla HTTPS con la plataforma. La ventaja sobre montar Google
// Drive directamente es que aquí cada lectura y cada escritura pasa por los
// permisos, las cuotas y la bitácora de DocuHub.
//
// La autenticación es Basic, que solo es aceptable porque todo viaja por
// HTTPS. La contraseña que se usa no es la del usuario sino un token por
// equipo: Windows la guarda indefinidamente en su Administrador de
// credenciales, y un token se revoca sin tocar la cuenta.

func (s *Server) davHandler() http.Handler {
	ls := webdav.NewMemLS()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, tokenID, ok := s.davAuth(w, r)
		if !ok {
			return
		}

		h := &webdav.Handler{
			Prefix:     "/dav",
			FileSystem: davfs.New(s.davDeps(r), user),
			LockSystem: ls,
			Logger: func(req *http.Request, err error) {
				if err != nil && !errors.Is(err, context.Canceled) {
					slog.Debug("webdav", "metodo", req.Method, "ruta", req.URL.Path, "error", err)
				}
			},
		}

		if tokenID != uuid.Nil {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.repo.TouchDeviceToken(ctx, tokenID, clientIP(r, s.cfg.TrustProxyHeaders)); err != nil {
					slog.Debug("webdav: no se pudo marcar el uso del equipo", "error", err)
				}
			}()
		}

		h.ServeHTTP(w, r)
	})
}

// davDeps enlaza el sistema de archivos con el resto de la plataforma.
func (s *Server) davDeps(r *http.Request) davfs.Deps {
	return davfs.Deps{
		Repo:  s.repo,
		Drive: s.drive,
		Quota: s.quota,
		Audit: func(action, resType, resID, resName string, ok bool, meta map[string]any) {
			s.audit(r, action, resType, resID, resName, ok, meta)
		},
	}
}

// davAuth acepta el token del equipo o, como alternativa, la contraseña real
// del usuario. Lo primero es lo que usa el instalador; lo segundo permite
// montar la unidad a mano sin generar nada.
func (s *Server) davAuth(w http.ResponseWriter, r *http.Request) (*models.User, uuid.UUID, bool) {
	rechazar := func() (*models.User, uuid.UUID, bool) {
		w.Header().Set("WWW-Authenticate", `Basic realm="DocuHub", charset="UTF-8"`)
		http.Error(w, "Credenciales no válidas", http.StatusUnauthorized)
		return nil, uuid.Nil, false
	}

	correo, clave, hay := r.BasicAuth()
	if !hay || correo == "" || clave == "" {
		return rechazar()
	}

	// 1) Token de equipo.
	if user, tokenID, err := s.repo.UserByDeviceToken(r.Context(), crypto.HashToken(clave)); err == nil {
		if strings.EqualFold(user.Email, correo) {
			return user, tokenID, true
		}
		return rechazar()
	} else if !errors.Is(err, repo.ErrNotFound) {
		http.Error(w, "Error al validar las credenciales", http.StatusInternalServerError)
		return nil, uuid.Nil, false
	}

	// 2) Contraseña del usuario.
	user, err := s.repo.UserByEmail(r.Context(), correo)
	if err != nil {
		return rechazar()
	}
	valida, err := crypto.VerifyPassword(clave, user.PasswordHash)
	if err != nil || !valida || user.Status != "active" {
		s.audit(r, "dav.auth", "user", "", correo, false, map[string]any{"via": "webdav"})
		return rechazar()
	}
	return user, uuid.Nil, true
}

// ------------------------------------------------- instalador del equipo ---

type conectarRequest struct {
	DeviceName string `json:"device_name"`
	Letter     string `json:"letter"`
}

// handleConectarPC emite un token nuevo y devuelve un script de PowerShell que
// deja la unidad montada en ese equipo. El token solo existe dentro del script
// que se descarga: no se guarda en claro en ningún sitio ni se vuelve a
// mostrar.
func (s *Server) handleConectarPC(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	nombre := strings.TrimSpace(r.URL.Query().Get("equipo"))
	if nombre == "" {
		nombre = "Equipo de " + strings.Split(user.Email, "@")[0]
	}
	if len(nombre) > 80 {
		nombre = nombre[:80]
	}
	letra := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("letra")))
	if len(letra) != 1 || letra[0] < 'D' || letra[0] > 'Z' {
		letra = "W"
	}

	token, err := crypto.NewToken(24)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo generar la credencial del equipo")
		return
	}
	if _, err := s.repo.CreateDeviceToken(r.Context(), user.ID, crypto.HashToken(token), nombre, nil); err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo registrar el equipo")
		return
	}

	s.audit(r, "device.connect", "user", user.ID.String(), nombre, true,
		map[string]any{"letra": letra})

	script := scriptConexion(s.cfg.BaseURL, user.Email, token, letra, nombre)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="Conectar-DocuHub.ps1"`)
	w.Header().Set("Cache-Control", "no-store")
	// El guion BOM hace que PowerShell 5.1 lea bien los acentos del script.
	_, _ = w.Write([]byte("\xEF\xBB\xBF" + script))
}

func (s *Server) handleListarEquipos(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	equipos, err := s.repo.ListDeviceTokens(r.Context(), user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudieron listar tus equipos")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": equipos})
}

func (s *Server) handleRevocarEquipo(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.repo.RevokeDeviceToken(r.Context(), user.ID, id); err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo desconectar el equipo")
		return
	}
	s.audit(r, "device.revoke", "user", user.ID.String(), id.String(), true, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// scriptConexion arma el instalador. Va en español y explica lo que hace,
// porque quien lo ejecuta es una persona que acaba de descargarlo y tiene
// derecho a saber qué va a tocar en su equipo.
func scriptConexion(baseURL, correo, token, letra, equipo string) string {
	host := strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://")

	return fmt.Sprintf(`# Conectar este equipo con DocuHub
#
# Monta la plataforma documental como una unidad de red (%s:) usando la cuenta
# %s. Lo genero la propia plataforma: la credencial de abajo
# pertenece solo a este equipo y se puede revocar desde "Mi cuenta" sin tocar
# la contrasena.
#
# Que hace, en orden:
#   1. Activa el cliente WebDAV de Windows y lo deja en arranque automatico
#   2. Sube el limite de tamano de archivo (Windows trae 50 MB por defecto)
#   3. Monta la unidad %s: de forma permanente
#
# Hace falta ejecutarlo como administrador solo la primera vez, por los
# pasos 1 y 2.

$ErrorActionPreference = 'Continue'
$Host.UI.RawUI.WindowTitle = 'Conectar DocuHub'

$servidor = '%s'
$usuario  = '%s'
$clave    = '%s'
$letra    = '%s'
$equipo   = '%s'

function Paso($t) { Write-Host ''; Write-Host ">> $t" -ForegroundColor Cyan }
function Ok($t)   { Write-Host "   [ok] $t" -ForegroundColor Green }
function Aviso($t){ Write-Host "   [!]  $t" -ForegroundColor Yellow }

function Fin($codigo) {
    Write-Host ''
    Write-Host 'Pulsa una tecla para cerrar...' -ForegroundColor DarkGray
    $null = $Host.UI.RawUI.ReadKey('NoEcho,IncludeKeyDown')
    exit $codigo
}

Clear-Host
Write-Host ''
Write-Host '  ========================================' -ForegroundColor Blue
Write-Host '    DocuHub - conectar este equipo' -ForegroundColor White
Write-Host '  ========================================' -ForegroundColor Blue
Write-Host ("   Cuenta : {0}" -f $usuario)
Write-Host ("   Unidad : {0}:" -f $letra)

$esAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
    ).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

# --- 1. Cliente WebDAV ------------------------------------------------------
Paso 'Preparando el cliente de red de Windows'
$svc = Get-Service WebClient -ErrorAction SilentlyContinue
if (-not $svc) {
    Aviso 'Este Windows no trae el cliente WebDAV (ediciones Server lo traen como caracteristica opcional).'
} else {
    if ($esAdmin) {
        Set-Service WebClient -StartupType Automatic -ErrorAction SilentlyContinue
        Ok 'arrancara solo con el equipo'
    }
    if ($svc.Status -ne 'Running') {
        try { Start-Service WebClient -ErrorAction Stop; Ok 'servicio iniciado' }
        catch { Aviso 'no se pudo iniciar el servicio WebClient (ejecuta como administrador)' }
    } else {
        Ok 'servicio ya en marcha'
    }
}

# --- 2. Limite de tamano ----------------------------------------------------
# Windows rechaza por defecto los archivos de mas de 50 MB por WebDAV, que
# para planos y videos de obra no sirve de nada. 4 GB es el maximo admitido.
Paso 'Ajustando el limite de tamano de archivo'
if ($esAdmin) {
    try {
        $p = 'HKLM:\SYSTEM\CurrentControlSet\Services\WebClient\Parameters'
        Set-ItemProperty -Path $p -Name 'FileSizeLimitInBytes' -Value 4294967295 -Type DWord -ErrorAction Stop
        Set-ItemProperty -Path $p -Name 'FsCtlRequestTimeoutInSec' -Value 600 -Type DWord -ErrorAction SilentlyContinue
        Ok 'hasta 4 GB por archivo'
        Restart-Service WebClient -Force -ErrorAction SilentlyContinue
    } catch {
        Aviso "no se pudo ajustar: $($_.Exception.Message)"
    }
} else {
    Aviso 'sin permisos de administrador: el limite se queda en 50 MB por archivo'
    Aviso 'vuelve a ejecutar este script como administrador para subirlo a 4 GB'
}

# --- 3. Montar la unidad ----------------------------------------------------
Paso ("Montando la unidad {0}:" -f $letra)

# Si ya existia un montaje anterior, se retira para no dejar dos.
cmd /c "net use ${letra}: /delete /y" 2>&1 | Out-Null

$ruta = "\\$servidor@SSL\DavWWWRoot\dav"
$salida = cmd /c "net use ${letra}: $ruta /user:$usuario $clave /persistent:yes" 2>&1

if ($LASTEXITCODE -eq 0) {
    Ok ("unidad {0}: conectada" -f $letra)
} else {
    Write-Host ''
    Write-Host '   No se pudo montar la unidad:' -ForegroundColor Red
    $salida | ForEach-Object { Write-Host "     $_" -ForegroundColor DarkGray }
    Write-Host ''
    Write-Host '   Causas habituales:' -ForegroundColor Yellow
    Write-Host '     - El servicio WebClient no esta en marcha (paso 1).'
    Write-Host '     - La plataforma esta apagada ahora mismo.'
    Write-Host '     - Un antivirus o la red de la oficina bloquean WebDAV.'
    Fin 1
}

# --- Comprobacion -----------------------------------------------------------
Paso 'Comprobando'
Start-Sleep -Seconds 2
if (Test-Path ("{0}:\" -f $letra)) {
    $n = (Get-ChildItem ("{0}:\" -f $letra) -ErrorAction SilentlyContinue | Measure-Object).Count
    Ok ("se ve el contenido ({0} elementos en la raiz)" -f $n)
} else {
    Aviso 'la unidad aparece pero todavia no responde; dale unos segundos'
}

Write-Host ''
Write-Host '  ----------------------------------------' -ForegroundColor DarkGray
Write-Host ("   Listo. Abre el Explorador y busca la unidad {0}:" -f $letra) -ForegroundColor Green
Write-Host '  ----------------------------------------' -ForegroundColor DarkGray
Write-Host ''
Write-Host '   Se reconecta sola cada vez que inicies sesion en Windows.' -ForegroundColor DarkGray
Write-Host '   Los archivos NO ocupan espacio en este equipo: se descargan' -ForegroundColor DarkGray
Write-Host '   al abrirlos y se suben al guardarlos.' -ForegroundColor DarkGray
Write-Host ''
Write-Host ("   Si pierdes este equipo, entra a {0} y" -f $servidor) -ForegroundColor DarkGray
Write-Host '   desconectalo desde Mi cuenta: la credencial deja de valer.' -ForegroundColor DarkGray

Fin 0
`, letra, correo, letra, host, correo, token, letra, equipo)
}
