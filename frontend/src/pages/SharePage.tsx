import { useEffect, useState } from 'react'
import { api, ApiError, formatDate, humanBytes } from '../api'

// Página que ve alguien de fuera de la empresa al abrir un enlace público.
// No requiere cuenta: el token del enlace es toda la credencial.
export default function SharePage({ token }: { token: string }) {
  const [info, setInfo] = useState<any>(null)
  const [error, setError] = useState('')
  const [password, setPassword] = useState('')
  const [grant, setGrant] = useState<string | undefined>()
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api
      .publicInfo(token)
      .then(setInfo)
      .catch((err) => setError(err instanceof ApiError ? err.message : 'No se pudo abrir el enlace'))
  }, [token])

  async function unlock() {
    setBusy(true)
    setError('')
    try {
      const res = await api.publicUnlock(token, password)
      setGrant(res.grant)
      setInfo(await api.publicInfo(token))
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Contraseña incorrecta')
    } finally {
      setBusy(false)
    }
  }

  if (error && !info) {
    return (
      <div className="login-wrap">
        <div className="card login-card">
          <h1>Enlace no disponible</h1>
          <div className="alert error">{error}</div>
          <p className="muted">
            Puede haber caducado, alcanzado su límite de descargas o haber sido revocado por quien
            lo compartió.
          </p>
        </div>
      </div>
    )
  }

  if (!info) {
    return (
      <div className="login-wrap">
        <div className="muted">Cargando…</div>
      </div>
    )
  }

  const locked = info.has_password && !grant

  return (
    <div className="login-wrap">
      <div className="card" style={{ width: 'min(620px, 100%)' }}>
        <div className="row between">
          <h1 style={{ margin: 0 }}>
            <span aria-hidden>🗂️</span> DocuHub
          </h1>
          <span className="badge">compartido contigo</span>
        </div>

        {info.note && <p className="muted">{info.note}</p>}
        {error && <div className="alert error">{error}</div>}

        {locked ? (
          <>
            <div className="alert warn">Este enlace está protegido con contraseña.</div>
            <div className="field">
              <label htmlFor="pw">Contraseña</label>
              <input
                id="pw"
                type="password"
                value={password}
                autoFocus
                onChange={(e) => setPassword(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && unlock()}
              />
            </div>
            <button className="primary" onClick={unlock} disabled={busy || !password}>
              {busy ? 'Comprobando…' : 'Abrir'}
            </button>
          </>
        ) : info.type === 'file' ? (
          <>
            <h2>{info.name}</h2>
            <p className="muted">
              {humanBytes(info.size_bytes)} · {info.mime_type}
            </p>
            <a className="btn primary" href={api.publicDownloadUrl(token, undefined, grant)}>
              Descargar archivo
            </a>
          </>
        ) : (
          <>
            <h2>📁 {info.name}</h2>
            <div className="item-list">
              {(info.files ?? []).map((f: any) => (
                <div className="item" key={f.id}>
                  <span className="icon" aria-hidden>
                    📄
                  </span>
                  <div className="name">
                    <b>{f.name}</b>
                    <span className="muted">{humanBytes(f.size_bytes)}</span>
                  </div>
                  <a className="btn small" href={api.publicDownloadUrl(token, f.id, grant)}>
                    Descargar
                  </a>
                </div>
              ))}
              {(info.files ?? []).length === 0 && <p className="muted">La carpeta está vacía.</p>}
            </div>
          </>
        )}

        <hr style={{ border: 'none', borderTop: '1px solid var(--border)', margin: '1.2rem 0 .6rem' }} />
        <p className="muted" style={{ margin: 0 }}>
          {info.expires_at && <>Caduca el {formatDate(info.expires_at)}. </>}
          {info.max_downloads > 0 && (
            <>
              Descargas usadas: {info.download_count} de {info.max_downloads}.{' '}
            </>
          )}
          Cada descarga queda registrada.
        </p>
      </div>
    </div>
  )
}
