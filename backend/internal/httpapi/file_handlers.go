package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/docuhub/docuhub/internal/models"
	"github.com/docuhub/docuhub/internal/quota"
	"github.com/google/uuid"
)

func (s *Server) handleFileInfo(w http.ResponseWriter, r *http.Request) {
	file, ok := s.requireFile(w, r, "id", models.LevelViewer)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, file)
}

type updateFileRequest struct {
	Name        *string  `json:"name"`
	Description *string  `json:"description"`
	Tags        []string `json:"tags"`
}

func (s *Server) handleUpdateFile(w http.ResponseWriter, r *http.Request) {
	file, ok := s.requireFile(w, r, "id", models.LevelEditor)
	if !ok {
		return
	}
	var req updateFileRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Name != nil {
		name, err := sanitizeName(*req.Name)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		req.Name = &name
		if file.DriveFileID != "" {
			acc, err := s.accountFor(r.Context(), file.DriveAccountID)
			if err == nil {
				if err := s.drive.Rename(r.Context(), acc, file.DriveFileID, name); err != nil {
					writeErr(w, http.StatusBadGateway, "No se pudo renombrar en Google Drive: "+err.Error())
					return
				}
			}
		}
	}
	if req.Tags != nil {
		for i, t := range req.Tags {
			req.Tags[i] = strings.TrimSpace(t)
		}
	}

	updated, err := s.repo.UpdateFileMeta(r.Context(), file.ID, req.Name, req.Description, req.Tags)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo actualizar el archivo")
		return
	}
	s.audit(r, "file.update", "file", file.ID.String(), updated.Name, true, nil)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	file, ok := s.requireFile(w, r, "id", models.LevelEditor)
	if !ok {
		return
	}
	user := userFrom(r.Context())

	// Un editor borra lo suyo; borrar lo ajeno exige nivel de responsable.
	if file.OwnerID != nil && *file.OwnerID != user.ID {
		if _, _, level, err := s.fileAccess(r.Context(), user, file.ID); err != nil || !atLeast(level, models.LevelManager) {
			writeErr(w, http.StatusForbidden, "Solo puedes eliminar archivos que subiste tú")
			return
		}
	}

	if err := s.repo.SoftDeleteFile(r.Context(), file.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo eliminar el archivo")
		return
	}
	if file.OwnerID != nil {
		_ = s.repo.AddUsedBytes(r.Context(), *file.OwnerID, -file.SizeBytes)
	}
	if file.DriveFileID != "" {
		if acc, err := s.accountFor(r.Context(), file.DriveAccountID); err == nil {
			if err := s.drive.Trash(r.Context(), acc, file.DriveFileID); err != nil {
				slog.Warn("no se pudo enviar a la papelera de Drive", "archivo", file.Name, "error", err)
			}
		}
	}

	s.audit(r, "file.delete", "file", file.ID.String(), file.Name, true,
		map[string]any{"bytes": file.SizeBytes})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDownload canaliza el archivo desde Drive hacia el navegador.
//
// El archivo nunca toca el disco de la laptop: io.Copy mueve los bytes con un
// buffer de 32 KB, así que da igual que pesen 50 MB o 50 GB.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	file, ok := s.requireFile(w, r, "id", models.LevelDownloader)
	if !ok {
		return
	}
	user := userFrom(r.Context())

	if file.Status != "ready" || file.DriveFileID == "" {
		writeErr(w, http.StatusConflict, "El archivo aún no terminó de subirse")
		return
	}

	if err := s.quota.CheckBandwidth(r.Context(), user, file.SizeBytes); err != nil {
		var limitErr *quota.LimitError
		if errors.As(err, &limitErr) {
			s.audit(r, "file.download", "file", file.ID.String(), file.Name, false,
				map[string]any{"motivo": "cuota de descarga superada"})
			writeErrCode(w, http.StatusForbidden, "quota_exceeded", limitErr.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "No se pudo verificar tu cuota")
		return
	}

	acc, err := s.accountFor(r.Context(), file.DriveAccountID)
	if err != nil {
		writeErr(w, http.StatusFailedDependency, err.Error())
		return
	}

	rangeHeader := r.Header.Get("Range")
	resp, err := s.drive.Download(r.Context(), acc, file.DriveFileID, rangeHeader)
	if err != nil {
		s.audit(r, "file.download", "file", file.ID.String(), file.Name, false,
			map[string]any{"error": err.Error()})
		writeErr(w, http.StatusBadGateway, "Google Drive no entregó el archivo: "+err.Error())
		return
	}
	defer resp.Body.Close()

	s.streamToClient(w, r, resp, file.Name, file.MimeType, r.URL.Query().Get("inline") == "1")

	// La contabilidad se hace con lo realmente transferido, no con el tamaño
	// nominal: si el usuario cancela a la mitad, se le cobra la mitad.
	written := transferred(w)
	s.recordTransfer(user.ID, file.ID, nil, written, clientIP(r, s.cfg.TrustProxyHeaders), rangeHeader == "")

	s.audit(r, "file.download", "file", file.ID.String(), file.Name, true,
		map[string]any{"bytes": written, "parcial": rangeHeader != ""})
}

