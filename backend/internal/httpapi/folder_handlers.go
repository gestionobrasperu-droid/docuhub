package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/docuhub/docuhub/internal/models"
	"github.com/docuhub/docuhub/internal/repo"
	"github.com/google/uuid"
)

// primaryAccount devuelve la cuenta de Drive que recibe los archivos nuevos.
func (s *Server) primaryAccount(ctx context.Context) (*models.DriveAccount, error) {
	acc, err := s.repo.PrimaryAccount(ctx)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, errDriveNotConnected
		}
		return nil, err
	}
	if acc.Status != "active" {
		return nil, errDriveNotConnected
	}
	return acc, nil
}

var errDriveNotConnected = errors.New("no hay ninguna cuenta de Google Drive conectada; un administrador debe enlazarla desde Administración → Google Drive")

func (s *Server) handleRootFolder(w http.ResponseWriter, r *http.Request) {
	root, err := s.repo.RootFolder(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError,
			"La carpeta raíz no está creada todavía. Conecta Google Drive desde Administración.")
		return
	}
	s.writeFolderPayload(w, r, root.ID)
}

func (s *Server) handleFolderContents(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	s.writeFolderPayload(w, r, id)
}

// writeFolderPayload arma todo lo que la pantalla del explorador necesita en
// una sola respuesta: migas de pan, subcarpetas visibles, archivos, el nivel
// de acceso del usuario y el tamaño acumulado.
func (s *Server) writeFolderPayload(w http.ResponseWriter, r *http.Request, folderID uuid.UUID) {
	user := userFrom(r.Context())

	folder, chain, level, err := s.folderAccess(r.Context(), user, folderID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "La carpeta no existe")
		} else {
			writeErr(w, http.StatusInternalServerError, "No se pudo abrir la carpeta")
		}
		return
	}
	if !atLeast(level, models.LevelViewer) {
		writeErr(w, http.StatusNotFound, "La carpeta no existe")
		return
	}

	children, err := s.repo.ListSubfolders(r.Context(), folder.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudieron listar las subcarpetas")
		return
	}
	visible, err := s.visibleFolders(r.Context(), user, chain, children)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudieron filtrar las subcarpetas")
		return
	}

	files := []*models.File{}
	if atLeast(level, models.LevelViewer) {
		files, err = s.repo.ListFiles(r.Context(), folder.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "No se pudieron listar los archivos")
			return
		}
	}

	size, count, err := s.repo.FolderSize(r.Context(), folder.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo calcular el tamaño")
		return
	}

	// La cadena incluye la propia carpeta al final; las migas son el resto.
	breadcrumb := chain
	if len(breadcrumb) > 0 {
		breadcrumb = breadcrumb[:len(breadcrumb)-1]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"folder":      folder,
		"breadcrumb":  breadcrumb,
		"folders":     visible,
		"files":       files,
		"level":       level,
		"total_bytes": size,
		"file_count":  count,
		"can_upload":  atLeast(level, models.LevelEditor),
		"can_manage":  atLeast(level, models.LevelManager),
	})
}

type createFolderRequest struct {
	ParentID   *uuid.UUID `json:"parent_id"`
	Name       string     `json:"name"`
	Restricted bool       `json:"restricted"`
}

func (s *Server) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	var req createFolderRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, err := sanitizeName(req.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// Sin padre explícito se crea colgando de la raíz.
	var parent *models.Folder
	if req.ParentID != nil {
		parent, err = s.repo.FolderByID(r.Context(), *req.ParentID)
	} else {
		parent, err = s.repo.RootFolder(r.Context())
	}
	if err != nil {
		writeErr(w, http.StatusNotFound, "La carpeta de destino no existe")
		return
	}

	_, _, level, err := s.folderAccess(r.Context(), user, parent.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo verificar el destino")
		return
	}
	if !atLeast(level, models.LevelEditor) {
		writeErr(w, http.StatusForbidden, "No puedes crear carpetas aquí")
		return
	}

	acc, err := s.primaryAccount(r.Context())
	if err != nil {
		writeErr(w, http.StatusFailedDependency, err.Error())
		return
	}

	parentDriveID := parent.DriveFolderID
	if parentDriveID == "" {
		parentDriveID = acc.RootFolderID
	}
	driveFolder, err := s.drive.EnsureFolder(r.Context(), acc, name, parentDriveID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Google Drive rechazó la creación: "+err.Error())
		return
	}

	path := strings.TrimSuffix(parent.Path, "/") + "/" + name
	created, err := s.repo.CreateFolder(r.Context(), &parent.ID, name, path,
		driveFolder.ID, &acc.ID, &user.ID, false)
	if err != nil {
		writeErr(w, http.StatusConflict, "Ya existe una carpeta con ese nombre aquí")
		return
	}
	if req.Restricted {
		if err := s.repo.SetFolderRestricted(r.Context(), created.ID, true); err != nil {
			writeErr(w, http.StatusInternalServerError, "La carpeta se creó pero no se pudo restringir")
			return
		}
		created.Restricted = true
	}

	s.audit(r, "folder.create", "folder", created.ID.String(), created.Path, true,
		map[string]any{"restringida": req.Restricted})
	writeJSON(w, http.StatusCreated, created)
}

