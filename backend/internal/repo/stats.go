package repo

import (
	"context"
	"time"
)

type Totals struct {
	Users         int   `json:"users"`
	ActiveUsers   int   `json:"active_users"`
	Files         int   `json:"files"`
	StoredBytes   int64 `json:"stored_bytes"`
	Downloads     int64 `json:"downloads"`
	Folders       int   `json:"folders"`
	ActiveShares  int   `json:"active_shares"`
	UploadsToday  int   `json:"uploads_today"`
	DownloadBytes int64 `json:"download_bytes_30d"`
	UploadBytes   int64 `json:"upload_bytes_30d"`
}

type DailyTraffic struct {
	Day       string `json:"day"`
	Uploads   int64  `json:"uploads"`
	Downloads int64  `json:"downloads"`
}

type UserUsage struct {
	Email         string `json:"email"`
	Name          string `json:"name"`
	StoredBytes   int64  `json:"stored_bytes"`
	QuotaBytes    int64  `json:"quota_bytes"`
	DownloadBytes int64  `json:"download_bytes_30d"`
	FileCount     int    `json:"file_count"`
}

type TopFile struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	Downloads int64  `json:"downloads"`
	Owner     string `json:"owner"`
}

// Totals resume el estado de la plataforma en una sola consulta por métrica.
// Son agregados baratos: con decenas de miles de filas siguen siendo instantáneos.
func (r *Repo) Totals(ctx context.Context) (*Totals, error) {
	var t Totals
	err := r.db.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM users),
			(SELECT count(*) FROM users WHERE status = 'active'),
			(SELECT count(*) FROM files WHERE deleted_at IS NULL AND status = 'ready'),
			(SELECT COALESCE(sum(size_bytes), 0) FROM files WHERE deleted_at IS NULL AND status = 'ready'),
			(SELECT COALESCE(sum(download_count), 0) FROM files WHERE deleted_at IS NULL),
			(SELECT count(*) FROM folders WHERE deleted_at IS NULL),
			(SELECT count(*) FROM share_links
			  WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())),
			(SELECT count(*) FROM files
			  WHERE created_at >= date_trunc('day', now()) AND status = 'ready'),
			(SELECT COALESCE(sum(bytes), 0) FROM bandwidth_log
			  WHERE direction = 'download' AND created_at >= now() - interval '30 days'),
			(SELECT COALESCE(sum(bytes), 0) FROM bandwidth_log
			  WHERE direction = 'upload' AND created_at >= now() - interval '30 days')`,
	).Scan(&t.Users, &t.ActiveUsers, &t.Files, &t.StoredBytes, &t.Downloads,
		&t.Folders, &t.ActiveShares, &t.UploadsToday, &t.DownloadBytes, &t.UploadBytes)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// TrafficByDay devuelve la serie de los últimos N días, rellenando con ceros
// los días sin movimiento para que el gráfico no tenga huecos.
func (r *Repo) TrafficByDay(ctx context.Context, days int) ([]DailyTraffic, error) {
	if days <= 0 || days > 365 {
		days = 30
	}
	rows, err := r.db.Query(ctx, `
		SELECT to_char(d.day, 'YYYY-MM-DD'),
		       COALESCE(sum(b.bytes) FILTER (WHERE b.direction = 'upload'), 0),
		       COALESCE(sum(b.bytes) FILTER (WHERE b.direction = 'download'), 0)
		FROM generate_series(
		        date_trunc('day', now()) - make_interval(days => $1 - 1),
		        date_trunc('day', now()),
		        interval '1 day') AS d(day)
		LEFT JOIN bandwidth_log b
		       ON b.created_at >= d.day AND b.created_at < d.day + interval '1 day'
		GROUP BY d.day
		ORDER BY d.day`, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []DailyTraffic{}
	for rows.Next() {
		var t DailyTraffic
		if err := rows.Scan(&t.Day, &t.Uploads, &t.Downloads); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repo) UsageByUser(ctx context.Context, since time.Time) ([]UserUsage, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.email, u.name,
		       COALESCE(f.bytes, 0), u.quota_bytes,
		       COALESCE(b.bytes, 0), COALESCE(f.cnt, 0)
		FROM users u
		LEFT JOIN (
			SELECT owner_id, sum(size_bytes) AS bytes, count(*) AS cnt
			FROM files WHERE deleted_at IS NULL AND status = 'ready'
			GROUP BY owner_id
		) f ON f.owner_id = u.id
		LEFT JOIN (
			SELECT user_id, sum(bytes) AS bytes
			FROM bandwidth_log
			WHERE direction = 'download' AND created_at >= $1
			GROUP BY user_id
		) b ON b.user_id = u.id
		ORDER BY COALESCE(f.bytes, 0) DESC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []UserUsage{}
	for rows.Next() {
		var u UserUsage
		if err := rows.Scan(&u.Email, &u.Name, &u.StoredBytes, &u.QuotaBytes,
			&u.DownloadBytes, &u.FileCount); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *Repo) TopFiles(ctx context.Context, limit int) ([]TopFile, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	rows, err := r.db.Query(ctx, `
		SELECT f.name, f.size_bytes, f.download_count, COALESCE(u.email, '—')
		FROM files f LEFT JOIN users u ON u.id = f.owner_id
		WHERE f.deleted_at IS NULL AND f.download_count > 0
		ORDER BY f.download_count DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []TopFile{}
	for rows.Next() {
		var t TopFile
		if err := rows.Scan(&t.Name, &t.SizeBytes, &t.Downloads, &t.Owner); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
