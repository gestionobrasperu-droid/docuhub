package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/docuhub/docuhub/internal/crypto"
	"github.com/docuhub/docuhub/internal/models"
	"github.com/docuhub/docuhub/internal/repo"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type createShareRequest struct {
	FileID        *uuid.UUID `json:"file_id"`
	FolderID      *uuid.UUID `json:"folder_id"`
	Password      string     `json:"password"`
	MaxDownloads  int        `json:"max_downloads"`
	ExpiresInDays int        `json:"expires_in_days"`
	Note          string     `json:"note"`
}

// handleCreateShare emite un enlace público. El token se muestra UNA vez: en
// la base solo queda su hash, igual que con una contraseña.
func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	var req createShareRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if (req.FileID == nil) == (req.FolderID == nil) {
		writeErr(w, http.StatusBadRequest, "Indica un archivo o una carpeta, no ambos")
		return
	}

	// Compartir hacia fuera exige poder descargar el recurso.
	var resourceName string
	if req.FileID != nil {
		file, _, level, err := s.fileAccess(r.Context(), user, *req.FileID)
		if err != nil || !atLeast(level, models.LevelDownloader) {
			writeErr(w, http.StatusForbidden, "No puedes compartir este archivo")
			return
		}
		resourceName = file.Name
	} else {
		folder, _, level, err := s.folderAccess(r.Context(), user, *req.FolderID)
		if err != nil || !atLeast(level, models.LevelDownloader) {
			writeErr(w, http.StatusForbidden, "No puedes compartir esta carpeta")
			return
		}
		resourceName = folder.Path
	}

	passwordHash := ""
	if req.Password != "" {
		if len(req.Password) < 6 {
			writeErr(w, http.StatusBadRequest, "La contraseña del enlace debe tener al menos 6 caracteres")
			return
		}
		h, err := crypto.HashPassword(req.Password)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "No se pudo proteger el enlace")
			return
		}
		passwordHash = h
	}

	var expiresAt *time.Time
	if req.ExpiresInDays > 0 {
		t := time.Now().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &t
	}
	if req.MaxDownloads < 0 {
		req.MaxDownloads = 0
	}

	token, err := crypto.NewToken(24)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo generar el enlace")
		return
	}

	share, err := s.repo.CreateShare(r.Context(), crypto.HashToken(token), req.FileID, req.FolderID,
		user.ID, passwordHash, req.MaxDownloads, false, strings.TrimSpace(req.Note), expiresAt)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo crear el enlace")
		return
	}

	share.Token = token
	share.URL = s.cfg.BaseURL + "/s/" + token

	s.audit(r, "share.create", "share", share.ID.String(), resourceName, true, map[string]any{
		"expira":         expiresAt,
		"max_descargas":  req.MaxDownloads,
		"con_contrasena": passwordHash != "",
	})
	writeJSON(w, http.StatusCreated, share)
}

func (s *Server) handleListShares(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	// Un administrador ve todos los enlaces; el resto, solo los suyos.
	var filter *uuid.UUID
	if !user.IsAtLeast(models.RoleManager) {
		id := user.ID
		filter = &id
	}
	shares, err := s.repo.ListShares(r.Context(), filter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudieron listar los enlaces")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": shares})
}

func (s *Server) handleRevokeShare(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	shares, err := s.repo.ListShares(r.Context(), nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo revocar el enlace")
		return
	}
	for _, sh := range shares {
		if sh.ID != id {
			continue
		}
		if !user.IsAtLeast(models.RoleManager) && (sh.CreatedBy == nil || *sh.CreatedBy != user.ID) {
			writeErr(w, http.StatusForbidden, "Solo puedes revocar tus propios enlaces")
			return
		}
		if err := s.repo.RevokeShare(r.Context(), id); err != nil {
			writeErr(w, http.StatusInternalServerError, "No se pudo revocar el enlace")
			return
		}
		s.audit(r, "share.revoke", "share", id.String(), sh.Note, true, nil)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	writeErr(w, http.StatusNotFound, "El enlace no existe")
}

// ------------------------------------------------------- acceso público ---

// publicToken resuelve el token de la URL contra un enlace vigente.
func (s *Server) publicToken(w http.ResponseWriter, r *http.Request) (*models.ShareLink, string, bool) {
	token := chi.URLParam(r, "token")
	if token == "" {
		writeErr(w, http.StatusNotFound, "Enlace no válido")
		return nil, "", false
	}
	share, pwHash, err := s.repo.ShareByToken(r.Context(), crypto.HashToken(token))
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "Este enlace no existe, expiró o fue revocado")
		} else {
			writeErr(w, http.StatusInternalServerError, "No se pudo abrir el enlace")
		}
		return nil, "", false
	}
	return share, pwHash, true
}

