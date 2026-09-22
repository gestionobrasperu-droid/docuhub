import { useCallback, useEffect, useState } from 'react'
import { api, ApiError, formatDate, humanBytes, SessionInfo, User } from '../api'
import Modal from '../components/Modal'

type Tab = 'drive' | 'usuarios' | 'uso' | 'bitacora' | 'cuenta'

export default function AdminPage({
  session, onSessionChange,
}: {
  session: SessionInfo
  onSessionChange: () => void
}) {
  const [tab, setTab] = useState<Tab>('drive')

  return (
    <div>
      <h1>Administración</h1>
      <div className="tabs">
        <button className={tab === 'drive' ? 'active' : ''} onClick={() => setTab('drive')}>
          Google Drive
        </button>
        <button className={tab === 'usuarios' ? 'active' : ''} onClick={() => setTab('usuarios')}>
          Usuarios
        </button>
        <button className={tab === 'uso' ? 'active' : ''} onClick={() => setTab('uso')}>
          Uso y estadísticas
        </button>
        <button className={tab === 'bitacora' ? 'active' : ''} onClick={() => setTab('bitacora')}>
          Bitácora
        </button>
        <button className={tab === 'cuenta' ? 'active' : ''} onClick={() => setTab('cuenta')}>
          Mi cuenta
        </button>
      </div>

      {tab === 'drive' && <DriveTab isAdmin={session.user.role === 'admin'} onChange={onSessionChange} />}
      {tab === 'usuarios' && <UsersTab me={session.user} />}
      {tab === 'uso' && <UsageTab />}
      {tab === 'bitacora' && <AuditTab />}
      {tab === 'cuenta' && <AccountTab />}
    </div>
  )
}

// ------------------------------------------------------------ Google Drive --

