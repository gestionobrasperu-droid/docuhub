package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/docuhub/docuhub/internal/crypto"
	"github.com/docuhub/docuhub/internal/repo"
)

// Hash de descarte: se verifica contra él cuando el correo no existe, para que
// una cuenta inexistente tarde lo mismo que una con contraseña incorrecta.
// Sin esto, el tiempo de respuesta revela qué correos están registrados.
const dummyHash = "$argon2id$v=19$m=65536,t=3,p=2$c29tZXNhbHR2YWx1ZTEy$c2FsdGVkaGFzaHBsYWNlaG9sZGVydmFsdWU"

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || req.Password == "" {
		writeErr(w, http.StatusBadRequest, "Ingresa tu correo y tu contraseña")
		return
	}

	user, err := s.repo.UserByEmail(r.Context(), req.Email)
	if err != nil {
		if !errors.Is(err, repo.ErrNotFound) {
			writeErr(w, http.StatusInternalServerError, "Error al validar las credenciales")
			return
		}
		_, _ = crypto.VerifyPassword(req.Password, dummyHash)
		s.audit(r, "auth.login", "user", "", req.Email, false, map[string]any{"motivo": "usuario inexistente"})
		writeErr(w, http.StatusUnauthorized, "Correo o contraseña incorrectos")
		return
	}

	ok, err := crypto.VerifyPassword(req.Password, user.PasswordHash)
	if err != nil || !ok {
		s.audit(r, "auth.login", "user", user.ID.String(), user.Email, false,
			map[string]any{"motivo": "contraseña incorrecta"})
		writeErr(w, http.StatusUnauthorized, "Correo o contraseña incorrectos")
		return
	}
	if user.Status != "active" {
		s.audit(r, "auth.login", "user", user.ID.String(), user.Email, false,
			map[string]any{"motivo": "cuenta suspendida"})
		writeErr(w, http.StatusForbidden, "Tu cuenta está suspendida. Contacta al administrador.")
		return
	}

	token, err := crypto.NewToken(32)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo crear la sesión")
		return
	}
	expires := time.Now().Add(s.cfg.SessionTTL)
	if _, err := s.repo.CreateSession(r.Context(), user.ID, crypto.HashToken(token),
		clientIP(r, s.cfg.TrustProxyHeaders), userAgent(r), expires); err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo crear la sesión")
		return
	}
	_ = s.repo.TouchLogin(r.Context(), user.ID)

	s.setSessionCookie(w, token, expires)
	s.audit(r, "auth.login", "user", user.ID.String(), user.Email, true, nil)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":       user,
		"expires_at": expires,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(s.cfg.CookieName); err == nil && c.Value != "" {
		if err := s.repo.RevokeSession(r.Context(), crypto.HashToken(c.Value)); err != nil {
			writeErr(w, http.StatusInternalServerError, "No se pudo cerrar la sesión")
			return
		}
	}
	s.clearSessionCookie(w)
	s.audit(r, "auth.logout", "user", "", "", true, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMe devuelve el usuario y el contexto que el frontend necesita para
// decidir qué pintar: si Drive está conectado y cuánto espacio le queda.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	quota := user.QuotaBytes
	if quota == 0 {
		quota = s.cfg.DefaultQuotaBytes
	}
	bandwidth := user.BandwidthBytes
	if bandwidth == 0 {
		bandwidth = s.cfg.DefaultBandwidthBytes
	}

	monthStart := time.Now().UTC().Truncate(24 * time.Hour).AddDate(0, 0, -time.Now().UTC().Day()+1)
	used, err := s.repo.BandwidthUsedSince(r.Context(), user.ID, monthStart)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo calcular el consumo")
		return
	}

	driveConnected := false
	if acc, err := s.repo.PrimaryAccount(r.Context()); err == nil && acc.Status == "active" {
		driveConnected = true
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user":                 user,
		"quota_bytes":          quota,
		"bandwidth_bytes":      bandwidth,
		"bandwidth_used_month": used,
		"drive_connected":      driveConnected,
		"max_chunk_bytes":      s.cfg.MaxChunkBytes,
	})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	var req changePasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := validatePassword(req.NewPassword); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	ok, err := crypto.VerifyPassword(req.CurrentPassword, user.PasswordHash)
	if err != nil || !ok {
		s.audit(r, "auth.password_change", "user", user.ID.String(), user.Email, false, nil)
		writeErr(w, http.StatusUnauthorized, "La contraseña actual no es correcta")
		return
	}

	hash, err := crypto.HashPassword(req.NewPassword)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo actualizar la contraseña")
		return
	}
	if err := s.repo.SetPassword(r.Context(), user.ID, hash, false); err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo actualizar la contraseña")
		return
	}

	// Cambiar la contraseña invalida el resto de sesiones; la actual se
	// renueva para no expulsar a quien acaba de hacerlo.
	if err := s.repo.RevokeUserSessions(r.Context(), user.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudieron cerrar las otras sesiones")
		return
	}
	token, err := crypto.NewToken(32)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo renovar la sesión")
		return
	}
	expires := time.Now().Add(s.cfg.SessionTTL)
	if _, err := s.repo.CreateSession(r.Context(), user.ID, crypto.HashToken(token),
		clientIP(r, s.cfg.TrustProxyHeaders), userAgent(r), expires); err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo renovar la sesión")
		return
	}
	s.setSessionCookie(w, token, expires)

	s.audit(r, "auth.password_change", "user", user.ID.String(), user.Email, true, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// validatePassword aplica una política simple pero efectiva: longitud antes
// que complejidad. Una frase larga vence a "P@ssw0rd".
func validatePassword(pw string) error {
	if utf8.RuneCountInString(pw) < 10 {
		return errors.New("La contraseña debe tener al menos 10 caracteres")
	}
	if utf8.RuneCountInString(pw) > 200 {
		return errors.New("La contraseña es demasiado larga")
	}
	if strings.TrimSpace(pw) == "" {
		return errors.New("La contraseña no puede ser solo espacios")
	}
	return nil
}
