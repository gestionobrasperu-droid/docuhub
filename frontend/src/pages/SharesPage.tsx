import { useCallback, useEffect, useState } from 'react'
import { api, ApiError, formatDate } from '../api'

// Panel de enlaces públicos emitidos. Aquí se ve de un vistazo qué salió de
// la empresa, cuántas veces se descargó y qué sigue vigente.
export default function SharesPage() {
  const [shares, setShares] = useState<any[]>([])
  const [error, setError] = useState('')
  const [soloActivos, setSoloActivos] = useState(false)

  const load = useCallback(async () => {
    try {
      setShares((await api.shares()).shares)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudieron cargar los enlaces')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  function estado(s: any): { texto: string; clase: string } {
    if (s.revoked_at) return { texto: 'revocado', clase: 'danger' }
    if (s.expires_at && new Date(s.expires_at) < new Date()) return { texto: 'caducado', clase: 'warn' }
    if (s.max_downloads > 0 && s.download_count >= s.max_downloads) return { texto: 'agotado', clase: 'warn' }
    return { texto: 'activo', clase: 'ok' }
  }

  const visibles = soloActivos ? shares.filter((s) => estado(s).texto === 'activo') : shares
  const activos = shares.filter((s) => estado(s).texto === 'activo').length

  return (
    <div>
      <div className="page-head">
        <h1>Enlaces compartidos</h1>
        <div className="lead">
          {activos} activos de {shares.length} emitidos. Por seguridad, el enlace completo solo se
          muestra al crearlo.
        </div>
      </div>

      {error && (
        <div className="alert error">
          <span className="ico">⚠️</span>
          <span>{error}</span>
        </div>
      )}

      <div className="card">
        <div className="card-head">
          <label className="check" style={{ marginBottom: 0 }}>
            <input type="checkbox" checked={soloActivos} onChange={(e) => setSoloActivos(e.target.checked)} />
            <span>Mostrar solo los activos</span>
          </label>
          <div className="spacer" />
          <span className="dim">{visibles.length} enlaces</span>
        </div>

        {visibles.length === 0 ? (
          <div className="empty">
            {shares.length === 0
              ? 'Todavía no has creado ningún enlace. Hazlo desde el botón «Compartir» de cualquier archivo o carpeta.'
              : 'Ningún enlace activo.'}
          </div>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Nota</th>
                  <th>Tipo</th>
                  <th>Estado</th>
                  <th>Descargas</th>
                  <th>Caduca</th>
                  <th>Último uso</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {visibles.map((s) => {
                  const e = estado(s)
                  return (
                    <tr key={s.id}>
                      <td>
                        {s.note || <span className="dim">sin nota</span>}
                        {s.has_password && (
                          <span className="badge" style={{ marginLeft: '.35rem' }} title="Protegido con contraseña">
                            🔒
                          </span>
                        )}
                      </td>
                      <td className="dim">{s.file_id ? 'archivo' : 'carpeta'}</td>
                      <td>
                        <span className={`badge ${e.clase}`}>{e.texto}</span>
                      </td>
                      <td className="num">
                        {s.download_count}
                        {s.max_downloads > 0 && <span className="dim"> / {s.max_downloads}</span>}
                      </td>
                      <td className="dim">{s.expires_at ? formatDate(s.expires_at) : 'nunca'}</td>
                      <td className="dim">{formatDate(s.last_used_at)}</td>
                      <td style={{ textAlign: 'right' }}>
                        {!s.revoked_at && (
                          <button
                            className="danger sm"
                            onClick={async () => {
                              if (!confirm('¿Revocar este enlace? Dejará de funcionar de inmediato.')) return
                              await api.revokeShare(s.id)
                              await load()
                            }}
                          >
                            Revocar
                          </button>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
