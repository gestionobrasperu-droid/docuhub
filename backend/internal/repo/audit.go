package repo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/docuhub/docuhub/internal/models"
	"github.com/google/uuid"
)

// AuditInput es lo que registra cada acción relevante de la plataforma.
type AuditInput struct {
	ActorID      *uuid.UUID
	ActorEmail   string
	Action       string
	ResourceType string
	ResourceID   string
	ResourceName string
	IP           string
	UserAgent    string
	Success      bool
	Metadata     map[string]any
}

func (r *Repo) InsertAudit(ctx context.Context, in AuditInput) error {
	meta := []byte("{}")
	if in.Metadata != nil {
		if b, err := json.Marshal(in.Metadata); err == nil {
			meta = b
		}
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO audit_log
			(actor_id, actor_email, action, resource_type, resource_id, resource_name,
			 ip, user_agent, success, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		in.ActorID, in.ActorEmail, in.Action, in.ResourceType, in.ResourceID,
		in.ResourceName, in.IP, in.UserAgent, in.Success, meta)
	return err
}

type AuditFilter struct {
	ActorID *uuid.UUID
	Action  string
	Since   *time.Time
	Limit   int
	Offset  int
}

func (r *Repo) ListAudit(ctx context.Context, f AuditFilter) ([]*models.AuditEntry, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, actor_email, action, resource_type, resource_id, resource_name,
		       ip, user_agent, success, metadata, created_at
		FROM audit_log
		WHERE ($1::uuid IS NULL OR actor_id = $1)
		  AND ($2::text IS NULL OR action = $2)
		  AND ($3::timestamptz IS NULL OR created_at >= $3)
		ORDER BY created_at DESC
		LIMIT $4 OFFSET $5`,
		f.ActorID, nullString(f.Action), f.Since, f.Limit, f.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*models.AuditEntry{}
	for rows.Next() {
		var e models.AuditEntry
		var meta []byte
		if err := rows.Scan(&e.ID, &e.ActorEmail, &e.Action, &e.ResourceType, &e.ResourceID,
			&e.ResourceName, &e.IP, &e.UserAgent, &e.Success, &meta, &e.CreatedAt); err != nil {
			return nil, err
		}
		var m any
		if json.Unmarshal(meta, &m) == nil {
			e.Metadata = m
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// InsertBandwidth contabiliza bytes movidos. Alimenta las cuotas mensuales y
// los informes de consumo del panel.
func (r *Repo) InsertBandwidth(ctx context.Context, userID, fileID, shareID *uuid.UUID, direction string, bytes int64, ip string) error {
	if bytes <= 0 {
		return nil
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO bandwidth_log (user_id, file_id, share_id, direction, bytes, ip)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		userID, fileID, shareID, direction, bytes, ip)
	return err
}

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
