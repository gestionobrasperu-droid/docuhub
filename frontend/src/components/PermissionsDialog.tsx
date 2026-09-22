import { useCallback, useEffect, useState } from 'react'
import { api, ApiError, formatDate } from '../api'
import Modal from './Modal'

interface Props {
  resourceType: 'file' | 'folder'
  resourceId: string
  resourceName: string
  onClose: () => void
}

const LEVELS: { value: string; label: string; help: string }[] = [
  { value: 'viewer', label: 'Ver', help: 'Ve que existe y sus datos, pero no lo descarga' },
  { value: 'downloader', label: 'Descargar', help: 'Además puede descargarlo' },
  { value: 'editor', label: 'Editar', help: 'Además sube, renombra y crea subcarpetas' },
  { value: 'manager', label: 'Responsable', help: 'Además elimina y otorga permisos' },
]

export default function PermissionsDialog({ resourceType, resourceId, resourceName, onClose }: Props) {
  const [perms, setPerms] = useState<any[]>([])
  const [email, setEmail] = useState('')
  const [level, setLevel] = useState('downloader')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      const res = await api.permissions(resourceType, resourceId)
      setPerms(res.permissions)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudieron cargar los permisos')
    }
  }, [resourceType, resourceId])

  useEffect(() => {
    void load()
  }, [load])

  async function grant() {
    if (!email.trim()) return
    setBusy(true)
    setError('')
    try {
      await api.grantPermission({
        subject_email: email.trim(),
        resource_type: resourceType,
        resource_id: resourceId,
        level,
      })
      setEmail('')
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudo otorgar el permiso')
    } finally {
      setBusy(false)
    }
  }

  async function revoke(id: string) {
    try {
      await api.revokePermission(id)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudo revocar')
    }
  }

  return (
    <Modal
      title={`Permisos de «${resourceName}»`}
      onClose={onClose}
      footer={<button onClick={onClose}>Cerrar</button>}
    >
      {error && <div className="alert error">{error}</div>}

      <div className="row" style={{ alignItems: 'flex-end', gap: '.5rem' }}>
        <div className="field" style={{ flex: 2, marginBottom: 0 }}>
          <label htmlFor="pe">Correo del usuario</label>
          <input
            id="pe"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="persona@empresa.com"
          />
        </div>
        <div className="field" style={{ flex: 1, marginBottom: 0 }}>
          <label htmlFor="pl">Nivel</label>
          <select id="pl" value={level} onChange={(e) => setLevel(e.target.value)}>
            {LEVELS.map((l) => (
              <option key={l.value} value={l.value}>
                {l.label}
              </option>
            ))}
          </select>
        </div>
        <button className="primary" onClick={grant} disabled={busy || !email.trim()}>
          Otorgar
        </button>
      </div>
      <p className="muted">{LEVELS.find((l) => l.value === level)?.help}</p>

      <div className="table-wrap" style={{ marginTop: '1rem' }}>
        <table>
          <thead>
            <tr>
              <th>Quién</th>
              <th>Nivel</th>
              <th>Desde</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {perms.length === 0 && (
              <tr>
                <td colSpan={4} className="muted">
                  Sin permisos explícitos. El acceso lo decide el rol de cada usuario
                  {resourceType === 'folder' ? ', salvo que la carpeta esté restringida.' : '.'}
                </td>
              </tr>
            )}
            {perms.map((p) => (
              <tr key={p.id}>
                <td>{p.subject_label || p.subject_id}</td>
                <td>
                  <span className="badge accent">
                    {LEVELS.find((l) => l.value === p.level)?.label ?? p.level}
                  </span>
                </td>
                <td className="muted">{formatDate(p.created_at)}</td>
                <td style={{ textAlign: 'right' }}>
                  <button className="small danger" onClick={() => revoke(p.id)}>
                    Quitar
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Modal>
  )
}
