import { useCallback, useEffect, useState } from 'react'
import { api, ApiError, formatDate } from '../api'

// Panel de enlaces públicos emitidos. Aquí se ve de un vistazo qué salió de
// la empresa, cuántas veces se descargó y qué sigue vigente.
export default function SharesPage() {
  const [shares, setShares] = useState<any[]>([])
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    try {
      const res = await api.shares()
      setShares(res.shares)
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
    if (s.max_downloads > 0 && s.download_count >= s.max_downloads)
      return { texto: 'agotado', clase: 'warn' }
    return { texto: 'activo', clase: 'ok' }
  }

  return (
    <div className="card">
      <h1>Enlaces compartidos</h1>
      {error && <div className="alert error">{error}</div>}

      <p className="muted">
        Por seguridad, el enlace completo solo se muestra al crearlo. Si se perdió, revoca este y
        emite uno nuevo.
      </p>

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
            {shares.length === 0 && (
              <tr>
                <td colSpan={7} className="muted">
                  Todavía no has creado ningún enlace. Hazlo desde el botón «Compartir» de cualquier
                  archivo o carpeta.
                </td>
              </tr>
            )}
            {shares.map((s) => {
              const e = estado(s)
              return (
                <tr key={s.id}>
                  <td>
                    {s.note || <span className="muted">sin nota</span>}
                    {s.has_password && <span className="badge"> con contraseña</span>}
                  </td>
                  <td className="muted">{s.file_id ? 'archivo' : 'carpeta'}</td>
                  <td>
                    <span className={`badge ${e.clase}`}>{e.texto}</span>
                  </td>
                  <td>
                    {s.download_count}
                    {s.max_downloads > 0 && ` / ${s.max_downloads}`}
                  </td>
                  <td className="muted">{s.expires_at ? formatDate(s.expires_at) : 'nunca'}</td>
                  <td className="muted">{formatDate(s.last_used_at)}</td>
                  <td style={{ textAlign: 'right' }}>
                    {!s.revoked_at && (
                      <button
                        className="small danger"
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
    </div>
  )
}
