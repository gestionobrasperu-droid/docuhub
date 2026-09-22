package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/docuhub/docuhub/internal/models"
	"github.com/docuhub/docuhub/internal/repo"
	"github.com/google/uuid"
)

// Modelo de acceso, en corto:
//
//   - Cada carpeta puede ser "abierta" o "restringida".
//   - En una carpeta abierta, el rol global del usuario le da un nivel base
//     (un member puede subir, un guest solo mirar). Es lo que hace que la
//     plataforma sea usable el primer día, sin configurar nada.
//   - En cuanto una carpeta de la cadena está marcada como restringida, el
//     nivel base desaparece: solo entra quien tenga un permiso explícito
//     sobre ella o sobre alguna de sus subcarpetas.
//   - Los permisos explícitos solo suman; nunca restan.
//
// Así, "Contabilidad 2026" se marca restringida y queda invisible para el
// resto, mientras "Planos" sigue siendo de acceso general.

// baseLevel es el nivel que otorga el rol global en carpetas no restringidas.
func baseLevel(u *models.User) string {
	switch u.Role {
	case models.RoleAdmin, models.RoleManager:
		return models.LevelManager
	case models.RoleMember:
		return models.LevelEditor
	default:
		return models.LevelViewer
	}
}

func atLeast(level, required string) bool {
	return models.LevelRank[level] >= models.LevelRank[required]
}

// folderAccess devuelve la carpeta, su cadena de ancestros y el nivel efectivo.
func (s *Server) folderAccess(ctx context.Context, user *models.User, folderID uuid.UUID) (*models.Folder, []*models.Folder, string, error) {
	chain, err := s.repo.FolderChain(ctx, folderID)
	if err != nil {
		return nil, nil, "", err
	}
	if len(chain) == 0 {
		return nil, nil, "", repo.ErrNotFound
	}
	folder := chain[len(chain)-1]

	level, err := s.levelForChain(ctx, user, chain, "folder", folderID, folder.OwnerID)
	if err != nil {
		return nil, nil, "", err
	}
	return folder, chain, level, nil
}

// fileAccess resuelve el nivel efectivo sobre un archivo concreto.
func (s *Server) fileAccess(ctx context.Context, user *models.User, fileID uuid.UUID) (*models.File, []*models.Folder, string, error) {
	file, err := s.repo.FileByID(ctx, fileID)
	if err != nil {
		return nil, nil, "", err
	}
	chain, err := s.repo.FolderChain(ctx, file.FolderID)
	if err != nil {
		return nil, nil, "", err
	}
	level, err := s.levelForChain(ctx, user, chain, "file", fileID, file.OwnerID)
	if err != nil {
		return nil, nil, "", err
	}
	return file, chain, level, nil
}

// levelForChain combina el nivel base con los permisos explícitos.
func (s *Server) levelForChain(ctx context.Context, user *models.User, chain []*models.Folder, resType string, resID uuid.UUID, ownerID *uuid.UUID) (string, error) {
	ids := make([]uuid.UUID, 0, len(chain))
	restricted := false
	for _, f := range chain {
		ids = append(ids, f.ID)
		if f.Restricted {
			restricted = true
		}
	}

	explicit, err := s.repo.EffectiveLevel(ctx, user, resType, resID, ids, ownerID)
	if err != nil {
		return "", err
	}

	if restricted {
		// Sin permiso explícito no hay acceso, por mucho rol que se tenga.
		// Los administradores sí pasan: EffectiveLevel ya los resolvió arriba.
		return explicit, nil
	}

	base := baseLevel(user)
	if models.LevelRank[explicit] > models.LevelRank[base] {
		return explicit, nil
	}
	return base, nil
}

// requireFolder resuelve la carpeta del parámetro de ruta y exige un nivel.
// Responde al cliente y devuelve ok=false cuando algo falla.
func (s *Server) requireFolder(w http.ResponseWriter, r *http.Request, param, level string) (*models.Folder, []*models.Folder, bool) {
	id, ok := parseUUID(w, r, param)
	if !ok {
		return nil, nil, false
	}
	user := userFrom(r.Context())

	folder, chain, got, err := s.folderAccess(r.Context(), user, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "La carpeta no existe")
		} else {
			writeErr(w, http.StatusInternalServerError, "No se pudo resolver la carpeta")
		}
		return nil, nil, false
	}
	if !atLeast(got, level) {
		// 404 en lugar de 403 cuando ni siquiera puede verla: así no se
		// filtra la existencia de carpetas restringidas.
		if got == "" {
			writeErr(w, http.StatusNotFound, "La carpeta no existe")
		} else {
			writeErr(w, http.StatusForbidden, "No tienes permisos suficientes sobre esta carpeta")
		}
		return nil, nil, false
	}
	return folder, chain, true
}

// requireFile es el equivalente para archivos.
func (s *Server) requireFile(w http.ResponseWriter, r *http.Request, param, level string) (*models.File, bool) {
	id, ok := parseUUID(w, r, param)
	if !ok {
		return nil, false
	}
	user := userFrom(r.Context())

	file, _, got, err := s.fileAccess(r.Context(), user, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "El archivo no existe")
		} else {
			writeErr(w, http.StatusInternalServerError, "No se pudo resolver el archivo")
		}
		return nil, false
	}
	if !atLeast(got, level) {
		if got == "" {
			writeErr(w, http.StatusNotFound, "El archivo no existe")
		} else {
			writeErr(w, http.StatusForbidden, "No tienes permisos suficientes sobre este archivo")
		}
		return nil, false
	}
	return file, true
}

// visibleFolders filtra una lista de subcarpetas dejando solo las que el
// usuario puede al menos ver. Evita que una carpeta restringida aparezca
// listada aunque no se pueda abrir.
func (s *Server) visibleFolders(ctx context.Context, user *models.User, parentChain []*models.Folder, children []*models.Folder) ([]*models.Folder, error) {
	out := make([]*models.Folder, 0, len(children))
	for _, child := range children {
		chain := append(append([]*models.Folder{}, parentChain...), child)
		level, err := s.levelForChain(ctx, user, chain, "folder", child.ID, child.OwnerID)
		if err != nil {
			return nil, err
		}
		if atLeast(level, models.LevelViewer) {
			out = append(out, child)
		}
	}
	return out, nil
}
