package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// apiError es el único formato de error que sale de la API. El frontend
// muestra `message` tal cual al usuario, así que va siempre en español.
type apiError struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("no se pudo escribir la respuesta", "error", err)
	}
}

func writeErr(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, apiError{Message: message})
}

func writeErrCode(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiError{Message: message, Code: code})
}

// decodeJSON limita el cuerpo y rechaza campos desconocidos: un error de
// tipeo en el frontend se ve de inmediato en vez de ignorarse en silencio.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var syntaxErr *json.SyntaxError
		if errors.As(err, &syntaxErr) {
			writeErr(w, http.StatusBadRequest, "El cuerpo de la petición no es JSON válido")
		} else {
			writeErr(w, http.StatusBadRequest, "Datos inválidos: "+err.Error())
		}
		return false
	}
	return true
}

// clientIP resuelve la IP real detrás de Caddy y del túnel de Cloudflare.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if v := r.Header.Get("CF-Connecting-IP"); v != "" {
			return v
		}
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			if i := strings.IndexByte(v, ','); i > 0 {
				return strings.TrimSpace(v[:i])
			}
			return strings.TrimSpace(v)
		}
		if v := r.Header.Get("X-Real-IP"); v != "" {
			return v
		}
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}

func userAgent(r *http.Request) string {
	ua := r.UserAgent()
	if len(ua) > 400 {
		return ua[:400]
	}
	return ua
}
