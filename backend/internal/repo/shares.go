package repo

import (
	"context"
	"time"

	"github.com/docuhub/docuhub/internal/models"
	"github.com/google/uuid"
)

const shareCols = `id, file_id, folder_id, created_by,
	(password_hash <> '') AS has_password, max_downloads, download_count,
	allow_upload, note, expires_at, created_at, last_used_at, revoked_at`

func scanShare(row interface{ Scan(...any) error }) (*models.ShareLink, error) {
	var s models.ShareLink
	err := row.Scan(&s.ID, &s.FileID, &s.FolderID, &s.CreatedBy, &s.HasPassword,
		&s.MaxDownloads, &s.DownloadCount, &s.AllowUpload, &s.Note,
		&s.ExpiresAt, &s.CreatedAt, &s.LastUsedAt, &s.RevokedAt)
	if err != nil {
		return nil, norm(err)
	}
	return &s, nil
}

func (r *Repo) CreateShare(ctx context.Context, tokenHash string, fileID, folderID *uuid.UUID, createdBy uuid.UUID, passwordHash string, maxDownloads int, allowUpload bool, note string, expiresAt *time.Time) (*models.ShareLink, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO share_links
			(token_hash, file_id, folder_id, created_by, password_hash, max_downloads, allow_upload, note, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+shareCols,
		tokenHash, fileID, folderID, createdBy, passwordHash, maxDownloads, allowUpload, note, expiresAt)
	return scanShare(row)
}

// ShareByToken solo devuelve enlaces utilizables: ni revocados, ni vencidos,
// ni agotados. La comprobación vive en SQL para que no se pueda olvidar.
func (r *Repo) ShareByToken(ctx context.Context, tokenHash string) (*models.ShareLink, string, error) {
	var s models.ShareLink
	var pwHash string
	err := r.db.QueryRow(ctx, `
		SELECT id, file_id, folder_id, created_by, password_hash, max_downloads,
		       download_count, allow_upload, note, expires_at, created_at, last_used_at, revoked_at
		FROM share_links
		WHERE token_hash = $1
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > now())
		  AND (max_downloads = 0 OR download_count < max_downloads)`, tokenHash,
	).Scan(&s.ID, &s.FileID, &s.FolderID, &s.CreatedBy, &pwHash, &s.MaxDownloads,
		&s.DownloadCount, &s.AllowUpload, &s.Note, &s.ExpiresAt, &s.CreatedAt,
		&s.LastUsedAt, &s.RevokedAt)
	if err != nil {
		return nil, "", norm(err)
	}
	s.HasPassword = pwHash != ""
	return &s, pwHash, nil
}

func (r *Repo) RegisterShareUse(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx,
		`UPDATE share_links SET download_count = download_count + 1, last_used_at = now() WHERE id = $1`, id)
	return err
}

func (r *Repo) RevokeShare(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx,
		`UPDATE share_links SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	return err
}

// ListShares lista los enlaces creados por un usuario, o todos si es admin.
func (r *Repo) ListShares(ctx context.Context, createdBy *uuid.UUID) ([]*models.ShareLink, error) {
	rows, err := r.db.Query(ctx, `SELECT `+shareCols+` FROM share_links
		WHERE ($1::uuid IS NULL OR created_by = $1)
		ORDER BY created_at DESC LIMIT 500`, createdBy)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*models.ShareLink{}
	for rows.Next() {
		s, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
