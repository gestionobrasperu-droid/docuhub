package repo

import (
	"context"
	"time"

	"github.com/docuhub/docuhub/internal/models"
	"github.com/google/uuid"
)

const accCols = `id, email, display_name, refresh_token_enc, access_token_enc,
	access_expires_at, drive_id, root_folder_id, quota_total_bytes, quota_used_bytes,
	quota_checked_at, is_primary, status, last_error, created_at`

func scanAccount(row interface{ Scan(...any) error }) (*models.DriveAccount, error) {
	var a models.DriveAccount
	err := row.Scan(&a.ID, &a.Email, &a.DisplayName, &a.RefreshTokenEnc, &a.AccessTokenEnc,
		&a.AccessExpiresAt, &a.DriveID, &a.RootFolderID, &a.QuotaTotalBytes,
		&a.QuotaUsedBytes, &a.QuotaCheckedAt, &a.IsPrimary, &a.Status, &a.LastError, &a.CreatedAt)
	if err != nil {
		return nil, norm(err)
	}
	return &a, nil
}

// UpsertDriveAccount guarda la cuenta recién autorizada. Si ya existía (misma
// dirección de correo), actualiza el refresh token y la reactiva.
func (r *Repo) UpsertDriveAccount(ctx context.Context, email, displayName, refreshEnc, accessEnc string, accessExp time.Time, driveID string) (*models.DriveAccount, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO drive_accounts
			(email, display_name, refresh_token_enc, access_token_enc, access_expires_at, drive_id,
			 is_primary, status)
		VALUES ($1, $2, $3, $4, $5, $6,
			NOT EXISTS (SELECT 1 FROM drive_accounts WHERE status = 'active'), 'active')
		ON CONFLICT (lower(email)) DO UPDATE SET
			display_name      = EXCLUDED.display_name,
			refresh_token_enc = EXCLUDED.refresh_token_enc,
			access_token_enc  = EXCLUDED.access_token_enc,
			access_expires_at = EXCLUDED.access_expires_at,
			drive_id          = EXCLUDED.drive_id,
			status            = 'active',
			last_error        = '',
			updated_at        = now()
		RETURNING `+accCols,
		email, displayName, refreshEnc, accessEnc, accessExp, driveID)
	return scanAccount(row)
}

// PrimaryAccount devuelve la cuenta que recibe los archivos nuevos.
func (r *Repo) PrimaryAccount(ctx context.Context) (*models.DriveAccount, error) {
	row := r.db.QueryRow(ctx, `SELECT `+accCols+` FROM drive_accounts
		WHERE status = 'active'
		ORDER BY is_primary DESC, created_at
		LIMIT 1`)
	return scanAccount(row)
}

func (r *Repo) AccountByID(ctx context.Context, id uuid.UUID) (*models.DriveAccount, error) {
	row := r.db.QueryRow(ctx, `SELECT `+accCols+` FROM drive_accounts WHERE id = $1`, id)
	return scanAccount(row)
}

func (r *Repo) ListDriveAccounts(ctx context.Context) ([]*models.DriveAccount, error) {
	rows, err := r.db.Query(ctx, `SELECT `+accCols+` FROM drive_accounts ORDER BY is_primary DESC, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*models.DriveAccount{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SaveAccessToken implementa drive.AccountStore.
func (r *Repo) SaveAccessToken(ctx context.Context, id uuid.UUID, accessTokenEnc string, expiresAt time.Time) error {
	_, err := r.db.Exec(ctx, `
		UPDATE drive_accounts
		SET access_token_enc = $2, access_expires_at = $3, status = 'active', last_error = '', updated_at = now()
		WHERE id = $1`, id, accessTokenEnc, expiresAt)
	return err
}

// MarkAccountError implementa drive.AccountStore.
func (r *Repo) MarkAccountError(ctx context.Context, id uuid.UUID, msg string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE drive_accounts SET status = 'error', last_error = $2, updated_at = now() WHERE id = $1`,
		id, msg)
	return err
}

func (r *Repo) SetAccountRootFolder(ctx context.Context, id uuid.UUID, folderID string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE drive_accounts SET root_folder_id = $2, updated_at = now() WHERE id = $1`, id, folderID)
	return err
}

func (r *Repo) SetAccountQuota(ctx context.Context, id uuid.UUID, total, used int64) error {
	_, err := r.db.Exec(ctx, `
		UPDATE drive_accounts
		SET quota_total_bytes = $2, quota_used_bytes = $3, quota_checked_at = now(), updated_at = now()
		WHERE id = $1`, id, total, used)
	return err
}

func (r *Repo) SetPrimaryAccount(ctx context.Context, id uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op tras un commit correcto

	if _, err := tx.Exec(ctx, `UPDATE drive_accounts SET is_primary = FALSE`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE drive_accounts SET is_primary = TRUE WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repo) DeleteDriveAccount(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM drive_accounts WHERE id = $1`, id)
	return err
}
