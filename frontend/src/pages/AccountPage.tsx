import { FormEvent, ReactNode, useState } from 'react'
import { api, ApiError, formatDate, humanBytes, SessionInfo } from '../api'

const ROLES: Record<string, { label: string; desc: string }> = {
  admin: { label: 'Administrador', desc: 'Control total, incluida la conexión con Google Drive' },
  manager: { label: 'Gestor', desc: 'Administra carpetas, usuarios y permisos' },
  member: { label: 'Miembro', desc: 'Sube y descarga donde tenga permiso' },
  guest: { label: 'Invitado', desc: 'Solo consulta lo que se le comparta' },
}

export default function AccountPage({
  session, onChange,
}: {
  session: SessionInfo
  onChange: () => void
}) {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [repeat, setRepeat] = useState('')
  const [msg, setMsg] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const user = session.user
  const rol = ROLES[user.role] ?? { label: user.role, desc: '' }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setMsg('')

    if (next !== repeat) {
      setError('La contraseña nueva y su repetición no coinciden')
      return
    }
    setBusy(true)
    try {
      await api.changePassword(current, next)
      setMsg('Contraseña actualizada. Las demás sesiones se cerraron.')
      setCurrent('')
      setNext('')
      setRepeat('')
      onChange()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudo cambiar la contraseña')
    } finally {
      setBusy(false)
    }
  }

  const fuerza = evaluar(next)

  return (
    <div>
      <div className="page-head">
        <h1>Mi cuenta</h1>
        <div className="lead">Tus datos, tus límites y tu contraseña.</div>
      </div>

      <div className="grid-2">
        <div className="card">
          <div className="card-head">
            <h2>Datos de la cuenta</h2>
          </div>
          <div className="card-body">
            <Dato etiqueta="Nombre" valor={user.name || '—'} />
            <Dato etiqueta="Correo" valor={user.email} />
            <Dato
              etiqueta="Rol"
              valor={
                <>
                  <span className="badge accent">{rol.label}</span>
                  <div className="dim" style={{ marginTop: '.25rem' }}>
                    {rol.desc}
                  </div>
                </>
              }
            />
            <Dato etiqueta="Miembro desde" valor={formatDate(user.created_at)} />
            <Dato etiqueta="Último acceso" valor={formatDate(user.last_login_at)} />
            <Dato
              etiqueta="Espacio asignado"
              valor={`${humanBytes(user.used_bytes)} usados de ${humanBytes(session.quota_bytes)}`}
            />
            <Dato
              etiqueta="Descarga este mes"
              valor={`${humanBytes(session.bandwidth_used_month)} de ${humanBytes(session.bandwidth_bytes)}`}
            />
          </div>
        </div>

        <form className="card" onSubmit={submit}>
          <div className="card-head">
            <h2>Cambiar contraseña</h2>
          </div>
          <div className="card-body">
            {error && (
              <div className="alert error">
                <span className="ico">⚠️</span>
                <span>{error}</span>
              </div>
            )}
            {msg && (
              <div className="alert ok">
                <span className="ico">✓</span>
                <span>{msg}</span>
              </div>
            )}
            {user.must_change_password && !msg && (
              <div className="alert warn">
                <span className="ico">🔑</span>
                <span>Estás usando una contraseña temporal. Cámbiala por una tuya.</span>
              </div>
            )}

            <div className="field">
              <label htmlFor="cp">Contraseña actual</label>
              <input
                id="cp"
                type="password"
                autoComplete="current-password"
                value={current}
                onChange={(e) => setCurrent(e.target.value)}
                required
              />
            </div>

            <div className="field">
              <label htmlFor="np">Contraseña nueva</label>
              <input
                id="np"
                type="password"
                autoComplete="new-password"
                value={next}
                onChange={(e) => setNext(e.target.value)}
                required
              />
              {next && (
                <>
                  <div className="progress" style={{ marginTop: '.4rem' }}>
                    <div
                      className={fuerza.nivel >= 3 ? 'ok' : fuerza.nivel === 1 ? 'err' : ''}
                      style={{ width: `${fuerza.nivel * 25}%` }}
                    />
                  </div>
                  <div className="help">{fuerza.texto}</div>
                </>
              )}
              {!next && <div className="help">Mínimo 10 caracteres. Una frase larga es más segura que símbolos raros.</div>}
            </div>

            <div className="field">
              <label htmlFor="rp">Repite la contraseña nueva</label>
              <input
                id="rp"
                type="password"
                autoComplete="new-password"
                value={repeat}
                onChange={(e) => setRepeat(e.target.value)}
                required
              />
            </div>

            <button className="primary block" type="submit" disabled={busy || next.length < 10 || !current}>
              {busy ? 'Guardando…' : 'Cambiar contraseña'}
            </button>
            <p className="dim" style={{ marginTop: '.8rem', marginBottom: 0 }}>
              Al cambiarla se cierran todas tus demás sesiones abiertas.
            </p>
          </div>
        </form>
      </div>
    </div>
  )
}

function Dato({ etiqueta, valor }: { etiqueta: string; valor: ReactNode }) {
  return (
    <div style={{ display: 'flex', gap: '1rem', padding: '.45rem 0', borderBottom: '1px solid var(--border)' }}>
      <div style={{ flex: '0 0 40%', color: 'var(--text-2)', fontSize: '.85rem' }}>{etiqueta}</div>
      <div style={{ flex: 1, fontSize: '.9rem' }}>{valor}</div>
    </div>
  )
}

// Medidor honesto: premia la longitud, que es lo que de verdad cuesta romper,
// en lugar de exigir el símbolo de turno.
function evaluar(pw: string): { nivel: number; texto: string } {
  if (!pw) return { nivel: 0, texto: '' }
  if (pw.length < 10) return { nivel: 1, texto: 'Demasiado corta: mínimo 10 caracteres' }

  let nivel = 2
  if (pw.length >= 14) nivel = 3
  if (pw.length >= 20 || (pw.length >= 16 && /[^a-zA-Z0-9]/.test(pw))) nivel = 4

  const textos = ['', 'Débil', 'Aceptable', 'Buena', 'Muy buena']
  return { nivel, texto: textos[nivel] }
}
