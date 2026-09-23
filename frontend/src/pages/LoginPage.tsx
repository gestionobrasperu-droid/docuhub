import { FormEvent, useState } from 'react'
import { api, ApiError } from '../api'

export default function LoginPage({ onLogin }: { onLogin: () => void }) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setBusy(true)
    try {
      await api.login(email, password)
      onLogin()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudo iniciar sesión')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="auth-wrap">
      <div className="auth-card">
        <div className="auth-brand">
          <div className="mark" aria-hidden>
            📁
          </div>
          <h1>DocuHub</h1>
          <p>Gestión documental · Constructora Pesam</p>
        </div>

        <form className="card" onSubmit={submit}>
          <div className="card-body">
            {error && (
              <div className="alert error">
                <span className="ico">⚠️</span>
                <span>{error}</span>
              </div>
            )}

            <div className="field">
              <label htmlFor="email">Correo</label>
              <input
                id="email"
                type="email"
                autoComplete="username"
                placeholder="nombre@constructorapesam.com"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
                autoFocus
              />
            </div>

            <div className="field">
              <label htmlFor="password">Contraseña</label>
              <input
                id="password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </div>

            <button className="primary block" type="submit" disabled={busy}>
              {busy ? 'Entrando…' : 'Entrar'}
            </button>
          </div>
        </form>

        <p className="dim" style={{ textAlign: 'center', marginTop: '1rem' }}>
          El acceso es nominal. Si no tienes cuenta, pídesela a un administrador.
        </p>
      </div>
    </div>
  )
}
