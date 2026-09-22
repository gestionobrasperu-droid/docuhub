import { useState } from 'react'
import { api, ApiError, FileItem, Folder } from '../api'
import Modal from './Modal'

interface Props {
  file?: FileItem
  folder?: Folder
  onClose: () => void
}

/**
 * Crea un enlace público. El token completo se ve una sola vez: el servidor
 * solo guarda su hash, así que si se pierde hay que emitir otro.
 */
export default function ShareDialog({ file, folder, onClose }: Props) {
  const [password, setPassword] = useState('')
  const [days, setDays] = useState(7)
  const [maxDownloads, setMaxDownloads] = useState(0)
  const [note, setNote] = useState('')
  const [url, setUrl] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [copied, setCopied] = useState(false)

  const name = file?.name ?? folder?.name ?? ''

  async function create() {
    setBusy(true)
    setError('')
    try {
      const res = await api.createShare({
        file_id: file?.id,
        folder_id: folder?.id,
        password: password || undefined,
        max_downloads: maxDownloads,
        expires_in_days: days,
        note,
      })
      setUrl(res.url)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudo crear el enlace')
    } finally {
      setBusy(false)
    }
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(url)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      setError('No se pudo copiar automáticamente; selecciona el enlace y cópialo a mano.')
    }
  }

  return (
    <Modal
      title={`Compartir «${name}»`}
      onClose={onClose}
      footer={
        url ? (
          <button className="primary" onClick={onClose}>
            Listo
          </button>
        ) : (
          <>
            <button onClick={onClose}>Cancelar</button>
            <button className="primary" onClick={create} disabled={busy}>
              {busy ? 'Creando…' : 'Crear enlace'}
            </button>
          </>
        )
      }
    >
      {error && <div className="alert error">{error}</div>}

      {url ? (
        <>
          <div className="alert ok">
            Enlace creado. Cópialo ahora: por seguridad no se vuelve a mostrar.
          </div>
          <div className="field">
            <label>Enlace</label>
            <input readOnly value={url} onFocus={(e) => e.target.select()} className="mono" />
          </div>
          <button onClick={copy}>{copied ? '✓ Copiado' : 'Copiar enlace'}</button>
          {password && (
            <p className="muted">
              Recuerda enviar la contraseña por un canal distinto al del enlace.
            </p>
          )}
        </>
      ) : (
        <>
          <p className="muted" style={{ marginTop: 0 }}>
            Quien reciba este enlace podrá descargar sin tener cuenta. Cada apertura queda
            registrada con su dirección IP.
          </p>

          <div className="field">
            <label htmlFor="sp">Contraseña (opcional)</label>
            <input
              id="sp"
              type="text"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="Sin contraseña"
            />
          </div>

          <div className="row" style={{ gap: '1rem' }}>
            <div className="field" style={{ flex: 1 }}>
              <label htmlFor="sd">Caduca en (días)</label>
              <input
                id="sd"
                type="number"
                min={0}
                value={days}
                onChange={(e) => setDays(Number(e.target.value))}
              />
              <span className="muted">0 = sin caducidad</span>
            </div>
            <div className="field" style={{ flex: 1 }}>
              <label htmlFor="smd">Máximo de descargas</label>
              <input
                id="smd"
                type="number"
                min={0}
                value={maxDownloads}
                onChange={(e) => setMaxDownloads(Number(e.target.value))}
              />
              <span className="muted">0 = sin límite</span>
            </div>
          </div>

          <div className="field">
            <label htmlFor="sn">Nota interna</label>
            <input
              id="sn"
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="Ej.: enviado al cliente para la valorización de marzo"
            />
          </div>
        </>
      )}
    </Modal>
  )
}
