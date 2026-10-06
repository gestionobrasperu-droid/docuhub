// Package quota decide si una operación cabe dentro de los límites del
// usuario. Es la pieza que convierte "compartir archivos" en "administrar
// por completo los usos".
package quota

import (
	"context"
	"fmt"
	"time"

	"github.com/docuhub/docuhub/internal/models"
	"github.com/docuhub/docuhub/internal/repo"
)

type Checker struct {
	repo             *repo.Repo
	defaultStorage   int64
	defaultBandwidth int64
}

func New(r *repo.Repo, defaultStorage, defaultBandwidth int64) *Checker {
	return &Checker{repo: r, defaultStorage: defaultStorage, defaultBandwidth: defaultBandwidth}
}

type Usage struct {
	StorageLimit   int64 `json:"storage_limit"`
	StorageUsed    int64 `json:"storage_used"`
	BandwidthLimit int64 `json:"bandwidth_limit"`
	BandwidthUsed  int64 `json:"bandwidth_used"`
}

// LimitError distingue el rechazo por cuota de un error técnico, para que la
// interfaz pueda mostrar un mensaje útil en lugar de "error del servidor".
type LimitError struct {
	Kind   string // "almacenamiento" o "descarga"
	Limit  int64
	Used   int64
	Needed int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("Superarías tu límite de %s: usas %s de %s y esta operación necesita %s",
		e.Kind, Human(e.Used), Human(e.Limit), Human(e.Needed))
}

func (c *Checker) storageLimit(u *models.User) int64 {
	if u.QuotaBytes > 0 {
		return u.QuotaBytes
	}
	return c.defaultStorage
}

func (c *Checker) bandwidthLimit(u *models.User) int64 {
	if u.BandwidthBytes > 0 {
		return u.BandwidthBytes
	}
	return c.defaultBandwidth
}

// MonthStart es el inicio del mes en curso: la ventana de la cuota de tráfico.
func MonthStart() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// CheckStorage se llama antes de abrir una subida, no después: rechazar un
// archivo de 20 GB cuando ya se transfirió sería una crueldad.
func (c *Checker) CheckStorage(ctx context.Context, u *models.User, size int64) error {
	limit := c.storageLimit(u)
	if limit <= 0 { // 0 o negativo = sin límite
		return nil
	}
	if u.UsedBytes+size > limit {
		return &LimitError{Kind: "almacenamiento", Limit: limit, Used: u.UsedBytes, Needed: size}
	}
	return nil
}

// CheckBandwidth se evalúa antes de empezar a transmitir el archivo.
func (c *Checker) CheckBandwidth(ctx context.Context, u *models.User, size int64) error {
	limit := c.bandwidthLimit(u)
	if limit <= 0 {
		return nil
	}
	used, err := c.repo.BandwidthUsedSince(ctx, u.ID, MonthStart())
	if err != nil {
		return err
	}
	if used+size > limit {
		return &LimitError{Kind: "descarga mensual", Limit: limit, Used: used, Needed: size}
	}
	return nil
}

func (c *Checker) Snapshot(ctx context.Context, u *models.User) (*Usage, error) {
	used, err := c.repo.BandwidthUsedSince(ctx, u.ID, MonthStart())
	if err != nil {
		return nil, err
	}
	return &Usage{
		StorageLimit:   c.storageLimit(u),
		StorageUsed:    u.UsedBytes,
		BandwidthLimit: c.bandwidthLimit(u),
		BandwidthUsed:  used,
	}, nil
}

// Human formatea bytes para mensajes dirigidos a personas.
func Human(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTP"[exp])
}
