package repo

import (
	"context"
	"time"

	"github.com/docuhub/docuhub/internal/models"
	"github.com/google/uuid"
)

const uploadCols = `id, user_id, folder_id, file_id, drive_account_id, file_name, mime_type,
	size_bytes, bytes_received, session_url_enc, status, error, created_at, expires_at`

func scanUpload(row interface{ Scan(...any) error }) (*models.Upload, error) {
	var u models.Upload
	err := row.Scan(&u.ID, &u.UserID, &u.FolderID, &u.FileID, &u.DriveAccountID,
		&u.FileName, &u.MimeType, &u.SizeBytes, &u.BytesReceived, &u.SessionURLEnc,
		&u.Status, &u.Error, &u.CreatedAt, &u.ExpiresAt)
	if err != nil {
		return nil, norm(err)
	}
	return &u, nil
}

func (r *Repo) CreateUpload(ctx context.Context, userID, folderID uuid.UUID, fileID *uuid.UUID, driveAccountID *uuid.UUID, name, mime string, size int64, sessionURLEnc string, expiresAt time.Time) (*models.Upload, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO uploads (user_id, folder_id, file_id, drive_account_id, file_name,
		                     mime_type, size_bytes, session_url_enc, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+uploadCols,
		userID, folderID, fileID, driveAccountID, name, mime, size, sessionURLEnc, expiresAt)
	return scanUpload(row)
}

func (r *Repo) UploadByID(ctx context.Context, id uuid.UUID) (*models.Upload, error) {
	row := r.db.QueryRow(ctx, `SELECT `+uploadCols+` FROM uploads WHERE id = $1`, id)
	return scanUpload(row)
}

// SetUploadProgress guarda el avance confirmado por Drive. Es lo que permite
// reanudar tras un corte: el cliente pregunta y sigue desde ese byte.
func (r *Repo) SetUploadProgress(ctx context.Context, id uuid.UUID, bytesReceived int64) error {
	_, err := r.db.Exec(ctx,
		`UPDATE uploads SET bytes_received = $2, updated_at = now() WHERE id = $1`, id, bytesReceived)
	return err
}

func (r *Repo) FinishUpload(ctx context.Context, id uuid.UUID, status, errMsg string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE uploads SET status = $2, error = $3, updated_at = now() WHERE id = $1`,
		id, status, errMsg)
	return err
}

func (r *Repo) ListActiveUploads(ctx context.Context, userID uuid.UUID) ([]*models.Upload, error) {
	rows, err := r.db.Query(ctx, `SELECT `+uploadCols+` FROM uploads
		WHERE user_id = $1 AND status = 'active' AND expires_at > now()
		ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*models.Upload{}
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// PurgeStaleUploads marca como abortadas las sesiones vencidas y borra las
// filas de archivos que nunca llegaron a completarse.
func (r *Repo) PurgeStaleUploads(ctx context.Context) (int64, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE uploads SET status = 'aborted', error = 'sesión vencida', updated_at = now()
		WHERE status = 'active' AND expires_at < now()`)
	if err != nil {
		return 0, err
	}
	if _, err := r.db.Exec(ctx, `
		DELETE FROM files
		WHERE status = 'uploading' AND created_at < now() - interval '2 days'`); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
