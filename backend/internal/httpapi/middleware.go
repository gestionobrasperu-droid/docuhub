package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/docuhub/docuhub/internal/crypto"
	"github.com/docuhub/docuhub/internal/models"
	"github.com/docuhub/docuhub/internal/repo"
	"github.com/google/uuid"
)

type ctxKey string

const (
	ctxUser      ctxKey = "user"
	ctxSessionID ctxKey = "session_id"
)

// userFrom devuelve el usuario autenticado; nil si la ruta es pública.
func userFrom(ctx context.Context) *models.User {
	u, _ := ctx.Value(ctxUser).(*models.User)
	return u
}

func sessionIDFrom(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(ctxSessionID).(uuid.UUID)
	return id
}

// authenticate resuelve la cookie de sesión. No corta la petición: deja pasar
// al usuario anónimo para que cada ruta decida (las públicas existen).
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(s.cfg.CookieName)
		if err != nil || cookie.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		user, sid, err := s.repo.UserBySessionToken(r.Context(), crypto.HashToken(cookie.Value))
		if err != nil {
			if !errors.Is(err, repo.ErrNotFound) {
				slog.Error("fallo al resolver la sesión", "error", err)
			}
			s.clearSessionCookie(w)
			next.ServeHTTP(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), ctxUser, user)
		ctx = context.WithValue(ctx, ctxSessionID, sid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireAuth corta con 401 si no hay sesión.
func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if userFrom(r.Context()) == nil {
			writeErrCode(w, http.StatusUnauthorized, "no_auth", "Tu sesión expiró. Vuelve a entrar.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireRole exige un rol global mínimo.
func requireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := userFrom(r.Context())
			if u == nil {
				writeErrCode(w, http.StatusUnauthorized, "no_auth", "Tu sesión expiró. Vuelve a entrar.")
				return
			}
			if !u.IsAtLeast(role) {
				writeErr(w, http.StatusForbidden, "No tienes permisos para esta operación")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---------------------------------------------------------- observabilidad --

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Flush permite que el streaming de descargas llegue al navegador sin esperar
// a que se llene el buffer.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)

		// Las descargas largas se registran en nivel info; el resto en debug
		// para no llenar el log con el ruido del frontend.
		level := slog.LevelDebug
		if sw.status >= 400 || time.Since(start) > 3*time.Second {
			level = slog.LevelInfo
		}
		slog.Log(r.Context(), level, "http",
			"metodo", r.Method,
			"ruta", r.URL.Path,
			"estado", sw.status,
			"ms", time.Since(start).Milliseconds(),
			"ip", clientIP(r, s.cfg.TrustProxyHeaders))
	})
}

func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic en el handler", "error", rec, "ruta", r.URL.Path)
				writeErr(w, http.StatusInternalServerError, "Error interno del servidor")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// securityHeaders endurece el navegador. La CSP permite 'unsafe-inline' en
// estilos porque el frontend compilado inyecta estilos críticos.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; "+
				"script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// ------------------------------------------------------------ rate limit ---

// rateLimiter es un contador por ventana fija en memoria. Suficiente para una
// instancia única; si algún día hay varias, se cambia por Redis.
type rateLimiter struct {
	mu     sync.Mutex
	hits   map[string]*bucket
	limit  int
	window time.Duration
	lastGC time.Time
}

type bucket struct {
	count int
	reset time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		hits:   make(map[string]*bucket),
		limit:  limit,
		window: window,
		lastGC: time.Now(),
	}
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	if now.Sub(rl.lastGC) > 10*time.Minute {
		for k, b := range rl.hits {
			if now.After(b.reset) {
				delete(rl.hits, k)
			}
		}
		rl.lastGC = now
	}

	b, ok := rl.hits[key]
	if !ok || now.After(b.reset) {
		rl.hits[key] = &bucket{count: 1, reset: now.Add(rl.window)}
		return true
	}
	b.count++
	return b.count <= rl.limit
}

// limit protege endpoints sensibles (login, creación de enlaces) del abuso.
func (s *Server) limit(rl *rateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := clientIP(r, s.cfg.TrustProxyHeaders)
			if u := userFrom(r.Context()); u != nil {
				key = u.ID.String()
			}
			if !rl.allow(key) {
				w.Header().Set("Retry-After", "60")
				writeErr(w, http.StatusTooManyRequests,
					"Demasiados intentos. Espera un minuto y vuelve a probar.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
