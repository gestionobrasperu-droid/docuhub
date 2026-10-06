import { useCallback, useEffect, useState } from 'react'
import { api, ApiError, devices, formatDate, humanBytes, plural, SessionInfo, User } from '../api'
import Modal from '../components/Modal'

export type AdminSection = 'panel' | 'usuarios' | 'drive' | 'bitacora'

// El área de administración está separada del espacio del usuario: distinta
// entrada en el menú, distinto encabezado y distintas pantallas. Aquí nadie
// gestiona "sus" archivos; se gobierna la plataforma entera.
export default function AdminPage({
  section, session, onSessionChange, navigate,
}: {
  section: AdminSection
  session: SessionInfo
  onSessionChange: () => void
  navigate: (to: string) => void
}) {
  if (section === 'usuarios') return <UsersSection me={session.user} />
  if (section === 'drive') return <DriveSection isAdmin={session.user.role === 'admin'} onChange={onSessionChange} />
  if (section === 'bitacora') return <AuditSection />
  return <OverviewSection navigate={navigate} />
}

/* ─────────────────────────────────────────────── panel de control ──── */

function OverviewSection({ navigate }: { navigate: (to: string) => void }) {
  const [data, setData] = useState<any>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    api
      .admin.stats()
      .then(setData)
      .catch((err) => setError(err instanceof ApiError ? err.message : 'No se pudieron cargar las estadísticas'))
  }, [])

  if (error) {
    return (
      <div className="alert error">
        <span className="ico">⚠️</span>
        <span>{error}</span>
      </div>
    )
  }
  if (!data) return <div className="muted">Calculando…</div>

  const t = data.totals
  const traffic: any[] = data.traffic ?? []
  const maxDia = Math.max(1, ...traffic.map((d: any) => d.uploads + d.downloads))
  const cuentas: any[] = data.drive_accounts ?? []
  const principal = cuentas.find((a) => a.is_primary) ?? cuentas[0]
  const pctDrive = principal?.quota_total_bytes
    ? Math.round((principal.quota_used_bytes / principal.quota_total_bytes) * 100)
    : 0

  return (
    <div>
      <div className="page-head">
        <h1>Panel de control</h1>
        <div className="lead">Estado de la plataforma y consumo de los últimos 30 días.</div>
      </div>

      <div className="grid">
        <Metric label="Archivos" value={String(t.files)} hint={`${humanBytes(t.stored_bytes)} en total`} />
        <Metric label="Usuarios activos" value={String(t.active_users)} hint={plural(t.users, "cuenta en total", "cuentas en total")} />
        <Metric label="Descargas" value={String(t.downloads)} hint={`${humanBytes(t.download_bytes_30d)} en 30 días`} />
        <Metric label="Enlaces vigentes" value={String(t.active_shares)} hint={plural(t.uploads_today, "subida hoy", "subidas hoy")} />
      </div>

      <div className="grid-2" style={{ marginTop: '1rem' }}>
        <div className="card">
          <div className="card-head">
            <h2>Tráfico diario</h2>
            <div className="spacer" />
            <span className="dim">últimos {traffic.length} días</span>
          </div>
          <div className="card-body">
            <div className="chart">
              {traffic.map((d: any) => (
                <div
                  key={d.day}
                  className="bar"
                  style={{ height: `${((d.uploads + d.downloads) / maxDia) * 100}%` }}
                  title={`${d.day}\n↑ ${humanBytes(d.uploads)} subidos\n↓ ${humanBytes(d.downloads)} descargados`}
                />
              ))}
            </div>
            <div className="chart-axis">
              <span>{traffic[0]?.day ?? ''}</span>
              <span>{traffic[traffic.length - 1]?.day ?? ''}</span>
            </div>
            <p className="dim" style={{ marginTop: '.6rem', marginBottom: 0 }}>
              Pasa el cursor por una barra para ver el detalle del día.
            </p>
          </div>
        </div>

        <div className="card">
          <div className="card-head">
            <h2>Almacenamiento en Google Drive</h2>
          </div>
          <div className="card-body">
            {!principal ? (
              <div className="empty">
                No hay ninguna cuenta conectada.
                <div style={{ marginTop: '.8rem' }}>
                  <button className="primary" onClick={() => navigate('/admin/drive')}>
                    Conectar cuenta
                  </button>
                </div>
              </div>
            ) : (
              <>
                <div className="row between" style={{ marginBottom: '.4rem' }}>
                  <b style={{ fontSize: '.9rem' }}>{principal.email}</b>
                  <span className={`badge ${principal.status === 'active' ? 'ok' : 'danger'}`}>
                    {principal.status === 'active' ? 'activa' : principal.status}
                  </span>
                </div>
                <div className="progress">
                  <div
                    className={pctDrive >= 90 ? 'err' : ''}
                    style={{
                      width: `${Math.max(pctDrive, 1)}%`,
                      background: pctDrive >= 75 && pctDrive < 90 ? 'var(--warn)' : undefined,
                    }}
                  />
                </div>
                <div className="dim" style={{ marginTop: '.35rem' }}>
                  {humanBytes(principal.quota_used_bytes)} de {humanBytes(principal.quota_total_bytes)} ({pctDrive}%)
                </div>
                <div className="dim" style={{ marginTop: '.2rem' }}>
                  Comprobado {formatDate(principal.quota_checked_at)}
                </div>
                <button className="subtle block" style={{ marginTop: '1rem' }} onClick={() => navigate('/admin/drive')}>
                  Gestionar cuentas de Drive
                </button>
              </>
            )}
          </div>
        </div>
      </div>

      <div className="grid-2" style={{ marginTop: '1rem' }}>
        <div className="card">
          <div className="card-head">
            <h2>Consumo por usuario</h2>
          </div>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Usuario</th>
                  <th>Archivos</th>
                  <th>Almacenado</th>
                  <th>Descargado (mes)</th>
                </tr>
              </thead>
              <tbody>
                {(data.usage_by_user ?? []).map((u: any) => (
                  <tr key={u.email}>
                    <td>
                      <b>{u.name || u.email.split('@')[0]}</b>
                      <div className="dim">{u.email}</div>
                    </td>
                    <td className="num">{u.file_count}</td>
                    <td className="num">{humanBytes(u.stored_bytes)}</td>
                    <td className="num">{humanBytes(u.download_bytes_30d)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>

        <div className="card">
          <div className="card-head">
            <h2>Archivos más descargados</h2>
          </div>
          {(data.top_files ?? []).length === 0 ? (
            <div className="empty">Todavía no hay descargas registradas.</div>
          ) : (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Archivo</th>
                    <th>Tamaño</th>
                    <th>Descargas</th>
                  </tr>
                </thead>
                <tbody>
                  {(data.top_files ?? []).map((f: any, i: number) => (
                    <tr key={i}>
                      <td>
                        <b className="truncate" style={{ display: 'block', maxWidth: 280 }}>
                          {f.name}
                        </b>
                        <div className="dim">{f.owner}</div>
                      </td>
                      <td className="num">{humanBytes(f.size_bytes)}</td>
                      <td className="num">{f.downloads}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
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

/* ────────────────────────────────────────────────────── usuarios ───── */

function UsersSection({ me }: { me: User }) {
  const [users, setUsers] = useState<User[]>([])
  const [defaults, setDefaults] = useState({ quota: 0, bandwidth: 0 })
  const [error, setError] = useState('')
  const [creating, setCreating] = useState(false)
  const [temp, setTemp] = useState<{ email: string; password: string } | null>(null)
  const [filtro, setFiltro] = useState('')

  const load = useCallback(async () => {
    try {
      const res = await api.admin.users()
      setUsers(res.users)
      setDefaults({ quota: res.default_quota, bandwidth: res.default_bandwidth })
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudieron cargar los usuarios')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function update(id: string, body: any) {
    try {
      await api.admin.updateUser(id, body)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudo actualizar')
    }
  }

  const visibles = users.filter(
    (u) =>
      !filtro ||
      u.email.toLowerCase().includes(filtro.toLowerCase()) ||
      (u.name ?? '').toLowerCase().includes(filtro.toLowerCase()),
  )

  return (
    <div>
      <div className="page-head row between">
        <div>
          <h1>Usuarios</h1>
          <div className="lead">
            {users.length} cuentas · cuota por defecto {humanBytes(defaults.quota)}
          </div>
        </div>
        <button className="primary" onClick={() => setCreating(true)}>
          + Nuevo usuario
        </button>
      </div>

      {error && (
        <div className="alert error" onClick={() => setError('')}>
          <span className="ico">⚠️</span>
          <span>{error}</span>
        </div>
      )}

      {temp && (
        <div className="alert ok">
          <span className="ico">🔑</span>
          <span>
            Contraseña temporal de <b>{temp.email}</b>: <span className="mono">{temp.password}</span> — entrégasela
            por un canal seguro; no se vuelve a mostrar.
            <button className="ghost sm" style={{ marginLeft: '.5rem' }} onClick={() => setTemp(null)}>
              Ocultar
            </button>
          </span>
        </div>
      )}

      <div className="card">
        <div className="card-head">
          <div className="search" style={{ flex: '0 1 280px' }}>
            <span className="icon" aria-hidden>
              🔍
            </span>
            <input placeholder="Filtrar por nombre o correo…" value={filtro} onChange={(e) => setFiltro(e.target.value)} />
          </div>
          <div className="spacer" />
          <span className="dim">Un 0 en la cuota significa «usar el valor por defecto»</span>
        </div>

        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Usuario</th>
                <th>Rol</th>
                <th>Estado</th>
                <th>Usado</th>
                <th>Cuota (GB)</th>
                <th>Último acceso</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {visibles.map((u) => (
                <tr key={u.id}>
                  <td>
                    <b>{u.name || u.email.split('@')[0]}</b>
                    {u.id === me.id && <span className="badge accent" style={{ marginLeft: '.35rem' }}>tú</span>}
                    <div className="dim">{u.email}</div>
                  </td>
                  <td>
                    <select
                      value={u.role}
                      disabled={u.id === me.id}
                      onChange={(e) => update(u.id, { role: e.target.value })}
                      style={{ minWidth: 130 }}
                    >
                      <option value="guest">Invitado</option>
                      <option value="member">Miembro</option>
                      <option value="manager">Gestor</option>
                      <option value="admin">Administrador</option>
                    </select>
                  </td>
                  <td>
                    <button
                      className={u.status === 'active' ? 'sm subtle' : 'sm danger'}
                      disabled={u.id === me.id}
                      onClick={() => update(u.id, { status: u.status === 'active' ? 'suspended' : 'active' })}
                      title={u.status === 'active' ? 'Pulsa para suspender' : 'Pulsa para reactivar'}
                    >
                      {u.status === 'active' ? '● Activo' : '○ Suspendido'}
                    </button>
                  </td>
                  <td className="num">{humanBytes(u.used_bytes)}</td>
                  <td style={{ maxWidth: 110 }}>
                    <input
                      type="number"
                      min={0}
                      defaultValue={Math.round(u.quota_bytes / (1024 * 1024 * 1024))}
                      onBlur={(e) => {
                        const bytes = Number(e.target.value) * 1024 * 1024 * 1024
                        if (bytes !== u.quota_bytes) update(u.id, { quota_bytes: bytes })
                      }}
                    />
                  </td>
                  <td className="dim">{formatDate(u.last_login_at)}</td>
                  <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                    <button
                      className="ghost sm"
                      onClick={async () => {
                        if (!confirm(`¿Generar una contraseña nueva para ${u.email}?`)) return
                        const res = await api.admin.resetPassword(u.id)
                        setTemp({ email: u.email, password: res.temporary_password })
                      }}
                    >
                      Restablecer clave
                    </button>
                    <button
                      className="ghost sm"
                      title="Descarga el instalador que monta DocuHub como unidad de disco en el equipo de esta persona"
                      onClick={() => {
                        const equipo = prompt(
                          `Nombre del equipo de ${u.name || u.email.split('@')[0]}:`,
                          `Equipo de ${(u.name || u.email.split('@')[0]).split(' ')[0]}`,
                        )
                        if (!equipo) return
                        // El alta del equipo ocurre al generar el archivo.
                        window.location.href = devices.installerForUser(u.id, equipo, 'W')
                      }}
                    >
                      Instalador
                    </button>
                    {me.role === 'admin' && u.id !== me.id && (
                      <button
                        className="danger sm"
                        onClick={async () => {
                          if (!confirm(`¿Eliminar a ${u.email}? Sus archivos permanecen en la plataforma.`)) return
                          await api.admin.deleteUser(u.id)
                          await load()
                        }}
                      >
                        Eliminar
                      </button>
                    )}
                  </td>
                </tr>
              ))}
              {visibles.length === 0 && (
                <tr>
                  <td colSpan={7} className="empty">
                    Ningún usuario coincide con «{filtro}».
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {creating && (
        <NewUserModal
          onClose={() => setCreating(false)}
          onCreated={(email, password) => {
            setCreating(false)
            if (password) setTemp({ email, password })
            void load()
          }}
        />
      )}
    </div>
  )
}

function NewUserModal({
  onClose, onCreated,
}: {
  onClose: () => void
  onCreated: (email: string, password?: string) => void
}) {
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [role, setRole] = useState('member')
  const [quotaGB, setQuotaGB] = useState(0)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function create() {
    setBusy(true)
    setError('')
    try {
      const res = await api.admin.createUser({
        email,
        name,
        role,
        quota_bytes: quotaGB * 1024 * 1024 * 1024,
      })
      onCreated(res.user.email, res.temporary_password)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudo crear el usuario')
      setBusy(false)
    }
  }

  return (
    <Modal
      title="Nuevo usuario"
      onClose={onClose}
      footer={
        <>
          <button onClick={onClose}>Cancelar</button>
          <button className="primary" onClick={create} disabled={busy || !email.includes('@')}>
            {busy ? 'Creando…' : 'Crear usuario'}
          </button>
        </>
      }
    >
      {error && (
        <div className="alert error">
          <span className="ico">⚠️</span>
          <span>{error}</span>
        </div>
      )}

      <div className="field">
        <label htmlFor="ne">Correo</label>
        <input id="ne" type="email" value={email} autoFocus onChange={(e) => setEmail(e.target.value)} />
      </div>
      <div className="field">
        <label htmlFor="nn">Nombre</label>
        <input id="nn" value={name} onChange={(e) => setName(e.target.value)} placeholder="Nombre y apellido" />
      </div>
      <div className="row" style={{ alignItems: 'flex-start', gap: '1rem' }}>
        <div className="field" style={{ flex: 1 }}>
          <label htmlFor="nr">Rol</label>
          <select id="nr" value={role} onChange={(e) => setRole(e.target.value)}>
            <option value="guest">Invitado</option>
            <option value="member">Miembro</option>
            <option value="manager">Gestor</option>
            <option value="admin">Administrador</option>
          </select>
          <div className="help">
            {role === 'guest' && 'Solo ve y descarga lo que se le permita expresamente.'}
            {role === 'member' && 'Sube y descarga en las carpetas abiertas.'}
            {role === 'manager' && 'Administra carpetas, permisos y usuarios.'}
            {role === 'admin' && 'Control total, incluida la conexión con Google Drive.'}
          </div>
        </div>
        <div className="field" style={{ width: 130 }}>
          <label htmlFor="nq">Cuota (GB)</label>
          <input id="nq" type="number" min={0} value={quotaGB} onChange={(e) => setQuotaGB(Number(e.target.value))} />
          <div className="help">0 = por defecto</div>
        </div>
      </div>

      <div className="alert info">
        <span className="ico">ℹ️</span>
        <span>Se generará una contraseña temporal que verás una sola vez. El usuario deberá cambiarla al entrar.</span>
      </div>
    </Modal>
  )
}

/* ───────────────────────────────────────────────────── Google Drive ── */

function DriveSection({ isAdmin, onChange }: { isAdmin: boolean; onChange: () => void }) {
  const [data, setData] = useState<any>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [syncing, setSyncing] = useState<string | null>(null)
  const [syncResult, setSyncResult] = useState('')

  const load = useCallback(async () => {
    try {
      setData(await api.admin.drive())
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudo consultar el estado')
    }
  }, [])

  useEffect(() => {
    void load()
    const hash = window.location.hash
    if (hash.includes('drive=conectado')) {
      onChange()
      window.location.hash = '/admin/drive'
    } else if (hash.includes('drive_error=')) {
      setError('Google devolvió un error: ' + decodeURIComponent(hash.split('drive_error=')[1]))
      window.location.hash = '/admin/drive'
    }
  }, [load, onChange])

  async function connect() {
    setBusy(true)
    setError('')
    try {
      const res = await api.admin.driveConnect()
      // Google exige una navegación real del navegador, no una petición fetch.
      window.location.href = res.auth_url
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudo iniciar la conexión')
      setBusy(false)
    }
  }

  if (!isAdmin) {
    return (
      <div className="card">
        <div className="empty">Solo un administrador puede gestionar las cuentas de Google Drive.</div>
      </div>
    )
  }

  const cuentas: any[] = data?.accounts ?? []

  return (
    <div>
      <div className="page-head row between">
        <div>
          <h1>Google Drive</h1>
          <div className="lead">Dónde se guardan los archivos de la plataforma.</div>
        </div>
        <button className="primary" onClick={connect} disabled={busy || !data?.configured}>
          {busy ? 'Abriendo Google…' : '+ Conectar cuenta'}
        </button>
      </div>

      {error && (
        <div className="alert error" onClick={() => setError('')}>
          <span className="ico">⚠️</span>
          <span>{error}</span>
        </div>
      )}
      {syncResult && (
        <div className="alert ok" onClick={() => setSyncResult('')}>
          <span className="ico">✓</span>
          <span>{syncResult}</span>
        </div>
      )}
      {!data?.configured && (
        <div className="alert warn">
          <span className="ico">⚠️</span>
          <span>
            Faltan <span className="mono">GOOGLE_CLIENT_ID</span> y <span className="mono">GOOGLE_CLIENT_SECRET</span>{' '}
            en <span className="mono">deploy/.env</span>. Revisa <span className="mono">docs/02-GOOGLE-DRIVE-SETUP.md</span>.
          </span>
        </div>
      )}

      <div className="card">
        <div className="card-head">
          <h2>Cuentas enlazadas</h2>
        </div>

        {cuentas.length === 0 ? (
          <div className="empty">Todavía no hay ninguna cuenta conectada.</div>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Cuenta</th>
                  <th>Estado</th>
                  <th style={{ minWidth: 200 }}>Espacio</th>
                  <th>Comprobado</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {cuentas.map((a) => {
                  const pct = a.quota_total_bytes ? Math.round((a.quota_used_bytes / a.quota_total_bytes) * 100) : 0
                  return (
                    <tr key={a.id}>
                      <td>
                        <b>{a.email}</b>
                        {a.is_primary && <span className="badge accent" style={{ marginLeft: '.35rem' }}>principal</span>}
                        {a.last_error && <div className="dim">{a.last_error}</div>}
                      </td>
                      <td>
                        <span className={`badge ${a.status === 'active' ? 'ok' : 'danger'}`}>{a.status}</span>
                      </td>
                      <td>
                        {a.quota_total_bytes ? (
                          <>
                            <div className="progress">
                              <div className={pct >= 90 ? 'err' : ''} style={{ width: `${Math.max(pct, 1)}%` }} />
                            </div>
                            <span className="dim">
                              {humanBytes(a.quota_used_bytes)} de {humanBytes(a.quota_total_bytes)} ({pct}%)
                            </span>
                          </>
                        ) : (
                          <span className="dim">sin límite informado</span>
                        )}
                      </td>
                      <td className="dim">{formatDate(a.quota_checked_at)}</td>
                      <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                        <button
                          className="ghost sm"
                          onClick={async () => {
                            await api.admin.driveRefresh(a.id)
                            await load()
                          }}
                        >
                          Actualizar
                        </button>
                        {!data?.scope_limited && (
                          <button
                            className="subtle sm"
                            disabled={syncing === a.id}
                            onClick={async () => {
                              setSyncing(a.id)
                              setSyncResult('')
                              try {
                                const r = await api.admin.driveSync(a.id)
                                setSyncResult(
                                  `Escaneo terminado: ${r.files_imported} archivos nuevos (${humanBytes(
                                    r.bytes_imported,
                                  )}), ${r.folders_created} carpetas nuevas, ${r.files_skipped} ya conocidos.`,
                                )
                              } catch (err) {
                                setError(err instanceof ApiError ? err.message : 'El escaneo falló')
                              } finally {
                                setSyncing(null)
                              }
                            }}
                          >
                            {syncing === a.id ? 'Escaneando…' : 'Escanear'}
                          </button>
                        )}
                        {!a.is_primary && (
                          <button
                            className="ghost sm"
                            onClick={async () => {
                              await api.admin.driveSetPrimary(a.id)
                              await load()
                            }}
                          >
                            Hacer principal
                          </button>
                        )}
                        <button
                          className="danger sm"
                          onClick={async () => {
                            if (!confirm(`¿Desconectar ${a.email}? Los archivos seguirán en Drive.`)) return
                            await api.admin.driveDisconnect(a.id)
                            await load()
                            onChange()
                          }}
                        >
                          Desconectar
                        </button>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <div className="card">
        <div className="card-head">
          <h2>Configuración técnica</h2>
        </div>
        <div className="card-body">
          <div className="field">
            <label>URI de redirección autorizada en Google Cloud</label>
            <input readOnly className="mono" value={data?.redirect_uri ?? ''} onFocus={(e) => e.target.select()} />
            <div className="help">Debe coincidir carácter por carácter, o Google responderá redirect_uri_mismatch.</div>
          </div>

          <div className="field" style={{ marginBottom: 0 }}>
            <label>Permiso solicitado</label>
            <input readOnly className="mono" value={data?.scope ?? ''} onFocus={(e) => e.target.select()} />
          </div>

          {data?.scope_limited ? (
            <div className="alert info" style={{ marginTop: '.9rem', marginBottom: 0 }}>
              <span className="ico">ℹ️</span>
              <span>
                <b>Modo de acceso limitado.</b> La plataforma solo ve los archivos que ella misma crea. Es el permiso
                que Google considera no sensible, así que no exige pasar por su proceso de verificación. A cambio, lo
                que subas a mano desde drive.google.com queda fuera de su alcance: sube esos archivos desde aquí.
              </span>
            </div>
          ) : (
            <div className="alert info" style={{ marginTop: '.9rem', marginBottom: 0 }}>
              <span className="ico">ℹ️</span>
              <span>
                <b>Acceso completo.</b> Usa <b>Escanear</b> para registrar en la plataforma los archivos que hayas
                subido a mano dentro de la carpeta DocuHub.
              </span>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

/* ───────────────────────────────────────────────────────── bitácora ── */

const ACCIONES: Record<string, string> = {
  'auth.login': 'Inicio de sesión',
  'auth.logout': 'Cierre de sesión',
  'auth.password_change': 'Cambio de contraseña',
  'file.upload': 'Subida',
  'file.upload_init': 'Inicio de subida',
  'file.download': 'Descarga',
  'file.delete': 'Eliminación',
  'file.update': 'Cambio de datos',
  'file.search': 'Búsqueda',
  'folder.create': 'Carpeta creada',
  'folder.delete': 'Carpeta eliminada',
  'share.create': 'Enlace creado',
  'share.download': 'Descarga por enlace',
  'share.revoke': 'Enlace revocado',
  'share.view': 'Enlace abierto',
  'share.unlock': 'Enlace desbloqueado',
  'permission.grant': 'Permiso otorgado',
  'permission.revoke': 'Permiso revocado',
  'user.create': 'Usuario creado',
  'user.update': 'Usuario modificado',
  'user.delete': 'Usuario eliminado',
  'drive.connect': 'Drive conectado',
  'drive.sync': 'Escaneo de Drive',
}

function AuditSection() {
  const [entries, setEntries] = useState<any[]>([])
  const [action, setAction] = useState('')
  const [days, setDays] = useState('30')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const params = new URLSearchParams({ limit: '200' })
      if (action) params.set('action', action)
      if (days) params.set('days', days)
      const res = await api.admin.audit(params.toString())
      setEntries(res.entries)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudo leer la bitácora')
    } finally {
      setLoading(false)
    }
  }, [action, days])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <div>
      <div className="page-head">
        <h1>Bitácora</h1>
        <div className="lead">Todo lo que ha pasado en la plataforma, con su autor y su dirección IP.</div>
      </div>

      {error && (
        <div className="alert error">
          <span className="ico">⚠️</span>
          <span>{error}</span>
        </div>
      )}

      <div className="card">
        <div className="card-head">
          <select style={{ width: 210 }} value={action} onChange={(e) => setAction(e.target.value)}>
            <option value="">Todas las acciones</option>
            {Object.entries(ACCIONES).map(([k, v]) => (
              <option key={k} value={k}>
                {v}
              </option>
            ))}
          </select>
          <select style={{ width: 150 }} value={days} onChange={(e) => setDays(e.target.value)}>
            <option value="1">Últimas 24 horas</option>
            <option value="7">Últimos 7 días</option>
            <option value="30">Últimos 30 días</option>
            <option value="">Todo</option>
          </select>
          <div className="spacer" />
          <span className="dim">{entries.length} registros</span>
        </div>

        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Cuándo</th>
                <th>Quién</th>
                <th>Acción</th>
                <th>Sobre</th>
                <th>IP</th>
                <th>Resultado</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((e) => (
                <tr key={e.id}>
                  <td className="dim" style={{ whiteSpace: 'nowrap' }}>
                    {formatDate(e.created_at)}
                  </td>
                  <td>{e.actor_email || <span className="dim">anónimo</span>}</td>
                  <td>
                    {ACCIONES[e.action] ?? <span className="mono">{e.action}</span>}
                  </td>
                  <td className="truncate" style={{ maxWidth: 240 }}>
                    {e.resource_name || <span className="dim">—</span>}
                  </td>
                  <td className="mono dim">{e.ip}</td>
                  <td>
                    <span className={`badge ${e.success ? 'ok' : 'danger'}`}>{e.success ? 'ok' : 'falló'}</span>
                  </td>
                </tr>
              ))}
              {entries.length === 0 && !loading && (
                <tr>
                  <td colSpan={6} className="empty">
                    No hay registros con estos filtros.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}
