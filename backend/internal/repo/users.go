package repo

import (
	"context"
	"strings"
	"time"

	"github.com/docuhub/docuhub/internal/models"
	"github.com/google/uuid"
)

const userCols = `id, email, name, password_hash, role, status, quota_bytes,
	bandwidth_bytes, used_bytes, must_change_pw, created_at, last_login_at`

func scanUser(row interface{ Scan(...any) error }) (*models.User, error) {
	var u models.User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.Status,
		&u.QuotaBytes, &u.BandwidthBytes, &u.UsedBytes, &u.MustChangePw,
		&u.CreatedAt, &u.LastLoginAt)
	if err != nil {
		return nil, norm(err)
	}
	return &u, nil
}

func (r *Repo) CreateUser(ctx context.Context, email, name, passwordHash, role string, quota, bandwidth int64, mustChange bool) (*models.User, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash, role, quota_bytes, bandwidth_bytes, must_change_pw)
		VALUES (lower($1), $2, $3, $4, $5, $6, $7)
		RETURNING `+userCols,
		strings.TrimSpace(email), name, passwordHash, role, quota, bandwidth, mustChange)
	return scanUser(row)
}

func (r *Repo) UserByEmail(ctx context.Context, email string) (*models.User, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+userCols+` FROM users WHERE email = lower($1)`, strings.TrimSpace(email))
	return scanUser(row)
}

func (r *Repo) UserByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	row := r.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id)
	return scanUser(row)
}

func (r *Repo) ListUsers(ctx context.Context) ([]*models.User, error) {
	rows, err := r.db.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*models.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *Repo) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// UpdateUser modifica los campos administrables. Los punteros en nil se dejan
// como están, de modo que el PATCH del panel puede enviar solo lo que cambió.
func (r *Repo) UpdateUser(ctx context.Context, id uuid.UUID, name, role, status *string, quota, bandwidth *int64) (*models.User, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE users SET
			name            = COALESCE($2::text, name),
			role            = COALESCE($3::text, role),
			status          = COALESCE($4::text, status),
			quota_bytes     = COALESCE($5::bigint, quota_bytes),
			bandwidth_bytes = COALESCE($6::bigint, bandwidth_bytes),
			disabled_at     = CASE WHEN COALESCE($4::text, status) = 'suspended'
			                       THEN COALESCE(disabled_at, now()) ELSE NULL END
		WHERE id = $1
		RETURNING `+userCols, id, name, role, status, quota, bandwidth)
	return scanUser(row)
}

func (r *Repo) SetPassword(ctx context.Context, id uuid.UUID, hash string, mustChange bool) error {
	_, err := r.db.Exec(ctx,
		`UPDATE users SET password_hash = $2, must_change_pw = $3 WHERE id = $1`,
		id, hash, mustChange)
	return err
}

func (r *Repo) TouchLogin(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, id)
	return err
}

func (r *Repo) DeleteUser(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	return err
}

// AddUsedBytes ajusta el espacio ocupado del usuario. Admite valores negativos
// al borrar. El GREATEST evita que un descuadre deje la cuenta en negativo.
func (r *Repo) AddUsedBytes(ctx context.Context, id uuid.UUID, delta int64) error {
	_, err := r.db.Exec(ctx,
		`UPDATE users SET used_bytes = GREATEST(0, used_bytes + $2) WHERE id = $1`, id, delta)
	return err
}

// BandwidthUsedSince suma los bytes descargados por un usuario desde una fecha.
func (r *Repo) BandwidthUsedSince(ctx context.Context, id uuid.UUID, since time.Time) (int64, error) {
	var total *int64
	err := r.db.QueryRow(ctx, `
		SELECT sum(bytes) FROM bandwidth_log
		WHERE user_id = $1 AND direction = 'download' AND created_at >= $2`,
		id, since).Scan(&total)
	if err != nil {
		return 0, err
	}
	if total == nil {
		return 0, nil
	}
	return *total, nil
}

// ---------------------------------------------------------------- grupos --

func (r *Repo) GroupIDsForUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `SELECT group_id FROM group_members WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *Repo) CreateGroup(ctx context.Context, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx,
		`INSERT INTO groups (name) VALUES ($1) RETURNING id`, name).Scan(&id)
	return id, err
}

func (r *Repo) AddGroupMember(ctx context.Context, groupID, userID uuid.UUID) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)
		 ON CONFLICT DO NOTHING`, groupID, userID)
	return err
}
