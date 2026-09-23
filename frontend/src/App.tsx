import { useCallback, useEffect, useState } from 'react'
import { api, ApiError, humanBytes, SessionInfo } from './api'
import LoginPage from './pages/LoginPage'
import HomePage from './pages/HomePage'
import ExplorerPage from './pages/ExplorerPage'
import AdminPage from './pages/AdminPage'
import AccountPage from './pages/AccountPage'
import SharePage from './pages/SharePage'
import SharesPage from './pages/SharesPage'

// Enrutado por hash: sin dependencias y funciona igual servido desde el
// binario de Go, donde no hay servidor que reescriba rutas.
function useHashRoute(): [string, (to: string) => void] {
  const [hash, setHash] = useState(() => window.location.hash.slice(1) || '/')

  useEffect(() => {
    const onChange = () => setHash(window.location.hash.slice(1) || '/')
    window.addEventListener('hashchange', onChange)
    return () => window.removeEventListener('hashchange', onChange)
  }, [])

  const navigate = useCallback((to: string) => {
    window.location.hash = to
  }, [])

  return [hash, navigate]
}

const ROLES: Record<string, string> = {
  admin: 'Administrador',
  manager: 'Gestor',
  member: 'Miembro',
  guest: 'Invitado',
}

export default function App() {
  const [route, navigate] = useHashRoute()
  const [session, setSession] = useState<SessionInfo | null>(null)
  const [loading, setLoading] = useState(true)
  const [menuOpen, setMenuOpen] = useState(false)

  const refreshSession = useCallback(async () => {
    try {
      setSession(await api.me())
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) setSession(null)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void refreshSession()
  }, [refreshSession])

  // Al cambiar de sección se cierra el menú lateral en móvil.
  useEffect(() => {
    setMenuOpen(false)
  }, [route])

  // Los enlaces públicos no requieren sesión: se resuelven antes que nada.
  if (route.startsWith('/s/')) return <SharePage token={route.slice(3)} />

  if (loading) {
    return (
      <div className="auth-wrap">
        <div className="muted">Cargando…</div>
      </div>
    )
  }

  if (!session) return <LoginPage onLogin={refreshSession} />

  const user = session.user
  const isAdmin = user.role === 'admin'
  const isStaff = isAdmin || user.role === 'manager'
  const inAdmin = route.startsWith('/admin')

  const usedPct = session.quota_bytes
    ? Math.min(100, Math.round((user.used_bytes / session.quota_bytes) * 100))
    : 0

  async function logout() {
    await api.logout()
    setSession(null)
    navigate('/')
  }

  const title = (() => {
    if (route === '/') return 'Inicio'
    if (route.startsWith('/archivos') || route.startsWith('/f/')) return 'Archivos'
    if (route.startsWith('/enlaces')) return 'Enlaces compartidos'
    if (route.startsWith('/cuenta')) return 'Mi cuenta'
    if (route === '/admin') return 'Panel de control'
    if (route.startsWith('/admin/usuarios')) return 'Usuarios'
    if (route.startsWith('/admin/drive')) return 'Google Drive'
    if (route.startsWith('/admin/bitacora')) return 'Bitácora'
    return 'DocuHub'
  })()

  return (
    <div className="shell">
      {menuOpen && <div className="scrim" onClick={() => setMenuOpen(false)} />}

      <aside className={`sidebar${menuOpen ? ' open' : ''}`}>
        <div className="sidebar-brand">
          <span className="mark" aria-hidden>
            📁
          </span>
          <div>
            DocuHub
            <small>Constructora Pesam</small>
          </div>
        </div>

        <nav className="sidebar-nav">
          <div className="nav-group">
            <div className="nav-label">Mi espacio</div>
            <NavItem icon="🏠" label="Inicio" active={route === '/'} onClick={() => navigate('/')} />
            <NavItem
              icon="📂"
              label="Archivos"
              active={route.startsWith('/archivos') || route.startsWith('/f/')}
              onClick={() => navigate('/archivos')}
            />
            <NavItem
              icon="🔗"
              label="Enlaces compartidos"
              active={route.startsWith('/enlaces')}
              onClick={() => navigate('/enlaces')}
            />
            <NavItem
              icon="⚙️"
              label="Mi cuenta"
              active={route.startsWith('/cuenta')}
              onClick={() => navigate('/cuenta')}
            />
          </div>

          {/* La segunda agrupación solo existe para quien administra. Un
              miembro nunca ve que haya otra mitad de la plataforma. */}
          {isStaff && (
            <div className="nav-group">
              <div className="nav-label">Administración</div>
              <NavItem
                icon="📊"
                label="Panel de control"
                active={route === '/admin'}
                onClick={() => navigate('/admin')}
              />
              <NavItem
                icon="👥"
                label="Usuarios"
                active={route.startsWith('/admin/usuarios')}
                onClick={() => navigate('/admin/usuarios')}
              />
              {isAdmin && (
                <NavItem
                  icon="☁️"
                  label="Google Drive"
                  active={route.startsWith('/admin/drive')}
                  onClick={() => navigate('/admin/drive')}
                />
              )}
              <NavItem
                icon="📋"
                label="Bitácora"
                active={route.startsWith('/admin/bitacora')}
                onClick={() => navigate('/admin/bitacora')}
              />
            </div>
          )}
        </nav>

        <div className="sidebar-foot">
          <div className="sidebar-user">
            <div className="avatar" aria-hidden>
              {(user.name || user.email).slice(0, 2).toUpperCase()}
            </div>
            <div className="who">
              <b title={user.email}>{user.name || user.email.split('@')[0]}</b>
              <span>{ROLES[user.role] ?? user.role}</span>
            </div>
          </div>

          <div className="quota-mini">
            <div className="row between" style={{ gap: '.3rem' }}>
              <span>Espacio</span>
              <span>{usedPct}%</span>
            </div>
            <div className="bar">
              <div
                className={usedPct >= 95 ? 'full' : usedPct >= 80 ? 'high' : ''}
                style={{ width: `${Math.max(usedPct, 2)}%` }}
              />
            </div>
            <span>
              {humanBytes(user.used_bytes)} de {humanBytes(session.quota_bytes)}
            </span>
          </div>

          <button className="nav-item" onClick={logout}>
            <span className="nav-icon" aria-hidden>
              ⏻
            </span>
            Cerrar sesión
          </button>
        </div>
      </aside>

      <div className="main">
        <header className="topbar">
          <button className="ghost sidebar-toggle" onClick={() => setMenuOpen(true)} aria-label="Abrir menú">
            ☰
          </button>
          <h1>{title}</h1>
          {inAdmin && <span className="badge accent">Administración</span>}
          <div className="spacer" />
          {!session.drive_connected && isAdmin && (
            <button className="subtle sm" onClick={() => navigate('/admin/drive')}>
              ⚠️ Conectar Drive
            </button>
          )}
        </header>

        <main className="page">
          {!session.drive_connected && (
            <div className="alert warn">
              <span className="ico">⚠️</span>
              <span>
                Google Drive todavía no está conectado.{' '}
                {isAdmin ? (
                  <>
                    Ve a <b>Administración → Google Drive</b> y enlaza la cuenta de la empresa.
                  </>
                ) : (
                  <>Pídele a un administrador que enlace la cuenta de la empresa.</>
                )}
              </span>
            </div>
          )}

          {user.must_change_password && !route.startsWith('/cuenta') && (
            <div className="alert warn">
              <span className="ico">🔑</span>
              <span>
                Tu contraseña es temporal.{' '}
                <a
                  href="#/cuenta"
                  onClick={(e) => {
                    e.preventDefault()
                    navigate('/cuenta')
                  }}
                >
                  Cámbiala ahora
                </a>
                .
              </span>
            </div>
          )}

          {route === '/' && <HomePage session={session} navigate={navigate} />}

          {(route.startsWith('/archivos') || route.startsWith('/f/')) && (
            <ExplorerPage
              folderId={route.startsWith('/f/') ? route.slice(3) : null}
              session={session}
              navigate={navigate}
            />
          )}

          {route.startsWith('/enlaces') && <SharesPage />}
          {route.startsWith('/cuenta') && <AccountPage session={session} onChange={refreshSession} />}

          {inAdmin && (
            <AdminPage
              section={
                route.startsWith('/admin/usuarios')
                  ? 'usuarios'
                  : route.startsWith('/admin/drive')
                    ? 'drive'
                    : route.startsWith('/admin/bitacora')
                      ? 'bitacora'
                      : 'panel'
              }
              session={session}
              onSessionChange={refreshSession}
              navigate={navigate}
            />
          )}
        </main>
      </div>
    </div>
  )
}

function NavItem({
  icon, label, active, onClick,
}: {
  icon: string
  label: string
  active: boolean
  onClick: () => void
}) {
  return (
    <button className={`nav-item${active ? ' active' : ''}`} onClick={onClick}>
      <span className="nav-icon" aria-hidden>
        {icon}
      </span>
      {label}
    </button>
  )
}
