package repo

import (
	"context"
	"strings"

	"github.com/docuhub/docuhub/internal/models"
	"github.com/google/uuid"
)

const fileCols = `f.id, f.folder_id, f.name, f.mime_type, f.size_bytes, f.md5_checksum,
	f.drive_file_id, f.drive_account_id, f.owner_id, f.version, f.download_count,
	f.status, f.tags, f.description, f.created_at, f.updated_at`

func scanFile(row interface{ Scan(...any) error }) (*models.File, error) {
	var f models.File
	var email *string
	err := row.Scan(&f.ID, &f.FolderID, &f.Name, &f.MimeType, &f.SizeBytes, &f.MD5,
		&f.DriveFileID, &f.DriveAccountID, &f.OwnerID, &f.Version, &f.DownloadCount,
		&f.Status, &f.Tags, &f.Description, &f.CreatedAt, &f.UpdatedAt, &email)
	if err != nil {
		return nil, norm(err)
	}
	if email != nil {
		f.OwnerEmail = *email
	}
	if f.Tags == nil {
		f.Tags = []string{}
	}
	return &f, nil
}

const fileSelect = `SELECT ` + fileCols + `, u.email
	FROM files f LEFT JOIN users u ON u.id = f.owner_id`

// CreateFile registra el archivo antes de subirlo, en estado "uploading".
// Si la subida falla, la fila queda como rastro y la limpia el mantenimiento.
func (r *Repo) CreateFile(ctx context.Context, folderID uuid.UUID, name, mimeType string, size int64, ownerID *uuid.UUID, driveAccountID *uuid.UUID) (*models.File, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx, `
		INSERT INTO files (folder_id, name, mime_type, size_bytes, owner_id, drive_account_id, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'uploading')
		RETURNING id`, folderID, name, mimeType, size, ownerID, driveAccountID).Scan(&id)
	if err != nil {
		return nil, norm(err)
	}
	return r.FileByID(ctx, id)
}

func (r *Repo) FileByID(ctx context.Context, id uuid.UUID) (*models.File, error) {
	row := r.db.QueryRow(ctx, fileSelect+` WHERE f.id = $1 AND f.deleted_at IS NULL`, id)
	return scanFile(row)
}

func (r *Repo) ListFiles(ctx context.Context, folderID uuid.UUID) ([]*models.File, error) {
	rows, err := r.db.Query(ctx, fileSelect+`
		WHERE f.folder_id = $1 AND f.deleted_at IS NULL AND f.status = 'ready'
		ORDER BY lower(f.name)`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*models.File{}
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SearchFiles busca por nombre, descripción o etiqueta en toda la plataforma.
// El filtrado por permisos lo aplica la capa HTTP sobre el resultado.
func (r *Repo) SearchFiles(ctx context.Context, query string, limit int) ([]*models.File, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	pattern := "%" + strings.ToLower(strings.TrimSpace(query)) + "%"
	rows, err := r.db.Query(ctx, fileSelect+`
		WHERE f.deleted_at IS NULL AND f.status = 'ready'
		  AND (lower(f.name) LIKE $1 OR lower(f.description) LIKE $1
		       OR EXISTS (SELECT 1 FROM unnest(f.tags) t WHERE lower(t) LIKE $1))
		ORDER BY f.updated_at DESC
		LIMIT $2`, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*models.File{}
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// MarkFileReady cierra la subida: fija el id de Drive, el tamaño real y el
// checksum que devolvió Google.
func (r *Repo) MarkFileReady(ctx context.Context, id uuid.UUID, driveFileID string, size int64, md5 string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE files
		SET drive_file_id = $2, size_bytes = $3, md5_checksum = $4,
		    status = 'ready', updated_at = now()
		WHERE id = $1`, id, driveFileID, size, md5)
	return err
}

func (r *Repo) MarkFileFailed(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE files SET status = 'failed', updated_at = now() WHERE id = $1`, id)
	return err
}

func (r *Repo) UpdateFileMeta(ctx context.Context, id uuid.UUID, name, description *string, tags []string) (*models.File, error) {
	_, err := r.db.Exec(ctx, `
		UPDATE files SET
			name        = COALESCE($2::text, name),
			description = COALESCE($3::text, description),
			tags        = COALESCE($4::text[], tags),
			updated_at  = now()
		WHERE id = $1`, id, name, description, tags)
	if err != nil {
		return nil, norm(err)
	}
	return r.FileByID(ctx, id)
}

func (r *Repo) MoveFile(ctx context.Context, id, folderID uuid.UUID) error {
	_, err := r.db.Exec(ctx,
		`UPDATE files SET folder_id = $2, updated_at = now() WHERE id = $1`, id, folderID)
	return err
}

func (r *Repo) SoftDeleteFile(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE files SET deleted_at = now() WHERE id = $1`, id)
	return err
}

// RegisterDownload incrementa el contador y marca el último acceso. Se llama
// cuando la transferencia termina, no cuando empieza.
func (r *Repo) RegisterDownload(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx,
		`UPDATE files SET download_count = download_count + 1, last_access_at = now() WHERE id = $1`, id)
	return err
}

// AddVersion archiva la versión anterior antes de que la nueva la reemplace.
func (r *Repo) AddVersion(ctx context.Context, fileID uuid.UUID, version int, driveFileID string, size int64, md5 string, createdBy *uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO file_versions (file_id, version, drive_file_id, size_bytes, md5_checksum, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (file_id, version) DO NOTHING`,
		fileID, version, driveFileID, size, md5, createdBy)
	return err
}

func (r *Repo) BumpVersion(ctx context.Context, fileID uuid.UUID) (int, error) {
	var v int
	err := r.db.QueryRow(ctx,
		`UPDATE files SET version = version + 1, updated_at = now() WHERE id = $1 RETURNING version`,
		fileID).Scan(&v)
	return v, norm(err)
}

// FileByNameInFolder detecta colisiones para decidir entre crear o versionar.
func (r *Repo) FileByNameInFolder(ctx context.Context, folderID uuid.UUID, name string) (*models.File, error) {
	row := r.db.QueryRow(ctx, fileSelect+`
		WHERE f.folder_id = $1 AND lower(f.name) = lower($2)
		  AND f.deleted_at IS NULL AND f.status = 'ready'
		LIMIT 1`, folderID, name)
	return scanFile(row)
}
