package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/docuhub/docuhub/internal/drive"
	"github.com/docuhub/docuhub/internal/models"
	"github.com/docuhub/docuhub/internal/quota"
	"github.com/docuhub/docuhub/internal/repo"
	"github.com/google/uuid"
)

// Google exige que todos los trozos menos el último sean múltiplos de 256 KiB.
const chunkMultiple = 256 << 10

type initUploadRequest struct {
	FolderID uuid.UUID `json:"folder_id"`
	Name     string    `json:"name"`
	SizeBytes int64    `json:"size_bytes"`
	MimeType string    `json:"mime_type"`
}

// handleInitUpload abre una sesión resumible en Drive y devuelve el plan de
// subida al navegador. A partir de aquí el cliente manda trozos.
func (s *Server) handleInitUpload(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	var req initUploadRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	name, err := sanitizeName(req.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.SizeBytes < 0 {
		writeErr(w, http.StatusBadRequest, "El tamaño del archivo no es válido")
		return
	}

	// Permiso de escritura sobre la carpeta de destino.
	folder, _, level, err := s.folderAccess(r.Context(), user, req.FolderID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "La carpeta de destino no existe")
		return
	}
	if !atLeast(level, models.LevelEditor) {
		writeErr(w, http.StatusForbidden, "No puedes subir archivos a esta carpeta")
		return
	}

	// Cuota: se comprueba antes de mover un solo byte.
	if err := s.quota.CheckStorage(r.Context(), user, req.SizeBytes); err != nil {
		var limitErr *quota.LimitError
		if errors.As(err, &limitErr) {
			s.audit(r, "file.upload_init", "folder", folder.ID.String(), name, false,
				map[string]any{"motivo": "cuota de almacenamiento superada"})
			writeErrCode(w, http.StatusForbidden, "quota_exceeded", limitErr.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "No se pudo verificar tu cuota")
		return
	}

	acc, err := s.primaryAccount(r.Context())
	if err != nil {
		writeErr(w, http.StatusFailedDependency, err.Error())
		return
	}
	if acc.QuotaTotalBytes > 0 && acc.QuotaUsedBytes+req.SizeBytes > acc.QuotaTotalBytes {
		writeErrCode(w, http.StatusInsufficientStorage, "drive_full",
			fmt.Sprintf("La cuenta de Google Drive está llena (%s de %s usados). Libera espacio o enlaza otra cuenta.",
				quota.Human(acc.QuotaUsedBytes), quota.Human(acc.QuotaTotalBytes)))
		return
	}

	mimeType := req.MimeType
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(name))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}

	parentDriveID := folder.DriveFolderID
	if parentDriveID == "" {
		parentDriveID = acc.RootFolderID
	}

	// Si ya existe un archivo con ese nombre, la subida se convierte en una
	// versión nueva del mismo registro en lugar de duplicarlo.
	var fileID *uuid.UUID
	existing, err := s.repo.FileByNameInFolder(r.Context(), folder.ID, name)
	switch {
	case err == nil:
		id := existing.ID
		fileID = &id
	case errors.Is(err, repo.ErrNotFound):
		created, cerr := s.repo.CreateFile(r.Context(), folder.ID, name, mimeType, req.SizeBytes, &user.ID, &acc.ID)
		if cerr != nil {
			writeErr(w, http.StatusInternalServerError, "No se pudo registrar el archivo")
			return
		}
		id := created.ID
		fileID = &id
	default:
		writeErr(w, http.StatusInternalServerError, "No se pudo consultar la carpeta")
		return
	}

	sessionURL, err := s.drive.StartResumable(r.Context(), acc, name, mimeType, parentDriveID, req.SizeBytes)
	if err != nil {
		if fileID != nil {
			_ = s.repo.MarkFileFailed(r.Context(), *fileID)
		}
		writeErr(w, http.StatusBadGateway, "Google Drive no aceptó la subida: "+err.Error())
		return
	}

	sessionEnc, err := s.enc.Encrypt(sessionURL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo asegurar la sesión de subida")
		return
	}

	up, err := s.repo.CreateUpload(r.Context(), user.ID, folder.ID, fileID, &acc.ID,
		name, mimeType, req.SizeBytes, sessionEnc, time.Now().Add(6*24*time.Hour))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo registrar la subida")
		return
	}

	s.audit(r, "file.upload_init", "folder", folder.ID.String(), name, true,
		map[string]any{"bytes": req.SizeBytes, "version_de": fileID})
	writeJSON(w, http.StatusCreated, map[string]any{
		"upload_id":      up.ID,
		"chunk_size":     s.cfg.MaxChunkBytes,
		"bytes_received": 0,
		"expires_at":     up.ExpiresAt,
	})
}

