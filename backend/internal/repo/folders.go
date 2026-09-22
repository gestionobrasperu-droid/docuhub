package repo

import (
	"context"

	"github.com/docuhub/docuhub/internal/models"
	"github.com/google/uuid"
)

const folderCols = `id, parent_id, name, path, drive_folder_id, drive_account_id,
	owner_id, is_root, restricted, created_at, updated_at`

func scanFolder(row interface{ Scan(...any) error }) (*models.Folder, error) {
	var f models.Folder
	err := row.Scan(&f.ID, &f.ParentID, &f.Name, &f.Path, &f.DriveFolderID,
		&f.DriveAccountID, &f.OwnerID, &f.IsRoot, &f.Restricted, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		return nil, norm(err)
	}
	return &f, nil
}

func (r *Repo) CreateFolder(ctx context.Context, parentID *uuid.UUID, name, path, driveFolderID string, driveAccountID, ownerID *uuid.UUID, isRoot bool) (*models.Folder, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO folders (parent_id, name, path, drive_folder_id, drive_account_id, owner_id, is_root)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+folderCols,
		parentID, name, path, driveFolderID, driveAccountID, ownerID, isRoot)
	return scanFolder(row)
}

func (r *Repo) FolderByID(ctx context.Context, id uuid.UUID) (*models.Folder, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+folderCols+` FROM folders WHERE id = $1 AND deleted_at IS NULL`, id)
	return scanFolder(row)
}

// RootFolder devuelve la carpeta raíz de la plataforma (hay exactamente una).
func (r *Repo) RootFolder(ctx context.Context) (*models.Folder, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+folderCols+` FROM folders WHERE is_root AND deleted_at IS NULL ORDER BY created_at LIMIT 1`)
	return scanFolder(row)
}

func (r *Repo) ListSubfolders(ctx context.Context, parentID uuid.UUID) ([]*models.Folder, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+folderCols+` FROM folders
		 WHERE parent_id = $1 AND deleted_at IS NULL
		 ORDER BY lower(name)`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*models.Folder{}
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FolderChain devuelve la carpeta y todos sus ancestros, de la raíz hacia
// abajo. Sirve para las migas de pan y para evaluar permisos heredados.
func (r *Repo) FolderChain(ctx context.Context, id uuid.UUID) ([]*models.Folder, error) {
	rows, err := r.db.Query(ctx, `
		WITH RECURSIVE chain AS (
			SELECT `+folderCols+`, 0 AS depth FROM folders WHERE id = $1 AND deleted_at IS NULL
			UNION ALL
			SELECT f.id, f.parent_id, f.name, f.path, f.drive_folder_id, f.drive_account_id,
			       f.owner_id, f.is_root, f.restricted, f.created_at, f.updated_at, c.depth + 1
			FROM folders f JOIN chain c ON f.id = c.parent_id
			WHERE f.deleted_at IS NULL
		)
		SELECT `+folderCols+` FROM chain ORDER BY depth DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*models.Folder{}
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FolderByDriveID localiza una carpeta por su id en Google Drive.
func (r *Repo) FolderByDriveID(ctx context.Context, driveFolderID string) (*models.Folder, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+folderCols+` FROM folders
		 WHERE drive_folder_id = $1 AND deleted_at IS NULL LIMIT 1`, driveFolderID)
	return scanFolder(row)
}

// AttachFolderToDrive enlaza una carpeta local con su equivalente en Drive.
// Se usa al conectar (o reconectar) una cuenta de Google.
func (r *Repo) AttachFolderToDrive(ctx context.Context, id uuid.UUID, driveFolderID string, accountID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		UPDATE folders SET drive_folder_id = $2, drive_account_id = $3, updated_at = now()
		WHERE id = $1`, id, driveFolderID, accountID)
	return err
}

// SetFolderRestricted activa o desactiva el modo "solo permisos explícitos".
func (r *Repo) SetFolderRestricted(ctx context.Context, id uuid.UUID, restricted bool) error {
	_, err := r.db.Exec(ctx,
		`UPDATE folders SET restricted = $2, updated_at = now() WHERE id = $1`, id, restricted)
	return err
}

func (r *Repo) RenameFolder(ctx context.Context, id uuid.UUID, name, path string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE folders SET name = $2, path = $3, updated_at = now() WHERE id = $1`, id, name, path)
	return err
}

// SoftDeleteFolder marca la carpeta y, por el ON DELETE del árbol lógico,
// también sus archivos. No borra nada en Drive: eso lo decide el handler.
func (r *Repo) SoftDeleteFolder(ctx context.Context, id uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `
		WITH RECURSIVE tree AS (
			SELECT id FROM folders WHERE id = $1
			UNION ALL
			SELECT f.id FROM folders f JOIN tree t ON f.parent_id = t.id
		)
		UPDATE folders SET deleted_at = now() WHERE id IN (SELECT id FROM tree)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		WITH RECURSIVE tree AS (
			SELECT id FROM folders WHERE id = $1
			UNION ALL
			SELECT f.id FROM folders f JOIN tree t ON f.parent_id = t.id
		)
		UPDATE files SET deleted_at = now() WHERE folder_id IN (SELECT id FROM tree)`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// FolderSize suma el tamaño de todos los archivos vivos bajo una carpeta.
func (r *Repo) FolderSize(ctx context.Context, id uuid.UUID) (int64, int64, error) {
	var bytes *int64
	var count int64
	err := r.db.QueryRow(ctx, `
		WITH RECURSIVE tree AS (
			SELECT id FROM folders WHERE id = $1 AND deleted_at IS NULL
			UNION ALL
			SELECT f.id FROM folders f JOIN tree t ON f.parent_id = t.id WHERE f.deleted_at IS NULL
		)
		SELECT sum(size_bytes), count(*) FROM files
		WHERE folder_id IN (SELECT id FROM tree) AND deleted_at IS NULL`, id).Scan(&bytes, &count)
	if err != nil {
		return 0, 0, err
	}
	if bytes == nil {
		return 0, count, nil
	}
	return *bytes, count, nil
}