function DriveTab({ isAdmin, onChange }: { isAdmin: boolean; onChange: () => void }) {
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
    // Al volver del consentimiento de Google, la URL trae el resultado.
    const hash = window.location.hash
    if (hash.includes('drive=conectado')) {
      onChange()
      window.location.hash = '/admin'
    } else if (hash.includes('drive_error=')) {
      setError('Google devolvió un error: ' + decodeURIComponent(hash.split('drive_error=')[1]))
      window.location.hash = '/admin'
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
    return <div className="card">Solo un administrador puede gestionar las cuentas de Google Drive.</div>
  }

  const accounts: any[] = data?.accounts ?? []

  return (
    <div>
      {error && <div className="alert error">{error}</div>}
      {syncResult && (
        <div className="alert ok" onClick={() => setSyncResult('')}>
          {syncResult}
        </div>
      )}

      {!data?.configured && (
        <div className="alert warn">
          Faltan <span className="mono">GOOGLE_CLIENT_ID</span> y{' '}
          <span className="mono">GOOGLE_CLIENT_SECRET</span> en el archivo <span className="mono">deploy/.env</span>.
          Revisa <span className="mono">docs/02-GOOGLE-DRIVE-SETUP.md</span>.
        </div>
      )}

      <div className="card">
        <div className="row between">
          <div>
            <h2>Cuentas enlazadas</h2>
            <p className="muted" style={{ margin: 0 }}>
              Los archivos se guardan en el Drive de estas cuentas. La plataforma controla quién
              accede a ellos.
            </p>
          </div>
          <button className="primary" onClick={connect} disabled={busy || !data?.configured}>
            {busy ? 'Abriendo Google…' : 'Conectar cuenta'}
          </button>
        </div>

        {data?.redirect_uri && (
          <p className="muted mono" style={{ marginTop: '.7rem' }}>
            URI de redirección que debe estar autorizada en Google Cloud: {data.redirect_uri}
          </p>
        )}

        {data?.scope_limited ? (
          <p className="muted">
            <b>Modo de acceso limitado</b> (permiso <span className="mono">drive.file</span>): la
            plataforma solo ve los archivos que ella misma crea. Es el permiso que Google considera
            no sensible, asi que no exige pasar por su proceso de verificacion. A cambio, lo que
            subas a mano desde drive.google.com queda fuera de su alcance y <b>Escanear Drive</b> no
            lo encontrara: sube esos archivos desde aqui.
          </p>
        ) : (
          <p className="muted">
            <b>Escanear Drive</b> registra en la plataforma los archivos y carpetas que hayas subido
            a mano desde drive.google.com dentro de la carpeta <b>DocuHub</b>. A partir de ese
            momento quedan bajo el mismo control de permisos, cuotas y auditoria que los subidos
            desde aqui.
          </p>
        )}

        {accounts.length === 0 ? (
          <p className="muted">Todavía no hay ninguna cuenta conectada.</p>
        ) : (
          <div className="table-wrap" style={{ marginTop: '1rem' }}>
            <table>
              <thead>
                <tr>
                  <th>Cuenta</th>
                  <th>Estado</th>
                  <th>Espacio</th>
                  <th>Comprobado</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {accounts.map((a) => {
                  const pct = a.quota_total_bytes
                    ? Math.round((a.quota_used_bytes / a.quota_total_bytes) * 100)
                    : 0
                  return (
                    <tr key={a.id}>
                      <td>
                        <b>{a.email}</b>
                        {a.is_primary && <span className="badge accent"> principal</span>}
                        {a.last_error && <div className="muted">{a.last_error}</div>}
                      </td>
                      <td>
                        <span className={`badge ${a.status === 'active' ? 'ok' : 'danger'}`}>{a.status}</span>
                      </td>
                      <td style={{ minWidth: 160 }}>
                        {a.quota_total_bytes ? (
                          <>
                            <div className="progress">
                              <div
                                style={{
                                  width: `${pct}%`,
                                  background: pct > 90 ? 'var(--danger)' : undefined,
                                }}
                              />
                            </div>
                            <span className="muted">
                              {humanBytes(a.quota_used_bytes)} de {humanBytes(a.quota_total_bytes)} ({pct}%)
                            </span>
                          </>
                        ) : (
                          <span className="muted">sin límite informado</span>
                        )}
                      </td>
                      <td className="muted">{formatDate(a.quota_checked_at)}</td>
                      <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                        <button
                          className="small ghost"
                          onClick={async () => {
                            await api.admin.driveRefresh(a.id)
                            await load()
                          }}
                        >
                          Actualizar
                        </button>
                        <button
                          className="small"
                          disabled={syncing === a.id}
                          title="Registra en la plataforma los archivos que subiste a mano desde drive.google.com"
                          onClick={async () => {
                            setSyncing(a.id)
                            setSyncResult('')
                            try {
                              const r = await api.admin.driveSync(a.id)
                              setSyncResult(
                                `Escaneo terminado: ${r.files_imported} archivos nuevos ` +
                                  `(${humanBytes(r.bytes_imported)}), ${r.folders_created} carpetas nuevas, ` +
                                  `${r.files_skipped} ya conocidos.` +
                                  (r.truncated ? ' Se alcanzó el límite; vuelve a escanear para continuar.' : '') +
                                  (r.warnings.length ? ` Avisos: ${r.warnings.slice(0, 3).join('; ')}` : ''),
                              )
                            } catch (err) {
                              setError(err instanceof ApiError ? err.message : 'El escaneo falló')
                            } finally {
                              setSyncing(null)
                            }
                          }}
                        >
                          {syncing === a.id ? 'Escaneando…' : 'Escanear Drive'}
                        </button>
                        {!a.is_primary && (
                          <button
                            className="small ghost"
                            onClick={async () => {
                              await api.admin.driveSetPrimary(a.id)
                              await load()
                            }}
                          >
                            Hacer principal
                          </button>
                        )}
                        <button
                          className="small danger"
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

      {data?.root_folder && (
        <div className="card">
          <h3>Carpeta raíz</h3>
          <p className="muted" style={{ margin: 0 }}>
            Todo lo que sube la plataforma vive dentro de <b>{data.root_folder.name}</b> en el Drive
            de la cuenta principal.
          </p>
        </div>
      )}
    </div>
  )
}

// ---------------------------------------------------------------- usuarios --

function UsersTab({ me }: { me: User }) {
  const [users, setUsers] = useState<User[]>([])
  const [defaults, setDefaults] = useState({ quota: 0, bandwidth: 0 })
  const [error, setError] = useState('')
  const [creating, setCreating] = useState(false)
  const [tempPassword, setTempPassword] = useState<{ email: string; password: string } | null>(null)

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

  return (
    <div>
      {error && <div className="alert error">{error}</div>}

      {tempPassword && (
        <div className="alert ok">
          Contraseña temporal de <b>{tempPassword.email}</b>:{' '}
          <span className="mono">{tempPassword.password}</span> — entrégasela por un canal seguro;
          no se vuelve a mostrar.
          <button className="ghost small" onClick={() => setTempPassword(null)}>
            Ocultar
          </button>
        </div>
      )}

      <div className="card">
        <div className="row between">
          <h2>Usuarios ({users.length})</h2>
          <button className="primary" onClick={() => setCreating(true)}>
            Nuevo usuario
          </button>
        </div>
        <p className="muted">
          Cuota por defecto: {humanBytes(defaults.quota)} de almacenamiento y{' '}
          {humanBytes(defaults.bandwidth)} de descarga al mes. Un 0 en la tabla significa «usar el
          valor por defecto».
        </p>

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
              {users.map((u) => (
                <tr key={u.id}>
                  <td>
                    <b>{u.email}</b>
                    <div className="muted">{u.name}</div>
                  </td>
                  <td>
                    <select
                      value={u.role}
                      disabled={u.id === me.id}
                      onChange={(e) => update(u.id, { role: e.target.value })}
                    >
                      <option value="guest">Invitado</option>
                      <option value="member">Miembro</option>
                      <option value="manager">Gestor</option>
                      <option value="admin">Administrador</option>
                    </select>
                  </td>
                  <td>
                    <button
                      className={`small ${u.status === 'active' ? 'ghost' : 'danger'}`}
                      disabled={u.id === me.id}
                      onClick={() =>
                        update(u.id, { status: u.status === 'active' ? 'suspended' : 'active' })
                      }
                    >
                      {u.status === 'active' ? 'Activo' : 'Suspendido'}
                    </button>
                  </td>
                  <td>{humanBytes(u.used_bytes)}</td>
                  <td style={{ maxWidth: 110 }}>
                    <input
                      type="number"
                      min={0}
                      defaultValue={Math.round(u.quota_bytes / (1024 * 1024 * 1024))}
                      onBlur={(e) => {
                        const gb = Number(e.target.value)
                        const bytes = gb * 1024 * 1024 * 1024
                        if (bytes !== u.quota_bytes) update(u.id, { quota_bytes: bytes })
                      }}
                    />
                  </td>
                  <td className="muted">{formatDate(u.last_login_at)}</td>
                  <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                    <button
                      className="small ghost"
                      onClick={async () => {
                        if (!confirm(`¿Generar una contraseña nueva para ${u.email}?`)) return
                        const res = await api.admin.resetPassword(u.id)
                        setTempPassword({ email: u.email, password: res.temporary_password })
                      }}
                    >
                      Restablecer clave
                    </button>
                    {me.role === 'admin' && u.id !== me.id && (
                      <button
                        className="small danger"
                        onClick={async () => {
                          if (!confirm(`¿Eliminar a ${u.email}? Sus archivos permanecen.`)) return
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
            </tbody>
          </table>
        </div>
      </div>

      {creating && (
        <NewUserModal
          onClose={() => setCreating(false)}
          onCreated={(email, password) => {
            setCreating(false)
            if (password) setTempPassword({ email, password })
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
            Crear
          </button>
        </>
      }
    >
      {error && <div className="alert error">{error}</div>}
      <div className="field">
        <label htmlFor="ne">Correo</label>
        <input id="ne" type="email" value={email} autoFocus onChange={(e) => setEmail(e.target.value)} />
      </div>
      <div className="field">
        <label htmlFor="nn">Nombre</label>
        <input id="nn" value={name} onChange={(e) => setName(e.target.value)} />
      </div>
      <div className="row" style={{ gap: '1rem' }}>
        <div className="field" style={{ flex: 1 }}>
          <label htmlFor="nr">Rol</label>
          <select id="nr" value={role} onChange={(e) => setRole(e.target.value)}>
            <option value="guest">Invitado — solo ve y descarga lo que se le permita</option>
            <option value="member">Miembro — sube y descarga</option>
            <option value="manager">Gestor — administra carpetas y usuarios</option>
            <option value="admin">Administrador — control total</option>
          </select>
        </div>
        <div className="field" style={{ width: 130 }}>
          <label htmlFor="nq">Cuota (GB)</label>
          <input
            id="nq"
            type="number"
            min={0}
            value={quotaGB}
            onChange={(e) => setQuotaGB(Number(e.target.value))}
          />
        </div>
      </div>
      <p className="muted">
        Se generará una contraseña temporal que verás una sola vez. El usuario deberá cambiarla.
      </p>
    </Modal>
  )
}

// ------------------------------------------------------------------- uso ---

function UsageTab() {
  const [data, setData] = useState<any>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    api.admin
      .stats()
      .then(setData)
      .catch((err) => setError(err instanceof ApiError ? err.message : 'No se pudieron cargar las estadísticas'))
  }, [])

  if (error) return <div className="alert error">{error}</div>
  if (!data) return <div className="muted">Calculando…</div>

  const t = data.totals
  const traffic: any[] = data.traffic ?? []
  const maxTraffic = Math.max(1, ...traffic.map((d) => d.uploads + d.downloads))

  return (
    <div>
      <div className="grid">
        <Stat label="Archivos" value={String(t.files)} hint={humanBytes(t.stored_bytes) + ' almacenados'} />
        <Stat label="Usuarios activos" value={String(t.active_users)} hint={`${t.users} en total`} />
        <Stat label="Descargas" value={String(t.downloads)} hint={humanBytes(t.download_bytes_30d) + ' en 30 días'} />
        <Stat label="Enlaces vigentes" value={String(t.active_shares)} hint={`${t.uploads_today} subidas hoy`} />
      </div>

      <div className="card">
        <h3>Tráfico de los últimos {traffic.length} días</h3>
        <div className="bar-chart">
          {traffic.map((d) => (
            <div
              key={d.day}
              className="bar"
              style={{ height: `${((d.uploads + d.downloads) / maxTraffic) * 100}%` }}
              title={`${d.day}: ${humanBytes(d.uploads)} subidos, ${humanBytes(d.downloads)} descargados`}
            />
          ))}
        </div>
        <p className="muted">Pasa el cursor por cada barra para ver el detalle del día.</p>
      </div>

      <div className="card">
        <h3>Consumo por usuario</h3>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Usuario</th>
                <th>Archivos</th>
                <th>Almacenado</th>
                <th>Cuota</th>
                <th>Descargado (mes)</th>
              </tr>
            </thead>
            <tbody>
              {(data.usage_by_user ?? []).map((u: any) => (
                <tr key={u.email}>
                  <td>
                    <b>{u.email}</b>
                    <div className="muted">{u.name}</div>
                  </td>
                  <td>{u.file_count}</td>
                  <td>{humanBytes(u.stored_bytes)}</td>
                  <td className="muted">{u.quota_bytes ? humanBytes(u.quota_bytes) : 'por defecto'}</td>
                  <td>{humanBytes(u.download_bytes_30d)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      <div className="card">
        <h3>Archivos más descargados</h3>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Archivo</th>
                <th>Tamaño</th>
                <th>Descargas</th>
                <th>Propietario</th>
              </tr>
            </thead>
            <tbody>
              {(data.top_files ?? []).length === 0 && (
                <tr>
                  <td colSpan={4} className="muted">
                    Todavía no hay descargas registradas.
                  </td>
                </tr>
              )}
              {(data.top_files ?? []).map((f: any, i: number) => (
                <tr key={i}>
                  <td>{f.name}</td>
                  <td>{humanBytes(f.size_bytes)}</td>
                  <td>{f.downloads}</td>
                  <td className="muted">{f.owner}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}

function Stat({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="card stat">
      <div className="value">{value}</div>
      <div className="label">{label}</div>
      {hint && <div className="muted">{hint}</div>}
    </div>
  )
}

// -------------------------------------------------------------- bitácora ---

function AuditTab() {
  const [entries, setEntries] = useState<any[]>([])
  const [action, setAction] = useState('')
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    try {
      const params = new URLSearchParams({ limit: '150' })
      if (action) params.set('action', action)
      const res = await api.admin.audit(params.toString())
      setEntries(res.entries)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudo leer la bitácora')
    }
  }, [action])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <div className="card">
      <div className="row between">
        <h2>Bitácora</h2>
        <select style={{ width: 240 }} value={action} onChange={(e) => setAction(e.target.value)}>
          <option value="">Todas las acciones</option>
          <option value="auth.login">Inicios de sesión</option>
          <option value="file.upload">Subidas</option>
          <option value="file.download">Descargas</option>
          <option value="file.delete">Eliminaciones</option>
          <option value="share.create">Enlaces creados</option>
          <option value="share.download">Descargas por enlace</option>
          <option value="permission.grant">Permisos otorgados</option>
          <option value="drive.connect">Conexiones de Drive</option>
        </select>
      </div>
      {error && <div className="alert error">{error}</div>}

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
                <td className="muted" style={{ whiteSpace: 'nowrap' }}>{formatDate(e.created_at)}</td>
                <td>{e.actor_email || '—'}</td>
                <td className="mono">{e.action}</td>
                <td>{e.resource_name || e.resource_id || '—'}</td>
                <td className="mono muted">{e.ip}</td>
                <td>
                  <span className={`badge ${e.success ? 'ok' : 'danger'}`}>
                    {e.success ? 'ok' : 'falló'}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

// ------------------------------------------------------------- mi cuenta ---

function AccountTab() {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [msg, setMsg] = useState('')
  const [error, setError] = useState('')

  async function change() {
    setError('')
    setMsg('')
    try {
      await api.changePassword(current, next)
      setMsg('Contraseña actualizada. Las demás sesiones se cerraron.')
      setCurrent('')
      setNext('')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudo cambiar la contraseña')
    }
  }

  return (
    <div className="card" style={{ maxWidth: 440 }}>
      <h2>Cambiar mi contraseña</h2>
      {error && <div className="alert error">{error}</div>}
      {msg && <div className="alert ok">{msg}</div>}

      <div className="field">
        <label htmlFor="cp">Contraseña actual</label>
        <input id="cp" type="password" value={current} onChange={(e) => setCurrent(e.target.value)} />
      </div>
      <div className="field">
        <label htmlFor="np">Contraseña nueva</label>
        <input id="np" type="password" value={next} onChange={(e) => setNext(e.target.value)} />
        <span className="muted">Mínimo 10 caracteres. Una frase larga es más segura que símbolos raros.</span>
      </div>
      <button className="primary" onClick={change} disabled={!current || next.length < 10}>
        Cambiar
      </button>
    </div>
  )
}