type updateFolderRequest struct {
	Name       *string `json:"name"`
	Restricted *bool   `json:"restricted"`
}

func (s *Server) handleRenameFolder(w http.ResponseWriter, r *http.Request) {
	folder, chain, ok := s.requireFolder(w, r, "id", models.LevelEditor)
	if !ok {
		return
	}
	if folder.IsRoot {
		writeErr(w, http.StatusBadRequest, "La carpeta raíz no se puede modificar")
		return
	}

	var req updateFolderRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Restricted != nil {
		user := userFrom(r.Context())
		level, err := s.levelForChain(r.Context(), user, chain, "folder", folder.ID, folder.OwnerID)
		if err != nil || !atLeast(level, models.LevelManager) {
			writeErr(w, http.StatusForbidden, "Solo un responsable de la carpeta puede cambiar su visibilidad")
			return
		}
		if err := s.repo.SetFolderRestricted(r.Context(), folder.ID, *req.Restricted); err != nil {
			writeErr(w, http.StatusInternalServerError, "No se pudo cambiar la visibilidad")
			return
		}
		folder.Restricted = *req.Restricted
	}

	if req.Name != nil {
		name, err := sanitizeName(*req.Name)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		parentPath := "/"
		if len(chain) > 1 {
			parentPath = chain[len(chain)-2].Path
		}
		newPath := strings.TrimSuffix(parentPath, "/") + "/" + name

		if folder.DriveFolderID != "" {
			if acc, err := s.accountFor(r.Context(), folder.DriveAccountID); err == nil {
				if err := s.drive.Rename(r.Context(), acc, folder.DriveFolderID, name); err != nil {
					writeErr(w, http.StatusBadGateway, "No se pudo renombrar en Google Drive: "+err.Error())
					return
				}
			}
		}
		if err := s.repo.RenameFolder(r.Context(), folder.ID, name, newPath); err != nil {
			writeErr(w, http.StatusConflict, "Ya existe una carpeta con ese nombre")
			return
		}
		folder.Name, folder.Path = name, newPath
	}

	s.audit(r, "folder.update", "folder", folder.ID.String(), folder.Path, true, nil)
	writeJSON(w, http.StatusOK, folder)
}

func (s *Server) handleDeleteFolder(w http.ResponseWriter, r *http.Request) {
	folder, _, ok := s.requireFolder(w, r, "id", models.LevelManager)
	if !ok {
		return
	}
	if folder.IsRoot {
		writeErr(w, http.StatusBadRequest, "La carpeta raíz no se puede eliminar")
		return
	}

	if err := s.repo.SoftDeleteFolder(r.Context(), folder.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo eliminar la carpeta")
		return
	}

	// En Drive va a la papelera, no se borra: 30 días para arrepentirse.
	if folder.DriveFolderID != "" {
		if acc, err := s.accountFor(r.Context(), folder.DriveAccountID); err == nil {
			if err := s.drive.Trash(r.Context(), acc, folder.DriveFolderID); err != nil {
				s.audit(r, "folder.delete", "folder", folder.ID.String(), folder.Path, false,
					map[string]any{"drive": err.Error()})
			}
		}
	}

	s.audit(r, "folder.delete", "folder", folder.ID.String(), folder.Path, true, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// accountFor resuelve la cuenta de Drive de un recurso, con la primaria como
// respaldo para datos creados antes de que hubiera varias cuentas.
func (s *Server) accountFor(ctx context.Context, id *uuid.UUID) (*models.DriveAccount, error) {
	if id != nil {
		if acc, err := s.repo.AccountByID(ctx, *id); err == nil && acc.Status == "active" {
			return acc, nil
		}
	}
	return s.primaryAccount(ctx)
}

// sanitizeName valida nombres de carpeta y archivo. Se rechazan los caracteres
// que rompen rutas en Windows y los nombres de navegación.
func sanitizeName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("El nombre no puede estar vacío")
	}
	if len(name) > 255 {
		return "", errors.New("El nombre es demasiado largo (máximo 255 caracteres)")
	}
	if name == "." || name == ".." {
		return "", errors.New("Ese nombre no está permitido")
	}
	if strings.ContainsAny(name, `/\<>:"|?*`) {
		return "", errors.New(`El nombre no puede contener / \ < > : " | ? *`)
	}
	for _, r := range name {
		if r < 32 {
			return "", errors.New("El nombre contiene caracteres de control")
		}
	}
	return name, nil
}
