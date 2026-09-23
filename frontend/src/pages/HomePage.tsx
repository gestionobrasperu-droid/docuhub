import { useEffect, useState } from 'react'
import { api, ApiError, FileItem, formatDate, humanBytes, plural, SessionInfo } from '../api'
import { iconFor } from '../components/fileIcon'

// Pantalla de inicio del usuario: lo suyo, no lo de toda la plataforma.
// Quien entra quiere ver en qué estaba trabajando y cuánto espacio le queda;
// las estadísticas globales son otra pantalla y otro rol.
export default function HomePage({
  session, navigate,
}: {
  session: SessionInfo
  navigate: (to: string) => void
}) {
  const [data, setData] = useState<any>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    api
      .myOverview()
      .then(setData)
      .catch((err) => setError(err instanceof ApiError ? err.message : 'No se pudo cargar tu resumen'))
  }, [])

  const user = session.user
  const nombre = user.name || user.email.split('@')[0]
  const hora = new Date().getHours()
  const saludo = hora < 12 ? 'Buenos días' : hora < 19 ? 'Buenas tardes' : 'Buenas noches'

  const s = data?.summary
  const u = data?.usage
  const recientes: FileItem[] = data?.recent_files ?? []

  const pctAlmacen = u?.storage_limit ? Math.min(100, Math.round((u.storage_used / u.storage_limit) * 100)) : 0
  const pctTrafico = u?.bandwidth_limit
    ? Math.min(100, Math.round((u.bandwidth_used / u.bandwidth_limit) * 100))
    : 0

  return (
    <div>
      <div className="page-head">
        <h1>
          {saludo}, {nombre}
        </h1>
        <div className="lead">Este es el estado de tu espacio en DocuHub.</div>
      </div>

      {error && (
        <div className="alert error">
          <span className="ico">⚠️</span>
          <span>{error}</span>
        </div>
      )}

      <div className="grid">
        <Metric label="Mis archivos" value={s ? String(s.file_count) : '—'} hint={s ? humanBytes(s.stored_bytes) : ''} />
        <Metric label="Descargas" value={s ? String(s.download_count) : '—'} hint="de mis archivos" />
        <Metric label="Enlaces activos" value={s ? String(s.active_shares) : '—'} hint="compartidos hacia fuera" />
        <Metric
          label="Espacio usado"
          value={`${pctAlmacen}%`}
          hint={u ? `${humanBytes(u.storage_used)} de ${humanBytes(u.storage_limit)}` : ''}
        />
      </div>

      <div className="grid-2" style={{ marginTop: '1rem' }}>
        <div className="card">
          <div className="card-head">
            <h2>Archivos recientes</h2>
            <div className="spacer" />
            <button className="ghost sm" onClick={() => navigate('/archivos')}>
              Ver todos →
            </button>
          </div>

          {recientes.length === 0 ? (
            <div className="empty">
              Todavía no has subido nada.
              <div style={{ marginTop: '.8rem' }}>
                <button className="primary" onClick={() => navigate('/archivos')}>
                  Subir mi primer archivo
                </button>
              </div>
            </div>
          ) : (
            <div>
              {recientes.map((f) => (
                <div className="item" key={f.id}>
                  <span className="icon" aria-hidden>
                    {iconFor(f.mime_type)}
                  </span>
                  <div className="name">
                    <b title={f.name}>{f.name}</b>
                    <span>
                      {humanBytes(f.size_bytes)} · {formatDate(f.updated_at)}
                      {f.download_count > 0 && ` · ${plural(f.download_count, "descarga", "descargas")}`}
                    </span>
                  </div>
                  <div className="actions">
                    <a className="btn sm" href={api.downloadUrl(f.id)}>
                      Descargar
                    </a>
                    <button className="ghost sm" onClick={() => navigate(`/f/${f.folder_id}`)}>
                      Abrir carpeta
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>

        <div className="stack">
          <div className="card">
            <div className="card-head">
              <h2>Mis límites</h2>
            </div>
            <div className="card-body">
              <Gauge
                label="Almacenamiento"
                pct={pctAlmacen}
                detail={u ? `${humanBytes(u.storage_used)} de ${humanBytes(u.storage_limit)}` : '—'}
              />
              <div style={{ height: '1rem' }} />
              <Gauge
                label="Descarga este mes"
                pct={pctTrafico}
                detail={u ? `${humanBytes(u.bandwidth_used)} de ${humanBytes(u.bandwidth_limit)}` : '—'}
              />
              <p className="dim" style={{ marginTop: '1rem', marginBottom: 0 }}>
                El límite de descarga se reinicia el día 1 de cada mes. Si necesitas más, pídeselo a
                un administrador.
              </p>
            </div>
          </div>

          <div className="card">
            <div className="card-head">
              <h2>Accesos rápidos</h2>
            </div>
            <div className="card-body stack">
              <button className="block" onClick={() => navigate('/archivos')}>
                📂 Subir archivos
              </button>
              <button className="block" onClick={() => navigate('/enlaces')}>
                🔗 Ver mis enlaces compartidos
              </button>
              <button className="block" onClick={() => navigate('/cuenta')}>
                🔑 Cambiar mi contraseña
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

function Metric({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="card metric">
      <div className="label">{label}</div>
      <div className="value">{value}</div>
      {hint && <div className="hint">{hint}</div>}
    </div>
  )
}

function Gauge({ label, pct, detail }: { label: string; pct: number; detail: string }) {
  return (
    <div>
      <div className="row between" style={{ marginBottom: '.35rem' }}>
        <span style={{ fontSize: '.88rem', fontWeight: 550 }}>{label}</span>
        <span className="dim">{pct}%</span>
      </div>
      <div className="progress">
        <div
          className={pct >= 95 ? 'err' : ''}
          style={{
            width: `${Math.max(pct, 1)}%`,
            background: pct >= 80 && pct < 95 ? 'var(--warn)' : undefined,
          }}
        />
      </div>
      <div className="dim" style={{ marginTop: '.3rem' }}>
        {detail}
      </div>
    </div>
  )
}
