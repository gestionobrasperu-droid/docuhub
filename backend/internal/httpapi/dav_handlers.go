package httpapi

import (
	"context"
	"encoding/base64"
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
	return s.davHandlerPrefix("/dav")
}

// davHandlerPrefix sirve el mismo sistema de archivos bajo el prefijo que se
// le indique. Hacen falta dos: /dav para quien lo pida explicitamente, y la
// raiz para el redirector de Windows, que no acepta otra cosa.
func (s *Server) davHandlerPrefix(prefix string) http.Handler {
	ls := webdav.NewMemLS()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, tokenID, ok := s.davAuth(w, r)
		if !ok {
			return
		}

		h := &webdav.Handler{
			Prefix:     prefix,
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
		map[string]any{"letra": letra, "para": user.Email})

	s.entregarInstalador(w, r, user.Email, token, letra, nombre)
}

// handleInstaladorUsuario deja que un administrador prepare el instalador de
// otra persona. Repartir equipos a diez empleados no debería obligar a que
// cada uno entre a la plataforma y se lo descargue: el administrador se lo
// manda ya hecho.
func (s *Server) handleInstaladorUsuario(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	destino, err := s.repo.UserByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "El usuario no existe")
		return
	}
	if destino.Status != "active" {
		writeErr(w, http.StatusConflict, "La cuenta está suspendida: reactívala antes de conectar un equipo")
		return
	}

	nombre := strings.TrimSpace(r.URL.Query().Get("equipo"))
	if nombre == "" {
		nombre = "Equipo de " + strings.Split(destino.Email, "@")[0]
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
	if _, err := s.repo.CreateDeviceToken(r.Context(), destino.ID, crypto.HashToken(token), nombre, nil); err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo registrar el equipo")
		return
	}

	s.audit(r, "device.connect", "user", destino.ID.String(), nombre, true,
		map[string]any{"letra": letra, "para": destino.Email, "emitido_por_admin": true})

	s.entregarInstalador(w, r, destino.Email, token, letra, nombre)
}

// entregarInstalador devuelve el .bat de doble clic o, si se pide, el .ps1
// equivalente.

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

// entregarInstalador devuelve el .bat de doble clic o, si se pide, el .ps1
// equivalente.
func (s *Server) entregarInstalador(w http.ResponseWriter, r *http.Request, correo, token, letra, equipo string) {
	w.Header().Set("Cache-Control", "no-store")

	if strings.EqualFold(r.URL.Query().Get("formato"), "ps1") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="Conectar-DocuHub.ps1"`)
		// El BOM hace que PowerShell 5.1 lea bien los acentos.
		_, _ = w.Write([]byte("\xEF\xBB\xBF" + scriptCompleto(s.cfg.BaseURL, correo, token, letra, equipo)))
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="Conectar-DocuHub.bat"`)
	_, _ = w.Write([]byte(envolverEnBat(
		scriptPreparar(),
		scriptMontar(s.cfg.BaseURL, correo, token, letra, equipo),
	)))
}

// codificar prepara un script para -EncodedCommand, que espera base64 de
// UTF-16LE. Así el script viaja entero por la línea de comandos sin que cmd le
// toque una sola comilla.
func codificar(script string) string {
	utf16le := make([]byte, 0, len(script)*2)
	for _, r := range script {
		if r > 0xFFFF {
			r = '?' // fuera del plano básico: no aparece en estos scripts
		}
		utf16le = append(utf16le, byte(r), byte(r>>8))
	}
	return base64.StdEncoding.EncodeToString(utf16le)
}

