package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/docuhub/docuhub/internal/config"
	"github.com/docuhub/docuhub/internal/crypto"
	"github.com/docuhub/docuhub/internal/drive"
	"github.com/docuhub/docuhub/internal/quota"
	"github.com/docuhub/docuhub/internal/repo"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Server struct {
	cfg   *config.Config
	repo  *repo.Repo
	drive *drive.Client
	enc   *crypto.Encryptor
	quota *quota.Checker

	loginLimiter  *rateLimiter
	publicLimiter *rateLimiter

	// Estados pendientes del flujo OAuth de Google, con vencimiento corto.
	oauthMu    sync.Mutex
	oauthState map[string]time.Time

	// Claves temporales entregadas tras desbloquear un enlace con contraseña.
	grantMu     sync.Mutex
	shareGrants map[string]shareGrant

	handler http.Handler
}

// shareGrant autoriza descargas de un enlace protegido durante media hora.
type shareGrant struct {
	shareID uuid.UUID
	expires time.Time
}

func New(cfg *config.Config, r *repo.Repo, d *drive.Client, enc *crypto.Encryptor, q *quota.Checker, static http.Handler) *Server {
	s := &Server{
		cfg:           cfg,
		repo:          r,
		drive:         d,
		enc:           enc,
		quota:         q,
		loginLimiter:  newRateLimiter(10, time.Minute),
		publicLimiter: newRateLimiter(60, time.Minute),
		oauthState:    make(map[string]time.Time),
		shareGrants:   make(map[string]shareGrant),
	}
	s.handler = s.routes(static)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// WebDAV usa metodos HTTP que chi no conoce de serie: sin registrarlos,
// el router responde 405 a todo lo que no sea GET o PUT y Windows no puede
// ni listar la unidad.
func init() {
	for _, m := range []string{"PROPFIND", "PROPPATCH", "MKCOL", "COPY", "MOVE", "LOCK", "UNLOCK"} {
		chi.RegisterMethod(m)
	}
}

func (s *Server) routes(static http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(recoverPanics, s.logRequests, securityHeaders, davDiscovery, s.authenticate)

	r.Get("/healthz", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
		defer cancel()
		if err := s.repo.DB().Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "degradado", "database": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "time": time.Now()})
	})

	r.Route("/api", func(api chi.Router) {
		// ------------------------------------------------------ sesión --
		api.Route("/auth", func(a chi.Router) {
			a.With(s.limit(s.loginLimiter)).Post("/login", s.handleLogin)
			a.Post("/logout", s.handleLogout)
			a.With(requireAuth).Get("/me", s.handleMe)
			a.With(requireAuth).Post("/password", s.handleChangePassword)
		})

		// ------------------------------------------------ enlaces públicos --
		// Sin sesión: el token del enlace es la credencial.
		api.Route("/public", func(p chi.Router) {
			p.Use(s.limit(s.publicLimiter))
			p.Get("/{token}", s.handlePublicInfo)
			p.Post("/{token}/unlock", s.handlePublicUnlock)
			p.Get("/{token}/download", s.handlePublicDownload)
		})

		// --------------------------------------------- área autenticada --
		api.Group(func(priv chi.Router) {
			priv.Use(requireAuth)

			priv.Get("/folders/root", s.handleRootFolder)
			priv.Get("/folders/{id}", s.handleFolderContents)
			priv.Post("/folders", s.handleCreateFolder)
			priv.Patch("/folders/{id}", s.handleRenameFolder)
			priv.Delete("/folders/{id}", s.handleDeleteFolder)

			priv.Get("/files/{id}", s.handleFileInfo)
			priv.Patch("/files/{id}", s.handleUpdateFile)
			priv.Delete("/files/{id}", s.handleDeleteFile)
			priv.Get("/files/{id}/download", s.handleDownload)
			priv.Get("/search", s.handleSearch)

			// Pantalla de inicio del usuario: su consumo y sus archivos.
			priv.Get("/me/overview", s.handleMyOverview)

			// Equipos conectados como unidad de red.
			priv.Get("/me/conectar-pc", s.handleConectarPC)
			priv.Get("/me/equipos", s.handleListarEquipos)
			priv.Delete("/me/equipos/{id}", s.handleRevocarEquipo)

			priv.Post("/uploads", s.handleInitUpload)
			priv.Get("/uploads", s.handleListUploads)
			priv.Get("/uploads/{id}", s.handleUploadStatus)
			priv.Put("/uploads/{id}/chunk", s.handleUploadChunk)
			priv.Delete("/uploads/{id}", s.handleAbortUpload)

			priv.Get("/shares", s.handleListShares)
			priv.With(s.limit(s.loginLimiter)).Post("/shares", s.handleCreateShare)
			priv.Delete("/shares/{id}", s.handleRevokeShare)

			priv.Get("/permissions", s.handleListPermissions)
			priv.Post("/permissions", s.handleGrantPermission)
			priv.Delete("/permissions/{id}", s.handleRevokePermission)

			// ------------------------------------------ administración --
			priv.Route("/admin", func(adm chi.Router) {
				adm.Use(requireRole("manager"))

				adm.Get("/stats", s.handleStats)
				adm.Get("/audit", s.handleAudit)

				adm.Get("/users", s.handleListUsers)
				adm.Post("/users", s.handleCreateUser)
				adm.Patch("/users/{id}", s.handleUpdateUser)
				adm.Post("/users/{id}/password", s.handleResetPassword)
				adm.Get("/users/{id}/instalador", s.handleInstaladorUsuario)
				adm.With(requireRole("admin")).Delete("/users/{id}", s.handleDeleteUser)

				adm.Route("/drive", func(dr chi.Router) {
					dr.Use(requireRole("admin"))
					dr.Get("/", s.handleDriveStatus)
					dr.Get("/connect", s.handleDriveConnect)
					// El callback de Google llega como navegación del
					// navegador, no por fetch. La cookie de sesión viaja
					// igual porque es SameSite=Lax y es un GET de nivel
					// superior, así que exigir sesión de administrador aquí
					// es seguro y además impide que un tercero complete el
					// flujo con un código robado.
					dr.Get("/callback", s.handleDriveCallback)
					dr.Post("/{id}/primary", s.handleDriveSetPrimary)
					dr.Post("/{id}/sync", s.handleDriveSync)
					dr.Post("/{id}/refresh", s.handleDriveRefreshQuota)
					dr.Delete("/{id}", s.handleDriveDisconnect)
				})
			})
		})
	})

	// Montaje como unidad de red. Va fuera de /api porque Windows pide la
	// raiz del recurso, y su autenticacion es Basic, no la cookie de sesion.
	r.Handle("/dav", s.davHandler())
	r.Handle("/dav/*", s.davHandler())

	// Atajo legible para compartir: /s/{token} abre el frontend público.
	r.Get("/s/{token}", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/#/s/"+chi.URLParam(req, "token"), http.StatusFound)
	})

	// Todo lo demás lo sirve el frontend compilado (SPA).
	if static != nil {
		r.NotFound(static.ServeHTTP)
	}
	return r
}