// handleUploadChunk recibe un trozo y lo reenvía a Drive sin bufferizarlo
// completo: el cuerpo de la petición se canaliza directo.
func (s *Server) handleUploadChunk(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	up, ok := s.loadUpload(w, r, user)
	if !ok {
		return
	}

	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 {
		writeErr(w, http.StatusBadRequest, "Falta el parámetro offset o no es válido")
		return
	}
	if offset != up.BytesReceived {
		// El cliente se desincronizó: se le dice desde dónde continuar.
		writeJSON(w, http.StatusConflict, map[string]any{
			"message":        "El trozo no continúa donde corresponde",
			"code":           "offset_mismatch",
			"bytes_received": up.BytesReceived,
		})
		return
	}

	chunkLen := r.ContentLength
	if chunkLen <= 0 {
		writeErr(w, http.StatusLengthRequired, "El trozo debe indicar su tamaño (Content-Length)")
		return
	}
	if chunkLen > s.cfg.MaxChunkBytes {
		writeErr(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("El trozo excede el máximo permitido (%s)", quota.Human(s.cfg.MaxChunkBytes)))
		return
	}
	isLast := up.SizeBytes > 0 && offset+chunkLen >= up.SizeBytes
	if !isLast && chunkLen%chunkMultiple != 0 {
		writeErr(w, http.StatusBadRequest,
			"Cada trozo intermedio debe medir un múltiplo de 256 KB")
		return
	}

	sessionURL, err := s.enc.Decrypt(up.SessionURLEnc)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo recuperar la sesión de subida")
		return
	}

	body := http.MaxBytesReader(w, r.Body, chunkLen)
	defer body.Close()

	res, err := s.drive.UploadChunk(r.Context(), sessionURL, offset, chunkLen, up.SizeBytes, body)
	if err != nil {
		if errors.Is(err, drive.ErrUploadExpired) {
			_ = s.repo.FinishUpload(r.Context(), up.ID, "failed", "la sesión expiró")
			writeErrCode(w, http.StatusGone, "upload_expired",
				"La sesión de subida expiró. Vuelve a empezar el envío de este archivo.")
			return
		}
		_ = s.repo.FinishUpload(r.Context(), up.ID, "failed", err.Error())
		s.audit(r, "file.upload_chunk", "upload", up.ID.String(), up.FileName, false,
			map[string]any{"error": err.Error()})
		writeErr(w, http.StatusBadGateway, "Google Drive rechazó el trozo: "+err.Error())
		return
	}

	if err := s.repo.SetUploadProgress(r.Context(), up.ID, res.BytesReceived); err != nil {
		slog.Error("no se pudo guardar el avance de la subida", "error", err)
	}

	if !res.Complete {
		writeJSON(w, http.StatusOK, map[string]any{
			"complete":       false,
			"bytes_received": res.BytesReceived,
		})
		return
	}

	file, err := s.finishUpload(r, up, res)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "La subida terminó pero no se pudo registrar: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"complete":       true,
		"bytes_received": res.BytesReceived,
		"file":           file,
	})
}

// finishUpload cierra el registro: marca el archivo listo, archiva la versión
// anterior si la había y actualiza el consumo del usuario.
func (s *Server) finishUpload(r *http.Request, up *models.Upload, res *drive.ChunkResult) (*models.File, error) {
	ctx := r.Context()
	user := userFrom(ctx)

	if up.FileID == nil || res.File == nil {
		return nil, errors.New("faltan datos de la subida")
	}

	current, err := s.repo.FileByID(ctx, *up.FileID)
	if err != nil {
		return nil, err
	}

	// ¿Había un archivo anterior con contenido? Entonces esto es una versión.
	if current.DriveFileID != "" && current.DriveFileID != res.File.ID {
		if err := s.repo.AddVersion(ctx, current.ID, current.Version, current.DriveFileID,
			current.SizeBytes, current.MD5, current.OwnerID); err != nil {
			slog.Error("no se pudo archivar la versión anterior", "error", err)
		}
		if _, err := s.repo.BumpVersion(ctx, current.ID); err != nil {
			slog.Error("no se pudo incrementar la versión", "error", err)
		}
		// El espacio de la versión vieja sigue ocupado en Drive, así que se
		// descuenta y se vuelve a sumar con el tamaño nuevo más abajo.
		if current.OwnerID != nil {
			_ = s.repo.AddUsedBytes(ctx, *current.OwnerID, -current.SizeBytes)
		}
	}

	size := res.File.SizeBytes()
	if size == 0 {
		size = up.SizeBytes
	}
	if err := s.repo.MarkFileReady(ctx, current.ID, res.File.ID, size, res.File.MD5Checksum); err != nil {
		return nil, err
	}
	if err := s.repo.FinishUpload(ctx, up.ID, "completed", ""); err != nil {
		slog.Error("no se pudo cerrar la subida", "error", err)
	}
	if err := s.repo.AddUsedBytes(ctx, user.ID, size); err != nil {
		slog.Error("no se pudo actualizar el espacio usado", "error", err)
	}

	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		uid := user.ID
		fid := current.ID
		if err := s.repo.InsertBandwidth(bgCtx, &uid, &fid, nil, "upload", size,
			clientIP(r, s.cfg.TrustProxyHeaders)); err != nil {
			slog.Error("no se pudo registrar el tráfico de subida", "error", err)
		}
	}()

	s.audit(r, "file.upload", "file", current.ID.String(), current.Name, true,
		map[string]any{"bytes": size, "version": current.Version})

	return s.repo.FileByID(ctx, current.ID)
}