// handlePublicInfo describe qué hay detrás del enlace sin entregar el archivo.
func (s *Server) handlePublicInfo(w http.ResponseWriter, r *http.Request) {
	share, pwHash, ok := s.publicToken(w, r)
	if !ok {
		return
	}

	out := map[string]any{
		"has_password":   pwHash != "",
		"note":           share.Note,
		"expires_at":     share.ExpiresAt,
		"max_downloads":  share.MaxDownloads,
		"download_count": share.DownloadCount,
	}

	if share.FileID != nil {
		file, err := s.repo.FileByID(r.Context(), *share.FileID)
		if err != nil {
			writeErr(w, http.StatusNotFound, "El archivo ya no está disponible")
			return
		}
		out["type"] = "file"
		out["name"] = file.Name
		out["size_bytes"] = file.SizeBytes
		out["mime_type"] = file.MimeType
	} else if share.FolderID != nil {
		folder, err := s.repo.FolderByID(r.Context(), *share.FolderID)
		if err != nil {
			writeErr(w, http.StatusNotFound, "La carpeta ya no está disponible")
			return
		}
		files, err := s.repo.ListFiles(r.Context(), folder.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "No se pudo listar la carpeta")
			return
		}
		out["type"] = "folder"
		out["name"] = folder.Name

		// Con contraseña activa no se revela el contenido hasta desbloquear.
		if pwHash == "" {
			list := make([]map[string]any, 0, len(files))
			for _, f := range files {
				list = append(list, map[string]any{
					"id": f.ID, "name": f.Name, "size_bytes": f.SizeBytes, "mime_type": f.MimeType,
				})
			}
			out["files"] = list
		}
	}

	s.audit(r, "share.view", "share", share.ID.String(), share.Note, true, nil)
	writeJSON(w, http.StatusOK, out)
}

type unlockRequest struct {
	Password string `json:"password"`
}

// handlePublicUnlock valida la contraseña y devuelve una clave de un solo uso
// que el navegador adjunta a la descarga. Evita mandar la contraseña en la URL.
func (s *Server) handlePublicUnlock(w http.ResponseWriter, r *http.Request) {
	share, pwHash, ok := s.publicToken(w, r)
	if !ok {
		return
	}
	var req unlockRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if pwHash == "" {
		writeJSON(w, http.StatusOK, map[string]any{"unlocked": true})
		return
	}

	valid, err := crypto.VerifyPassword(req.Password, pwHash)
	if err != nil || !valid {
		s.audit(r, "share.unlock", "share", share.ID.String(), share.Note, false, nil)
		writeErr(w, http.StatusUnauthorized, "Contraseña incorrecta")
		return
	}

	grant, err := crypto.NewToken(24)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo desbloquear el enlace")
		return
	}
	s.grantMu.Lock()
	s.shareGrants[grant] = shareGrant{shareID: share.ID, expires: time.Now().Add(30 * time.Minute)}
	s.grantMu.Unlock()

	s.audit(r, "share.unlock", "share", share.ID.String(), share.Note, true, nil)
	writeJSON(w, http.StatusOK, map[string]any{"unlocked": true, "grant": grant})
}

// handlePublicDownload entrega el archivo de un enlace público.
func (s *Server) handlePublicDownload(w http.ResponseWriter, r *http.Request) {
	share, pwHash, ok := s.publicToken(w, r)
	if !ok {
		return
	}

	if pwHash != "" && !s.checkGrant(r.URL.Query().Get("grant"), share.ID) {
		writeErrCode(w, http.StatusUnauthorized, "password_required",
			"Este enlace está protegido con contraseña")
		return
	}

	// El enlace puede apuntar a un archivo concreto o a uno dentro de la
	// carpeta compartida.
	var fileID uuid.UUID
	if share.FileID != nil {
		fileID = *share.FileID
	} else {
		id, err := uuid.Parse(r.URL.Query().Get("file_id"))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "Indica qué archivo quieres descargar")
			return
		}
		file, err := s.repo.FileByID(r.Context(), id)
		if err != nil || share.FolderID == nil || file.FolderID != *share.FolderID {
			// Solo se sirve lo que está directamente en la carpeta compartida.
			writeErr(w, http.StatusNotFound, "Ese archivo no pertenece a este enlace")
			return
		}
		fileID = id
	}

	file, err := s.repo.FileByID(r.Context(), fileID)
	if err != nil || file.Status != "ready" || file.DriveFileID == "" {
		writeErr(w, http.StatusNotFound, "El archivo ya no está disponible")
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
		writeErr(w, http.StatusBadGateway, "No se pudo obtener el archivo: "+err.Error())
		return
	}
	defer resp.Body.Close()

	s.streamToClient(w, r, resp, file.Name, file.MimeType, false)

	written := transferred(w)
	if rangeHeader == "" {
		if err := s.repo.RegisterShareUse(r.Context(), share.ID); err != nil {
			// No se interrumpe la descarga por esto, pero queda en el log.
			s.audit(r, "share.download", "share", share.ID.String(), file.Name, false,
				map[string]any{"error": err.Error()})
		}
	}
	shareID := share.ID
	s.recordTransfer(uuid.Nil, file.ID, &shareID, written, clientIP(r, s.cfg.TrustProxyHeaders), rangeHeader == "")

	s.audit(r, "share.download", "share", share.ID.String(), file.Name, true,
		map[string]any{"bytes": written})
}

// checkGrant valida la clave temporal emitida tras desbloquear con contraseña.
func (s *Server) checkGrant(grant string, shareID uuid.UUID) bool {
	if grant == "" {
		return false
	}
	s.grantMu.Lock()
	defer s.grantMu.Unlock()

	now := time.Now()
	for k, v := range s.shareGrants {
		if now.After(v.expires) {
			delete(s.shareGrants, k)
		}
	}
	g, ok := s.shareGrants[grant]
	return ok && g.shareID == shareID && now.Before(g.expires)
}