// envolverEnBat arma el instalador de doble clic.
//
// La parte delicada es que son DOS contextos distintos y no se pueden mezclar:
//
//   - La preparación del sistema (servicio WebClient, límite de tamaño)
//     necesita permisos de administrador.
//   - El montaje de la unidad NO debe hacerse como administrador. Windows no
//     comparte las unidades de red entre la sesión elevada y la normal: si se
//     monta elevado, la unidad existe para el administrador y el usuario no la
//     ve en su Explorador.
//
// Por eso el .bat eleva solo un proceso aparte para la preparación, y hace el
// montaje en su propio proceso, que es el del usuario. Además deja activado
// EnableLinkedConnections, que es lo que hace que una unidad montada en un
// contexto se vea en el otro.
func envolverEnBat(preparar, montar string) string {
	return "@echo off\r\n" +
		"title Conectar DocuHub\r\n" +
		"chcp 65001 >nul\r\n" +
		"echo.\r\n" +
		"echo   Conectando este equipo con DocuHub...\r\n" +
		"echo.\r\n" +
		"\r\n" +
		":: Paso 1: preparar Windows. Pide permisos porque toca un servicio y el\r\n" +
		":: registro. Si se rechaza, se sigue igual: solo se pierde poder mover\r\n" +
		":: archivos de mas de 50 MB por la unidad.\r\n" +
		"net session >nul 2>&1\r\n" +
		"if %errorlevel% EQU 0 (\r\n" +
		"  powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand " + codificar(preparar) + "\r\n" +
		") else (\r\n" +
		"  powershell -NoProfile -Command \"try { Start-Process powershell -ArgumentList '-NoProfile','-ExecutionPolicy','Bypass','-EncodedCommand','" + codificar(preparar) + "' -Verb RunAs -Wait -ErrorAction Stop } catch { Write-Host '   Sin permisos: el limite por archivo se queda en 50 MB' -ForegroundColor Yellow }\"\r\n" +
		")\r\n" +
		"\r\n" +
		":: Paso 2: montar la unidad. Va SIN elevar, a proposito: una unidad\r\n" +
		":: montada como administrador no aparece en el Explorador del usuario.\r\n" +
		"powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand " + codificar(montar) + "\r\n"
}

// scriptPreparar deja Windows listo para montar unidades WebDAV. Es la única
// parte que necesita permisos de administrador.
func scriptPreparar() string {
	return `
Write-Host '>> Preparando Windows' -ForegroundColor Cyan

# El cliente WebDAV viene parado y en manual en muchas instalaciones.
$svc = Get-Service WebClient -ErrorAction SilentlyContinue
if (-not $svc) {
    Write-Host '   [!] Este Windows no trae el cliente WebDAV' -ForegroundColor Yellow
} else {
    Set-Service WebClient -StartupType Automatic -ErrorAction SilentlyContinue
    if ($svc.Status -ne 'Running') { Start-Service WebClient -ErrorAction SilentlyContinue }
    Write-Host '   [ok] cliente de red activado y en arranque automatico' -ForegroundColor Green
}

# Windows rechaza por WebDAV los archivos de mas de 50 MB. Para planos y videos
# de obra eso no sirve de nada; 4 GB es el maximo que admite.
try {
    $p = 'HKLM:\SYSTEM\CurrentControlSet\Services\WebClient\Parameters'
    Set-ItemProperty -Path $p -Name 'FileSizeLimitInBytes' -Value 4294967295 -Type DWord -ErrorAction Stop
    Set-ItemProperty -Path $p -Name 'FsCtlRequestTimeoutInSec' -Value 600 -Type DWord -ErrorAction SilentlyContinue
    Write-Host '   [ok] limite por archivo: 4 GB' -ForegroundColor Green
} catch {
    Write-Host "   [!] no se pudo subir el limite: $($_.Exception.Message)" -ForegroundColor Yellow
}

# Sin esto, una unidad montada desde una ventana de administrador no se ve en
# el Explorador normal, y al reves. Es la causa clasica de "la monte y no
# aparece".
try {
    $pol = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System'
    Set-ItemProperty -Path $pol -Name 'EnableLinkedConnections' -Value 1 -Type DWord -ErrorAction Stop
    Write-Host '   [ok] las unidades se comparten entre sesiones' -ForegroundColor Green
} catch { }

Restart-Service WebClient -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 2
`
}