// ------------------------------------------------------------- utilidades --

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// audit registra sin bloquear la respuesta del usuario. Usa un contexto propio
// porque el de la petición puede cancelarse justo al terminar.
func (s *Server) audit(r *http.Request, action, resType, resID, resName string, success bool, meta map[string]any) {
	var actorID *uuid.UUID
	actorEmail := ""
	if u := userFrom(r.Context()); u != nil {
		id := u.ID
		actorID = &id
		actorEmail = u.Email
	}
	in := repo.AuditInput{
		ActorID:      actorID,
		ActorEmail:   actorEmail,
		Action:       action,
		ResourceType: resType,
		ResourceID:   resID,
		ResourceName: resName,
		IP:           clientIP(r, s.cfg.TrustProxyHeaders),
		UserAgent:    userAgent(r),
		Success:      success,
		Metadata:     meta,
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.repo.InsertAudit(ctx, in); err != nil {
			slog.Error("no se pudo registrar en la bitácora", "accion", action, "error", err)
		}
	}()
}

func parseUUID(w http.ResponseWriter, r *http.Request, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Identificador inválido")
		return uuid.Nil, false
	}
	return id, true
}

// RunMaintenance ejecuta la limpieza periódica mientras el servidor viva.
func (s *Server) RunMaintenance(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	s.maintenanceRound(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.maintenanceRound(ctx)
		}
	}
}

func (s *Server) maintenanceRound(ctx context.Context) {
	if n, err := s.repo.PurgeExpiredSessions(ctx); err != nil {
		slog.Error("limpieza de sesiones", "error", err)
	} else if n > 0 {
		slog.Info("sesiones vencidas eliminadas", "cantidad", n)
	}

	if n, err := s.repo.PurgeStaleUploads(ctx); err != nil {
		slog.Error("limpieza de subidas", "error", err)
	} else if n > 0 {
		slog.Info("subidas abandonadas cerradas", "cantidad", n)
	}

	// Refresca el espacio disponible en cada cuenta de Drive enlazada.
	accounts, err := s.repo.ListDriveAccounts(ctx)
	if err != nil {
		return
	}
	for _, acc := range accounts {
		if acc.Status != "active" {
			continue
		}
		q, err := s.drive.About(ctx, acc)
		if err != nil {
			slog.Warn("no se pudo consultar la cuota de Drive", "cuenta", acc.Email, "error", err)
			continue
		}
		if err := s.repo.SetAccountQuota(ctx, acc.ID, q.Limit, q.Usage); err != nil {
			slog.Error("no se pudo guardar la cuota", "error", err)
		}
	}
}

// oauthStateNew crea un state de un solo uso para el flujo de Google.
func (s *Server) oauthStateNew() (string, error) {
	tok, err := crypto.NewToken(24)
	if err != nil {
		return "", err
	}
	s.oauthMu.Lock()
	defer s.oauthMu.Unlock()

	now := time.Now()
	for k, exp := range s.oauthState {
		if now.After(exp) {
			delete(s.oauthState, k)
		}
	}
	s.oauthState[tok] = now.Add(10 * time.Minute)
	return tok, nil
}

func (s *Server) oauthStateConsume(tok string) bool {
	s.oauthMu.Lock()
	defer s.oauthMu.Unlock()

	exp, ok := s.oauthState[tok]
	if !ok {
		return false
	}
	delete(s.oauthState, tok)
	return time.Now().Before(exp)
}
