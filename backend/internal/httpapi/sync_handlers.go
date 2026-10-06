package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/docuhub/docuhub/internal/drive"
	"github.com/docuhub/docuhub/internal/models"
	"github.com/docuhub/docuhub/internal/repo"
	"github.com/google/uuid"
)

// Sincronización Drive → DocuHub.
//
// La plataforma es la dueña del control de accesos, pero la carpeta de Drive
// sigue siendo una carpeta de Drive: su dueño puede entrar a drive.google.com
// y arrastrar archivos ahí. Cuando eso pasa, la plataforma no se entera sola
// (el API de Drive no avisa sin configurar notificaciones push con un dominio
// verificado, que es más complejidad de la que este caso necesita).
//
// Este escaneo recorre la carpeta raíz y registra lo que encuentre de nuevo:
// carpetas y archivos pasan a existir en DocuHub con todo su control de
// permisos, cuotas y auditoría.

const (
	syncMaxDepth = 12
	syncMaxItems = 5000
)

type syncResult struct {
	FoldersCreated int      `json:"folders_created"`
	FilesImported  int      `json:"files_imported"`
	FilesSkipped   int      `json:"files_skipped"`
	BytesImported  int64    `json:"bytes_imported"`
	Truncated      bool     `json:"truncated"`
	Warnings       []string `json:"warnings"`
}

func (s *Server) handleDriveSync(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	user := userFrom(r.Context())

	acc, err := s.repo.AccountByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "La cuenta no existe")
		return
	}
	if acc.RootFolderID == "" {
		writeErr(w, http.StatusConflict, "La cuenta no tiene carpeta raíz; vuelve a conectarla")
		return
	}

	root, err := s.repo.RootFolder(r.Context())
	if err != nil {
		writeErr(w, http.StatusConflict, "Todavía no existe la carpeta raíz local")
		return
	}

	// Un escaneo grande puede tardar; se le da margen pero no infinito.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	res := &syncResult{Warnings: []string{}}
	if err := s.syncFolder(ctx, acc, root, acc.RootFolderID, 0, res, &user.ID); err != nil {
		s.audit(r, "drive.sync", "drive", acc.ID.String(), acc.Email, false,
			map[string]any{"error": err.Error()})
		writeErr(w, http.StatusBadGateway, "El escaneo falló: "+err.Error())
		return
	}

	s.audit(r, "drive.sync", "drive", acc.ID.String(), acc.Email, true, map[string]any{
		"carpetas_nuevas":  res.FoldersCreated,
		"archivos_nuevos":  res.FilesImported,
		"bytes_importados": res.BytesImported,
	})
	writeJSON(w, http.StatusOK, res)
}

// syncFolder recorre una carpeta de Drive y su contenido, en profundidad.
func (s *Server) syncFolder(ctx context.Context, acc *models.DriveAccount, localFolder *models.Folder, driveFolderID string, depth int, res *syncResult, ownerID *uuid.UUID) error {
	if depth > syncMaxDepth {
		res.Warnings = append(res.Warnings,
			"Se alcanzó la profundidad máxima en "+localFolder.Path)
		return nil
	}

	pageToken := ""
	for {
		children, next, err := s.drive.ListChildren(ctx, acc, driveFolderID, pageToken)
		if err != nil {
			return err
		}

		for i := range children {
			if res.FilesImported+res.FoldersCreated >= syncMaxItems {
				res.Truncated = true
				return nil
			}
			child := children[i]

			if child.IsFolder() {
				sub, err := s.ensureLocalFolder(ctx, acc, localFolder, &child, res, ownerID)
				if err != nil {
					res.Warnings = append(res.Warnings, child.Name+": "+err.Error())
					continue
				}
				if err := s.syncFolder(ctx, acc, sub, child.ID, depth+1, res, ownerID); err != nil {
					return err
				}
				continue
			}

			// Los documentos nativos de Google no tienen bytes propios; se
			// registran igual para que aparezcan y se puedan compartir, pero
			// su descarga requiere exportarlos (previsto en la Fase 6).
			if _, err := s.repo.FileByDriveID(ctx, child.ID); err == nil {
				res.FilesSkipped++
				continue
			} else if !errors.Is(err, repo.ErrNotFound) {
				return err
			}

			size := child.SizeBytes()
			imported, err := s.repo.ImportFile(ctx, localFolder.ID, child.Name, child.MimeType,
				size, child.ID, child.MD5Checksum, &acc.ID, ownerID)
			if err != nil {
				res.Warnings = append(res.Warnings, child.Name+": "+err.Error())
				continue
			}
			res.FilesImported++
			res.BytesImported += size
			slog.Debug("archivo importado desde Drive", "nombre", imported.Name, "bytes", size)

			if ownerID != nil && size > 0 {
				if err := s.repo.AddUsedBytes(ctx, *ownerID, size); err != nil {
					slog.Error("no se pudo actualizar el espacio usado", "error", err)
				}
			}
		}

		if next == "" {
			return nil
		}
		pageToken = next
	}
}

// ensureLocalFolder devuelve la carpeta local equivalente a una de Drive,
// creándola si hace falta.
func (s *Server) ensureLocalFolder(ctx context.Context, acc *models.DriveAccount, parent *models.Folder, driveFolder *drive.DriveFile, res *syncResult, ownerID *uuid.UUID) (*models.Folder, error) {
	if existing, err := s.repo.FolderByDriveID(ctx, driveFolder.ID); err == nil {
		return existing, nil
	} else if !errors.Is(err, repo.ErrNotFound) {
		return nil, err
	}

	name, err := sanitizeName(driveFolder.Name)
	if err != nil {
		// Un nombre que Drive acepta pero DocuHub no (barras, control…):
		// se importa con los caracteres problemáticos sustituidos.
		name = strings.Map(func(r rune) rune {
			if r < 32 || strings.ContainsRune(`/\<>:"|?*`, r) {
				return '-'
			}
			return r
		}, driveFolder.Name)
		if strings.TrimSpace(name) == "" {
			return nil, errors.New("nombre de carpeta no admitido")
		}
	}

	path := strings.TrimSuffix(parent.Path, "/") + "/" + name
	created, err := s.repo.CreateFolder(ctx, &parent.ID, name, path, driveFolder.ID, &acc.ID, ownerID, false)
	if err != nil {
		// Puede existir ya con el mismo nombre pero sin enlazar a Drive
		// (por ejemplo, creada antes de conectar la cuenta): se reenlaza.
		if sub, ferr := s.repo.FolderByDriveID(ctx, driveFolder.ID); ferr == nil {
			return sub, nil
		}
		return nil, err
	}
	res.FoldersCreated++
	return created, nil
}