// handleUploadStatus pregunta a Drive por dónde va la subida. El cliente lo
// llama al reconectar tras un corte para saber desde qué byte seguir.
func (s *Server) handleUploadStatus(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	up, ok := s.loadUpload(w, r, user)
	if !ok {
		return
	}

	sessionURL, err := s.enc.Decrypt(up.SessionURLEnc)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo recuperar la sesión de subida")
		return
	}
	res, err := s.drive.ResumeOffset(r.Context(), sessionURL, up.SizeBytes)
	if err != nil {
		if errors.Is(err, drive.ErrUploadExpired) {
			_ = s.repo.FinishUpload(r.Context(), up.ID, "failed", "la sesión expiró")
			writeErrCode(w, http.StatusGone, "upload_expired", "La sesión de subida expiró")
			return
		}
		writeErr(w, http.StatusBadGateway, "No se pudo consultar el avance: "+err.Error())
		return
	}

	if res.BytesReceived != up.BytesReceived {
		_ = s.repo.SetUploadProgress(r.Context(), up.ID, res.BytesReceived)
	}
	if res.Complete {
		if file, err := s.finishUpload(r, up, res); err == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"complete": true, "bytes_received": res.BytesReceived, "file": file,
			})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"complete":       false,
		"bytes_received": res.BytesReceived,
		"size_bytes":     up.SizeBytes,
		"chunk_size":     s.cfg.MaxChunkBytes,
		"file_name":      up.FileName,
	})
}

func (s *Server) handleListUploads(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	ups, err := s.repo.ListActiveUploads(r.Context(), user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudieron listar las subidas en curso")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"uploads": ups})
}

func (s *Server) handleAbortUpload(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	up, ok := s.loadUpload(w, r, user)
	if !ok {
		return
	}

	if sessionURL, err := s.enc.Decrypt(up.SessionURLEnc); err == nil {
		if err := s.drive.AbortUpload(r.Context(), sessionURL); err != nil {
			slog.Warn("no se pudo cancelar la sesión en Drive", "error", err)
		}
	}
	if err := s.repo.FinishUpload(r.Context(), up.ID, "aborted", "cancelada por el usuario"); err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo cancelar la subida")
		return
	}
	// Si el archivo nunca tuvo contenido, se descarta el registro vacío.
	if up.FileID != nil {
		if f, err := s.repo.FileByID(r.Context(), *up.FileID); err == nil && f.Status == "uploading" {
			_ = s.repo.SoftDeleteFile(r.Context(), f.ID)
		}
	}

	s.audit(r, "file.upload_abort", "upload", up.ID.String(), up.FileName, true, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// loadUpload valida que la subida exista, sea del usuario y siga activa.
func (s *Server) loadUpload(w http.ResponseWriter, r *http.Request, user *models.User) (*models.Upload, bool) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return nil, false
	}
	up, err := s.repo.UploadByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "La subida no existe")
		return nil, false
	}
	if up.UserID != user.ID && !user.IsAtLeast(models.RoleAdmin) {
		writeErr(w, http.StatusNotFound, "La subida no existe")
		return nil, false
	}
	if up.Status != "active" {
		writeErrCode(w, http.StatusGone, "upload_closed",
			"Esta subida ya está cerrada ("+up.Status+")")
		return nil, false
	}
	return up, true
}
