import { useEffect, useState } from 'react'
import { api, ApiError, formatDate, humanBytes } from '../api'
import { iconFor } from '../components/fileIcon'

// Página que ve alguien de fuera de la empresa al abrir un enlace público.
// No requiere cuenta: el token del enlace es toda la credencial. Es la única
// cara de DocuHub que ven los clientes, así que tiene que verse seria.
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
      <Marco>
        <div className="card-body">
          <div className="alert error">
            <span className="ico">⚠️</span>
            <span>{error}</span>
          </div>
          <p className="muted" style={{ marginBottom: 0 }}>
            El enlace puede haber caducado, haber alcanzado su límite de descargas o haber sido
            revocado por quien lo compartió. Pídele uno nuevo.
          </p>
        </div>
      </Marco>
    )
  }

  if (!info) {
    return (
      <div className="auth-wrap">
        <div className="muted">Cargando…</div>
      </div>
    )
  }

  const locked = info.has_password && !grant

  return (
    <Marco ancho={info.type === 'folder'}>
      <div className="card-body">
        {info.note && (
          <div className="alert info">
            <span className="ico">💬</span>
            <span>{info.note}</span>
          </div>
        )}
        {error && (
          <div className="alert error">
            <span className="ico">⚠️</span>
            <span>{error}</span>
          </div>
        )}

        {locked ? (
          <>
            <div className="alert warn">
              <span className="ico">🔒</span>
              <span>Este enlace está protegido con contraseña.</span>
            </div>
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
            <button className="primary block" onClick={unlock} disabled={busy || !password}>
              {busy ? 'Comprobando…' : 'Abrir'}
            </button>
          </>
        ) : info.type === 'file' ? (
          <div style={{ textAlign: 'center' }}>
            <div style={{ fontSize: '2.6rem', marginBottom: '.3rem' }} aria-hidden>
              {iconFor(info.mime_type)}
            </div>
            <h2 style={{ marginBottom: '.2rem' }}>{info.name}</h2>
            <p className="muted">{humanBytes(info.size_bytes)}</p>
            <a className="btn btn-primary" href={api.publicDownloadUrl(token, undefined, grant)}>
              ⬇ Descargar archivo
            </a>
          </div>
        ) : (
          <>
            <h2 style={{ marginBottom: '.8rem' }}>📁 {info.name}</h2>
            <div style={{ border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)' }}>
              {(info.files ?? []).map((f: any) => (
                <div className="item" key={f.id}>
                  <span className="icon" aria-hidden>
                    {iconFor(f.mime_type)}
                  </span>
                  <div className="name">
                    <b>{f.name}</b>
                    <span>{humanBytes(f.size_bytes)}</span>
                  </div>
                  <a className="btn sm" href={api.publicDownloadUrl(token, f.id, grant)}>
                    ⬇ Descargar
                  </a>
                </div>
              ))}
              {(info.files ?? []).length === 0 && <div className="empty">La carpeta está vacía.</div>}
            </div>
          </>
        )}
      </div>

      <div className="modal-foot" style={{ justifyContent: 'flex-start' }}>
        <span className="dim">
          {info.expires_at && <>Caduca el {formatDate(info.expires_at)}. </>}
          {info.max_downloads > 0 && (
            <>
              Descargas usadas: {info.download_count} de {info.max_downloads}.{' '}
            </>
          )}
          Cada descarga queda registrada.
        </span>
      </div>
    </Marco>
  )
}

function Marco({ children, ancho }: { children: React.ReactNode; ancho?: boolean }) {
  return (
    <div className="auth-wrap">
      <div style={{ width: ancho ? 'min(680px, 100%)' : 'min(460px, 100%)' }}>
        <div className="auth-brand">
          <div className="mark" aria-hidden>
            📁
          </div>
          <h1>DocuHub</h1>
          <p>Constructora Pesam te ha compartido esto</p>
        </div>
        <div className="card">{children}</div>
        <p className="dim" style={{ textAlign: 'center', marginTop: '1rem' }}>
          <a href="/legal/privacidad.html">Política de privacidad</a> ·{' '}
          <a href="/legal/terminos.html">Condiciones del servicio</a>
        </p>
      </div>
    </div>
  )
}