// scriptMontar hace el trabajo visible: montar la unidad y comprobarla. Corre
// en la sesión del usuario.
func scriptMontar(baseURL, correo, token, letra, equipo string) string {
	host := strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://")

	return fmt.Sprintf(`
$servidor = '%s'
$usuario  = '%s'
$clave    = '%s'
$letra    = '%s'
$equipo   = '%s'

function Fin($codigo) {
    Write-Host ''
    Write-Host 'Pulsa una tecla para cerrar...' -ForegroundColor DarkGray
    $null = $Host.UI.RawUI.ReadKey('NoEcho,IncludeKeyDown')
    exit $codigo
}

Write-Host ''
Write-Host '  ========================================' -ForegroundColor Blue
Write-Host '    DocuHub - conectar este equipo' -ForegroundColor White
Write-Host '  ========================================' -ForegroundColor Blue
Write-Host ("   Cuenta : {0}" -f $usuario)
Write-Host ("   Equipo : {0}" -f $equipo)
Write-Host ("   Unidad : {0}:" -f $letra)
Write-Host ''
Write-Host (">> Montando la unidad {0}:" -f $letra) -ForegroundColor Cyan

# Si ya habia un montaje anterior se retira, para no dejar dos apuntando al
# mismo sitio.
cmd /c "net use ${letra}: /delete /y" 2>&1 | Out-Null

# La credencial se guarda en el Administrador de credenciales de Windows, que
# sobrevive a los reinicios. Sin esto, la unidad reaparece al arrancar pero
# pide usuario y contrasena en cuanto se abre, que es justo lo que se quiere
# evitar.
cmd /c "cmdkey /delete:$servidor" 2>&1 | Out-Null
cmd /c "cmdkey /add:$servidor /user:$usuario /pass:$clave" 2>&1 | Out-Null

$ruta = "\\$servidor@SSL\"
$salida = cmd /c "net use ${letra}: $ruta /user:$usuario $clave /persistent:yes" 2>&1

if ($LASTEXITCODE -ne 0) {
    Write-Host ''
    Write-Host '   No se pudo montar la unidad:' -ForegroundColor Red
    $salida | ForEach-Object { Write-Host "     $_" -ForegroundColor DarkGray }
    Write-Host ''
    Write-Host '   Causas habituales:' -ForegroundColor Yellow
    Write-Host '     - La plataforma esta apagada ahora mismo.'
    Write-Host '     - El servicio WebClient no arranco (reinicia el equipo).'
    Write-Host '     - La red de la oficina o el antivirus bloquean WebDAV.'
    Fin 1
}
Write-Host ("   [ok] unidad {0}: conectada" -f $letra) -ForegroundColor Green

Start-Sleep -Seconds 2
if (Test-Path ("{0}:\" -f $letra)) {
    $n = (Get-ChildItem ("{0}:\" -f $letra) -ErrorAction SilentlyContinue | Measure-Object).Count
    Write-Host ("   [ok] se ve el contenido ({0} elementos en la raiz)" -f $n) -ForegroundColor Green
} else {
    Write-Host '   [!] la unidad aparece pero aun no responde; dale unos segundos' -ForegroundColor Yellow
}

# --- Que sobreviva a los reinicios -----------------------------------------
# /persistent:yes hace que Windows recuerde la unidad, pero las unidades WebDAV
# suelen reaparecer como "desconectadas" hasta que algo las toca, y si el
# servicio WebClient todavia no arranco, el intento falla. Una tarea al iniciar
# sesion la reconecta sola, con un margen para que la red este lista.
Write-Host ''
Write-Host '>> Dejando la conexion permanente' -ForegroundColor Cyan

$carpeta = Join-Path $env:LOCALAPPDATA 'DocuHub'
if (-not (Test-Path $carpeta)) { New-Item -ItemType Directory -Path $carpeta -Force | Out-Null }
$reconectar = Join-Path $carpeta 'reconectar.cmd'

# El script de reconexion no lleva la contrasena: la toma del Administrador de
# credenciales, donde quedo guardada arriba.
$contenido = @"
@echo off
rem Reconecta la unidad de DocuHub al iniciar sesion. Lo creo el instalador.
timeout /t 20 /nobreak >nul
sc query WebClient | find "RUNNING" >nul || net start WebClient >nul 2>&1
if exist ${letra}:\ exit /b 0
net use ${letra}: \\$servidor@SSL\ /persistent:yes >nul 2>&1
"@
Set-Content -Path $reconectar -Value $contenido -Encoding ASCII

schtasks /delete /tn "DocuHub-Reconectar" /f 2>&1 | Out-Null
$alta = & schtasks @('/create', '/tn', 'DocuHub-Reconectar', '/tr', $reconectar, '/sc', 'ONLOGON', '/f') 2>&1
if ($LASTEXITCODE -eq 0) {
    Write-Host '   [ok] se reconectara sola en cada inicio de sesion' -ForegroundColor Green
} else {
    Write-Host '   [!] no se pudo programar la reconexion automatica' -ForegroundColor Yellow
    Write-Host "       $alta" -ForegroundColor DarkGray
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
`, host, correo, token, letra, equipo)
}

