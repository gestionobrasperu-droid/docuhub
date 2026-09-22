import { useCallback, useEffect, useState } from 'react'
import { api, ApiError, humanBytes, SessionInfo } from './api'
import LoginPage from './pages/LoginPage'
import ExplorerPage from './pages/ExplorerPage'
import AdminPage from './pages/AdminPage'
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

export default function App() {
  const [route, navigate] = useHashRoute()
  const [session, setSession] = useState<SessionInfo | null>(null)
  const [loading, setLoading] = useState(true)

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

  // Los enlaces públicos no requieren sesión: se resuelven antes que nada.
  if (route.startsWith('/s/')) {
    return <SharePage token={route.slice(3)} />
  }

  if (loading) {
    return (
      <div className="login-wrap">
        <div className="muted">Cargando…</div>
      </div>
    )
  }

  if (!session) {
    return <LoginPage onLogin={refreshSession} />
  }

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <span aria-hidden>🗂️</span> DocuHub
        </div>
        <nav>
          <button className={route === '/' || route.startsWith('/f/') ? 'ghost active' : 'ghost'}
                  onClick={() => navigate('/')}>
            Archivos
          </button>
          <button className={route.startsWith('/enlaces') ? 'ghost active' : 'ghost'}
                  onClick={() => navigate('/enlaces')}>
            Enlaces
          </button>
          {(session.user.role === 'admin' || session.user.role === 'manager') && (
            <button className={route.startsWith('/admin') ? 'ghost active' : 'ghost'}
                    onClick={() => navigate('/admin')}>
              Administración
            </button>
          )}
        </nav>
        <div className="spacer" />
        <span className="muted" title={`Rol: ${session.user.role}`}>
          {session.user.email} · {humanBytes(session.user.used_bytes)} de {humanBytes(session.quota_bytes)}
        </span>
        <button
          className="ghost"
          onClick={async () => {
            await api.logout()
            setSession(null)
            navigate('/')
          }}
        >
          Salir
        </button>
      </header>

      <main className="content">
        {!session.drive_connected && (
          <div className="alert warn">
            Google Drive todavía no está conectado.{' '}
            {session.user.role === 'admin' ? (
              <>Ve a <b>Administración → Google Drive</b> y enlaza la cuenta de la empresa.</>
            ) : (
              <>Pídele a un administrador que enlace la cuenta de la empresa.</>
            )}
          </div>
        )}

        {session.user.must_change_password && (
          <div className="alert warn">
            Tu contraseña es temporal. Cámbiala desde <b>Administración → Mi cuenta</b>.
          </div>
        )}

        {route.startsWith('/admin') && <AdminPage session={session} onSessionChange={refreshSession} />}
        {route.startsWith('/enlaces') && <SharesPage />}
        {(route === '/' || route.startsWith('/f/')) && (
          <ExplorerPage
            folderId={route.startsWith('/f/') ? route.slice(3) : null}
            session={session}
            navigate={navigate}
          />
        )}
      </main>
    </div>
  )
}
