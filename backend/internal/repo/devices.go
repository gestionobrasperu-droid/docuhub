package repo

import (
	"context"
	"time"

	"github.com/docuhub/docuhub/internal/models"
	"github.com/google/uuid"
)

// CreateDeviceToken registra la credencial de un equipo concreto.
func (r *Repo) CreateDeviceToken(ctx context.Context, userID uuid.UUID, tokenHash, deviceName string, expiresAt *time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx, `
		INSERT INTO device_tokens (user_id, token_hash, device_name, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id`, userID, tokenHash, deviceName, expiresAt).Scan(&id)
	return id, norm(err)
}

// UserByDeviceToken resuelve la credencial de un equipo a su usuario. Solo
// devuelve algo si el token sigue vigente y la cuenta está activa.
func (r *Repo) UserByDeviceToken(ctx context.Context, tokenHash string) (*models.User, uuid.UUID, error) {
	var u models.User
	var tokenID uuid.UUID
	err := r.db.QueryRow(ctx, `
		SELECT d.id, u.id, u.email, u.name, u.password_hash, u.role, u.status,
		       u.quota_bytes, u.bandwidth_bytes, u.used_bytes, u.must_change_pw,
		       u.created_at, u.last_login_at
		FROM device_tokens d
		JOIN users u ON u.id = d.user_id
		WHERE d.token_hash = $1
		  AND d.revoked_at IS NULL
		  AND (d.expires_at IS NULL OR d.expires_at > now())
		  AND u.status = 'active'`, tokenHash,
	).Scan(&tokenID, &u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.Status,
		&u.QuotaBytes, &u.BandwidthBytes, &u.UsedBytes, &u.MustChangePw,
		&u.CreatedAt, &u.LastLoginAt)
	if err != nil {
		return nil, uuid.Nil, norm(err)
	}
	return &u, tokenID, nil
}

// TouchDeviceToken deja constancia de que el equipo sigue en uso. Se llama en
// segundo plano: no debe retrasar la respuesta de una descarga.
func (r *Repo) TouchDeviceToken(ctx context.Context, id uuid.UUID, ip string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE device_tokens SET last_used_at = now(), last_ip = $2 WHERE id = $1`, id, ip)
	return err
}

type DeviceToken struct {
	ID         uuid.UUID  `json:"id"`
	DeviceName string     `json:"device_name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	LastIP     string     `json:"last_ip,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

func (r *Repo) ListDeviceTokens(ctx context.Context, userID uuid.UUID) ([]*DeviceToken, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, device_name, created_at, last_used_at, last_ip, expires_at
		FROM device_tokens
		WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*DeviceToken{}
	for rows.Next() {
		var d DeviceToken
		if err := rows.Scan(&d.ID, &d.DeviceName, &d.CreatedAt, &d.LastUsedAt, &d.LastIP, &d.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, &d)
	}
	return out, rows.Err()
}

// RevokeDeviceToken desconecta un equipo. El usuario solo puede revocar los
// suyos: el filtro por user_id es parte de la consulta, no una comprobación
// que se pueda olvidar en la capa de arriba.
func (r *Repo) RevokeDeviceToken(ctx context.Context, userID, id uuid.UUID) error {
	_, err := r.db.Exec(ctx,
		`UPDATE device_tokens SET revoked_at = now()
		 WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, id, userID)
	return err
}