// streamToClient copia la respuesta de Drive al cliente conservando los
// encabezados que hacen posible reanudar y hacer seek.
func (s *Server) streamToClient(w http.ResponseWriter, r *http.Request, resp *http.Response, name, mimeType string, inline bool) {
	h := w.Header()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		h.Set("Content-Type", ct)
	} else if mimeType != "" {
		h.Set("Content-Type", mimeType)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		h.Set("Content-Length", cl)
	}
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		h.Set("Content-Range", cr)
	}
	h.Set("Accept-Ranges", "bytes")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Content-Disposition", contentDisposition(name, inline))

	status := http.StatusOK
	if resp.StatusCode == http.StatusPartialContent {
		status = http.StatusPartialContent
	}
	w.WriteHeader(status)

	if _, err := io.Copy(w, resp.Body); err != nil {
		// Lo normal aquí es que el usuario cancelara la descarga.
		slog.Debug("transferencia interrumpida", "archivo", name, "error", err)
	}
}

// transferred lee el contador del envoltorio de logging.
func transferred(w http.ResponseWriter) int64 {
	if sw, ok := w.(*statusWriter); ok {
		return sw.bytes
	}
	return 0
}

// recordTransfer guarda el consumo fuera del ciclo de la petición.
func (s *Server) recordTransfer(userID, fileID uuid.UUID, shareID *uuid.UUID, bytes int64, ip string, full bool) {
	if bytes <= 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		uid, fid := userID, fileID
		var uidPtr *uuid.UUID
		if uid != uuid.Nil {
			uidPtr = &uid
		}
		if err := s.repo.InsertBandwidth(ctx, uidPtr, &fid, shareID, "download", bytes, ip); err != nil {
			slog.Error("no se pudo registrar el consumo", "error", err)
		}
		if full {
			if err := s.repo.RegisterDownload(ctx, fid); err != nil {
				slog.Error("no se pudo contar la descarga", "error", err)
			}
		}
	}()
}

// contentDisposition arma el encabezado con el nombre original, incluidas
// tildes y eñes, usando la codificación de RFC 5987.
func contentDisposition(name string, inline bool) string {
	kind := "attachment"
	if inline {
		kind = "inline"
	}
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`,
		kind, ascii, url.PathEscape(name))
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		writeErr(w, http.StatusBadRequest, "Escribe al menos 2 caracteres para buscar")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	found, err := s.repo.SearchFiles(r.Context(), q, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "La búsqueda falló")
		return
	}

	// Se filtra por permisos después de buscar: la base no sabe de permisos
	// heredados, y así la consulta se mantiene simple y rápida.
	visible := make([]*models.File, 0, len(found))
	for _, f := range found {
		_, _, level, err := s.fileAccess(r.Context(), user, f.ID)
		if err != nil {
			continue
		}
		if atLeast(level, models.LevelViewer) {
			visible = append(visible, f)
		}
	}

	s.audit(r, "file.search", "", "", q, true, map[string]any{"resultados": len(visible)})
	writeJSON(w, http.StatusOK, map[string]any{"query": q, "files": visible})
}