// scriptCompleto es la versión .ps1, para quien tenga los .bat bloqueados por
// política de la empresa. Lleva las dos partes seguidas; si se ejecuta sin
// permisos, la preparación se salta sola y solo se pierde el límite de 4 GB.
func scriptCompleto(baseURL, correo, token, letra, equipo string) string {
	return `# Conectar este equipo con DocuHub
#
# Monta la plataforma documental como una unidad de red usando la cuenta de
# abajo. Lo genero la propia plataforma: la credencial pertenece solo a este
# equipo y se revoca desde "Mi cuenta" sin tocar la contrasena.
#
# Clic derecho -> Ejecutar con PowerShell. Como administrador la primera vez,
# para subir el limite de archivo de 50 MB a 4 GB.

$esAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
    ).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

if ($esAdmin) {
` + scriptPreparar() + `
    Write-Host ''
    Write-Host '   AVISO: al ejecutar como administrador, la unidad se monta para' -ForegroundColor Yellow
    Write-Host '   el administrador. Si luego no la ves en tu Explorador, vuelve a' -ForegroundColor Yellow
    Write-Host '   ejecutar este archivo SIN permisos de administrador.' -ForegroundColor Yellow
} else {
    Write-Host '>> Sin permisos de administrador: no se ajusta el limite de 50 MB' -ForegroundColor Yellow
}
` + scriptMontar(baseURL, correo, token, letra, equipo)
}

// davDiscovery responde al sondeo que hace el cliente WebDAV de Windows sobre
// la raíz del sitio antes de montar un subdirectorio: si ahí no ve las
// cabeceras DAV, decide que el servidor no habla WebDAV y aborta con "no se
// encuentra el nombre de red".
//
// Va como middleware y no como ruta a propósito. Registrar OPTIONS "/" en el
// router hace que chi deje de entregar GET "/" al handler de la aplicación y
// empiece a responder 405: la web entera se cae. Aquí se intercepta antes de
// llegar al enrutado y el resto del tráfico sigue su camino intacto.
func davDiscovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions && (r.URL.Path == "/" || r.URL.Path == "") {
			h := w.Header()
			h.Set("DAV", "1, 2")
			h.Set("MS-Author-Via", "DAV")
			h.Set("Allow", "OPTIONS, GET, HEAD, POST, PUT, DELETE, PROPFIND, PROPPATCH, MKCOL, COPY, MOVE, LOCK, UNLOCK")
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// esClienteWebDAV reconoce al redirector de Windows, que es quien monta las
// unidades de red. Se identifica siempre con el mismo User-Agent.
func esClienteWebDAV(r *http.Request) bool {
	ua := r.UserAgent()
	return strings.Contains(ua, "Microsoft-WebDAV-MiniRedir") ||
		strings.Contains(ua, "DavClnt") ||
		strings.Contains(ua, "Microsoft Office Existence Discovery")
}

// davEnRaiz deja que el redirector de Windows hable WebDAV directamente con la
// raíz del sitio.
//
// Hace falta porque ese cliente, antes de montar nada, pide GET / y mira lo
// que recibe: si es una página web, concluye que el servidor no habla WebDAV y
// aborta con "no se encuentra el nombre de red", sin llegar nunca a /dav. Los
// registros del servidor lo enseñan sin lugar a dudas.
//
// Se distingue por User-Agent, así que un navegador sigue viendo la web
// exactamente igual: para él no cambia nada.
func (s *Server) davEnRaiz(siguiente http.Handler) http.Handler {
	dav := s.davHandlerPrefix("")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if esClienteWebDAV(r) {
			dav.ServeHTTP(w, r)
			return
		}
		siguiente.ServeHTTP(w, r)
	})
}
