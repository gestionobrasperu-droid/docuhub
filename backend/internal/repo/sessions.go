package repo

import (
	"context"
	"time"

	"github.com/docuhub/docuhub/internal/models"
	"github.com/google/uuid"
)

func (r *Repo) CreateSession(ctx context.Context, userID uuid.UUID, tokenHash, ip, userAgent string, expiresAt time.Time) (*models.Session, error) {
	var s models.Session
	err := r.db.QueryRow(ctx, `
		INSERT INTO sessions (user_id, token_hash, ip, user_agent, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, user_id, ip, user_agent, created_at, expires_at`,
		userID, tokenHash, ip, userAgent, expiresAt,
	).Scan(&s.ID, &s.UserID, &s.IP, &s.UserAgent, &s.CreatedAt, &s.ExpiresAt)
	if err != nil {
		return nil, norm(err)
	}
	return &s, nil
}

// UserBySessionToken resuelve la cookie a un usuario activo en una sola
// consulta: es la operación más frecuente de toda la plataforma.
func (r *Repo) UserBySessionToken(ctx context.Context, tokenHash string) (*models.User, uuid.UUID, error) {
	var u models.User
	var sid uuid.UUID
	err := r.db.QueryRow(ctx, `
		SELECT s.id, u.id, u.email, u.name, u.password_hash, u.role, u.status,
		       u.quota_bytes, u.bandwidth_bytes, u.used_bytes, u.must_change_pw,
		       u.created_at, u.last_login_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1
		  AND s.revoked_at IS NULL
		  AND s.expires_at > now()
		  AND u.status = 'active'`, tokenHash,
	).Scan(&sid, &u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.Status,
		&u.QuotaBytes, &u.BandwidthBytes, &u.UsedBytes, &u.MustChangePw,
		&u.CreatedAt, &u.LastLoginAt)
	if err != nil {
		return nil, uuid.Nil, norm(err)
	}
	return &u, sid, nil
}

func (r *Repo) TouchSession(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE sessions SET last_seen_at = now() WHERE id = $1`, id)
	return err
}

func (r *Repo) RevokeSession(ctx context.Context, tokenHash string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE sessions SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`,
		tokenHash)
	return err
}

// RevokeUserSessions cierra todas las sesiones de un usuario: se llama al
// suspenderlo o al cambiarle la contraseña.
func (r *Repo) RevokeUserSessions(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.Exec(ctx,
		`UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`,
		userID)
	return err
}

// PurgeExpiredSessions lo ejecuta la tarea de mantenimiento cada hora.
func (r *Repo) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM sessions WHERE expires_at < now() - interval '7 days'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
