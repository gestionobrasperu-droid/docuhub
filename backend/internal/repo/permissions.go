package repo

import (
	"context"

	"github.com/docuhub/docuhub/internal/models"
	"github.com/google/uuid"
)

// EffectiveLevel calcula qué puede hacer un usuario sobre un recurso.
//
// Reglas, en orden:
//  1. admin y manager globales mandan sobre todo.
//  2. el dueño del recurso lo administra.
//  3. se toma el nivel más alto entre los permisos directos del usuario, los
//     de sus grupos, y los heredados de cualquier carpeta ancestro.
//
// Devuelve "" si no tiene ningún acceso.
func (r *Repo) EffectiveLevel(ctx context.Context, user *models.User, resourceType string, resourceID uuid.UUID, folderChain []uuid.UUID, ownerID *uuid.UUID) (string, error) {
	if user.Role == models.RoleAdmin || user.Role == models.RoleManager {
		return models.LevelManager, nil
	}
	if ownerID != nil && *ownerID == user.ID {
		return models.LevelManager, nil
	}

	groups, err := r.GroupIDsForUser(ctx, user.ID)
	if err != nil {
		return "", err
	}

	fileID := uuid.Nil
	if resourceType == "file" {
		fileID = resourceID
	} else {
		folderChain = append(folderChain, resourceID)
	}

	rows, err := r.db.Query(ctx, `
		SELECT level FROM permissions
		WHERE (expires_at IS NULL OR expires_at > now())
		  AND (
			(subject_type = 'user'  AND subject_id = $1) OR
			(subject_type = 'group' AND subject_id = ANY($2))
		  )
		  AND (
			(resource_type = 'file'   AND resource_id = $3) OR
			(resource_type = 'folder' AND resource_id = ANY($4))
		  )`, user.ID, groups, fileID, folderChain)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	best := ""
	for rows.Next() {
		var level string
		if err := rows.Scan(&level); err != nil {
			return "", err
		}
		if models.LevelRank[level] > models.LevelRank[best] {
			best = level
		}
	}
	return best, rows.Err()
}

func (r *Repo) GrantPermission(ctx context.Context, subjectType string, subjectID uuid.UUID, resourceType string, resourceID uuid.UUID, level string, grantedBy *uuid.UUID, expiresAt *string) (*models.Permission, error) {
	var p models.Permission
	err := r.db.QueryRow(ctx, `
		INSERT INTO permissions (subject_type, subject_id, resource_type, resource_id, level, granted_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7::timestamptz)
		ON CONFLICT (subject_type, subject_id, resource_type, resource_id)
		DO UPDATE SET level = EXCLUDED.level, expires_at = EXCLUDED.expires_at, granted_by = EXCLUDED.granted_by
		RETURNING id, subject_type, subject_id, resource_type, resource_id, level, created_at, expires_at`,
		subjectType, subjectID, resourceType, resourceID, level, grantedBy, expiresAt,
	).Scan(&p.ID, &p.SubjectType, &p.SubjectID, &p.ResourceType, &p.ResourceID,
		&p.Level, &p.CreatedAt, &p.ExpiresAt)
	if err != nil {
		return nil, norm(err)
	}
	return &p, nil
}

func (r *Repo) PermissionByID(ctx context.Context, id uuid.UUID) (*models.Permission, error) {
	var p models.Permission
	err := r.db.QueryRow(ctx, `
		SELECT id, subject_type, subject_id, resource_type, resource_id, level, created_at, expires_at
		FROM permissions WHERE id = $1`, id,
	).Scan(&p.ID, &p.SubjectType, &p.SubjectID, &p.ResourceType, &p.ResourceID,
		&p.Level, &p.CreatedAt, &p.ExpiresAt)
	if err != nil {
		return nil, norm(err)
	}
	return &p, nil
}

func (r *Repo) RevokePermission(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM permissions WHERE id = $1`, id)
	return err
}

// ListPermissions devuelve los permisos de un recurso con una etiqueta legible
// del sujeto, para pintarlos en la interfaz sin más consultas.
func (r *Repo) ListPermissions(ctx context.Context, resourceType string, resourceID uuid.UUID) ([]*models.Permission, error) {
	rows, err := r.db.Query(ctx, `
		SELECT p.id, p.subject_type, p.subject_id, p.resource_type, p.resource_id,
		       p.level, p.created_at, p.expires_at,
		       COALESCE(u.email, g.name, '') AS label
		FROM permissions p
		LEFT JOIN users  u ON p.subject_type = 'user'  AND u.id = p.subject_id
		LEFT JOIN groups g ON p.subject_type = 'group' AND g.id = p.subject_id
		WHERE p.resource_type = $1 AND p.resource_id = $2
		ORDER BY p.created_at`, resourceType, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*models.Permission{}
	for rows.Next() {
		var p models.Permission
		if err := rows.Scan(&p.ID, &p.SubjectType, &p.SubjectID, &p.ResourceType,
			&p.ResourceID, &p.Level, &p.CreatedAt, &p.ExpiresAt, &p.SubjectLabel); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}
