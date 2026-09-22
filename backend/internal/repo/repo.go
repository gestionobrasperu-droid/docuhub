// Package repo concentra todo el SQL. Ninguna otra capa escribe consultas,
// así el día que haya que optimizar un índice se sabe dónde mirar.
package repo

import (
	"errors"

	"github.com/docuhub/docuhub/internal/store"
	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("registro no encontrado")

type Repo struct {
	db *store.DB
}

func New(db *store.DB) *Repo { return &Repo{db: db} }

// DB expone el pool para los pocos casos que necesitan una transacción propia.
func (r *Repo) DB() *store.DB { return r.db }

// norm convierte el "sin filas" de pgx en el error del dominio.
func norm(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
