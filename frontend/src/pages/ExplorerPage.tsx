import { DragEvent, FormEvent, useCallback, useEffect, useRef, useState } from 'react'
import {
  api, ApiError, FileItem, Folder, FolderPayload, humanBytes, SessionInfo, uploadFile, UploadProgress,
} from '../api'
import Modal from '../components/Modal'
import ShareDialog from '../components/ShareDialog'
import PermissionsDialog from '../components/PermissionsDialog'

interface Props {
  folderId: string | null
  session: SessionInfo
  navigate: (to: string) => void
}

export default function ExplorerPage({ folderId, session, navigate }: Props) {
  const [data, setData] = useState<FolderPayload | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [uploads, setUploads] = useState<UploadProgress[]>([])
  const [dragging, setDragging] = useState(false)
  const [newFolder, setNewFolder] = useState(false)
  const [shareTarget, setShareTarget] = useState<{ file?: FileItem; folder?: Folder } | null>(null)
  const [permTarget, setPermTarget] = useState<{ type: 'file' | 'folder'; id: string; name: string } | null>(null)
  const [search, setSearch] = useState('')
  const [results, setResults] = useState<FileItem[] | null>(null)
  const fileInput = useRef<HTMLInputElement>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      setData(folderId ? await api.folder(folderId) : await api.root())
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudo abrir la carpeta')
      setData(null)
    } finally {
      setLoading(false)
    }
  }, [folderId])

  useEffect(() => {
    void load()
  }, [load])

  // ------------------------------------------------------------- subidas --

  const startUploads = useCallback(
    async (files: FileList | File[]) => {
      if (!data) return
      const list = Array.from(files)

      for (const file of list) {
        const update = (p: UploadProgress) =>
          setUploads((prev) => {
            const next = prev.filter((u) => u.fileName !== p.fileName)
            return [...next, p]
          })

        try {
          await uploadFile(file, data.folder.id, update)
        } catch (err) {
          update({
            fileName: file.name,
            sent: 0,
            total: file.size,
            percent: 0,
            status: 'error',
            message: err instanceof ApiError ? err.message : 'Falló la subida',
          })
        }
      }
      await load()
      // Las subidas terminadas se limpian tras unos segundos.
      setTimeout(() => setUploads((prev) => prev.filter((u) => u.status === 'error')), 6000)
    },
    [data, load],
  )

  function onDrop(e: DragEvent) {
    e.preventDefault()
    setDragging(false)
    if (e.dataTransfer.files.length) void startUploads(e.dataTransfer.files)
  }

  // ------------------------------------------------------------ acciones --

  async function createFolder(name: string, restricted: boolean) {
    if (!data) return
    try {
      await api.createFolder(data.folder.id, name, restricted)
      setNewFolder(false)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudo crear la carpeta')
    }
  }

  async function removeFile(file: FileItem) {
    if (!confirm(`¿Eliminar "${file.name}"? Va a la papelera de Google Drive, donde se puede recuperar durante 30 días.`))
      return
    try {
      await api.deleteFile(file.id)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudo eliminar')
    }
  }

  async function removeFolder(folder: Folder) {
    if (!confirm(`¿Eliminar la carpeta "${folder.name}" y todo su contenido?`)) return
    try {
      await api.deleteFolder(folder.id)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudo eliminar la carpeta')
    }
  }

  async function rename(kind: 'file' | 'folder', id: string, current: string) {
    const name = prompt('Nuevo nombre:', current)
    if (!name || name === current) return
    try {
      if (kind === 'file') await api.updateFile(id, { name })
      else await api.updateFolder(id, { name })
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudo renombrar')
    }
  }

  async function runSearch(e: FormEvent) {
    e.preventDefault()
    if (search.trim().length < 2) {
      setResults(null)
      return
    }
    try {
      const res = await api.search(search.trim())
      setResults(res.files)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'La búsqueda falló')
    }
  }

  // --------------------------------------------------------------- vista --

  if (loading && !data) return <div className="muted">Cargando…</div>

  return (
    <div
      onDragOver={(e) => {
        e.preventDefault()
        if (data?.can_upload) setDragging(true)
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={onDrop}
    >
      {error && (
        <div className="alert error" onClick={() => setError('')}>
          {error}
        </div>
      )}

      <div className="row between" style={{ marginBottom: '.8rem' }}>
        <form className="row" onSubmit={runSearch} style={{ flex: 1, maxWidth: 420 }}>
          <input
            placeholder="Buscar en toda la plataforma…"
            value={search}
            onChange={(e) => {
              setSearch(e.target.value)
              if (!e.target.value) setResults(null)
            }}
          />
          <button type="submit">Buscar</button>
        </form>

        {data?.can_upload && (
          <div className="row">
            <button onClick={() => setNewFolder(true)}>Nueva carpeta</button>
            <button className="primary" onClick={() => fileInput.current?.click()}>
              Subir archivos
            </button>
            <input
              ref={fileInput}
              type="file"
              multiple
              hidden
              onChange={(e) => {
                if (e.target.files?.length) void startUploads(e.target.files)
                e.target.value = ''
              }}
            />
          </div>
        )}
      </div>

      {uploads.length > 0 && (
        <div className="card">
          <h3>Subidas</h3>
          {uploads.map((u) => (
            <div key={u.fileName} style={{ marginBottom: '.6rem' }}>
              <div className="row between">
                <span className="mono">{u.fileName}</span>
                <span className="muted">
                  {u.status === 'error' ? u.message : `${u.percent}% · ${humanBytes(u.sent)} / ${humanBytes(u.total)}`}
                </span>
              </div>
              <div className="progress">
                <div
                  style={{
                    width: `${u.percent}%`,
                    background: u.status === 'error' ? 'var(--danger)' : undefined,
                  }}
                />
              </div>
            </div>
          ))}
        </div>
      )}

      {results ? (
        <div className="card">
          <div className="row between">
            <h2>Resultados de «{search}»</h2>
            <button className="ghost" onClick={() => setResults(null)}>
              Volver a la carpeta
            </button>
          </div>
          {results.length === 0 && <p className="muted">Sin coincidencias.</p>}
          <div className="item-list">
            {results.map((f) => (
              <FileRow
                key={f.id}
                file={f}
                canManage={false}
                onShare={() => setShareTarget({ file: f })}
                onPermissions={() => setPermTarget({ type: 'file', id: f.id, name: f.name })}
                onRename={() => rename('file', f.id, f.name)}
                onDelete={() => removeFile(f)}
              />
            ))}
          </div>
        </div>
      ) : (
        data && (
          <>
            <nav className="breadcrumb">
              {data.breadcrumb.map((f) => (
                <span key={f.id}>
                  <button onClick={() => navigate(f.is_root ? '/' : `/f/${f.id}`)}>{f.name}</button>
                  <span>/</span>
                </span>
              ))}
              <b>{data.folder.name}</b>
              {data.folder.restricted && <span className="badge warn">restringida</span>}
              <span className="muted">
                · {data.file_count} archivos · {humanBytes(data.total_bytes)}
              </span>
            </nav>

            <div className={`card ${dragging ? 'dropzone active' : ''}`}>
              {data.folders.length === 0 && data.files.length === 0 ? (
                <div className="dropzone">
                  {data.can_upload
                    ? 'Arrastra archivos aquí o usa «Subir archivos»'
                    : 'Esta carpeta está vacía'}
                </div>
              ) : (
                <div className="item-list">
                  {data.folders.map((f) => (
                    <div className="item" key={f.id}>
                      <span className="icon" aria-hidden>
                        {f.restricted ? '🔒' : '📁'}
                      </span>
                      <div className="name">
                        <b>
                          <a href={`#/f/${f.id}`}>{f.name}</a>
                        </b>
                      </div>
                      <div className="actions">
                        <button className="small ghost" onClick={() => setShareTarget({ folder: f })}>
                          Compartir
                        </button>
                        {data.can_manage && (
                          <>
                            <button
                              className="small ghost"
                              onClick={() => setPermTarget({ type: 'folder', id: f.id, name: f.name })}
                            >
                              Permisos
                            </button>
                            <button className="small ghost" onClick={() => rename('folder', f.id, f.name)}>
                              Renombrar
                            </button>
                            <button className="small danger" onClick={() => removeFolder(f)}>
                              Eliminar
                            </button>
                          </>
                        )}
                      </div>
                    </div>
                  ))}

                  {data.files.map((f) => (
                    <FileRow
                      key={f.id}
                      file={f}
                      canManage={data.can_manage || data.can_upload}
                      onShare={() => setShareTarget({ file: f })}
                      onPermissions={() => setPermTarget({ type: 'file', id: f.id, name: f.name })}
                      onRename={() => rename('file', f.id, f.name)}
                      onDelete={() => removeFile(f)}
                    />
                  ))}
                </div>
              )}
            </div>
          </>
        )
      )}

      {newFolder && <NewFolderModal onClose={() => setNewFolder(false)} onCreate={createFolder} />}

      {shareTarget && (
        <ShareDialog
          file={shareTarget.file}
          folder={shareTarget.folder}
          onClose={() => setShareTarget(null)}
        />
      )}

      {permTarget && (
        <PermissionsDialog
          resourceType={permTarget.type}
          resourceId={permTarget.id}
          resourceName={permTarget.name}
          onClose={() => setPermTarget(null)}
        />
      )}
    </div>
  )
}

function FileRow({
  file, canManage, onShare, onPermissions, onRename, onDelete,
}: {
  file: FileItem
  canManage: boolean
  onShare: () => void
  onPermissions: () => void
  onRename: () => void
  onDelete: () => void
}) {
  return (
    <div className="item">
      <span className="icon" aria-hidden>
        {iconFor(file.mime_type)}
      </span>
      <div className="name">
        <b>{file.name}</b>
        <span className="muted">
          {humanBytes(file.size_bytes)}
          {file.version > 1 && ` · v${file.version}`}
          {file.download_count > 0 && ` · ${file.download_count} descargas`}
          {file.owner_email && ` · ${file.owner_email}`}
        </span>
      </div>
      <div className="actions">
        <a className="btn small" href={api.downloadUrl(file.id)}>
          Descargar
        </a>
        <button className="small ghost" onClick={onShare}>
          Compartir
        </button>
        {canManage && (
          <>
            <button className="small ghost" onClick={onPermissions}>
              Permisos
            </button>
            <button className="small ghost" onClick={onRename}>
              Renombrar
            </button>
            <button className="small danger" onClick={onDelete}>
              Eliminar
            </button>
          </>
        )}
      </div>
    </div>
  )
}

function iconFor(mime: string): string {
  if (mime.startsWith('image/')) return '🖼️'
  if (mime.startsWith('video/')) return '🎞️'
  if (mime.startsWith('audio/')) return '🎵'
  if (mime.includes('pdf')) return '📕'
  if (mime.includes('zip') || mime.includes('rar') || mime.includes('7z')) return '🗜️'
  if (mime.includes('sheet') || mime.includes('excel') || mime.includes('csv')) return '📊'
  if (mime.includes('word') || mime.includes('document')) return '📝'
  if (mime.includes('dwg') || mime.includes('dxf')) return '📐'
  return '📄'
}

function NewFolderModal({
  onClose, onCreate,
}: {
  onClose: () => void
  onCreate: (name: string, restricted: boolean) => void
}) {
  const [name, setName] = useState('')
  const [restricted, setRestricted] = useState(false)

  return (
    <Modal
      title="Nueva carpeta"
      onClose={onClose}
      footer={
        <>
          <button onClick={onClose}>Cancelar</button>
          <button className="primary" disabled={!name.trim()} onClick={() => onCreate(name.trim(), restricted)}>
            Crear
          </button>
        </>
      }
    >
      <div className="field">
        <label htmlFor="fname">Nombre</label>
        <input id="fname" value={name} autoFocus onChange={(e) => setName(e.target.value)} />
      </div>
      <label className="row" style={{ gap: '.5rem' }}>
        <input
          type="checkbox"
          style={{ width: 'auto' }}
          checked={restricted}
          onChange={(e) => setRestricted(e.target.checked)}
        />
        <span>
          Carpeta restringida — solo la verá quien reciba permiso explícito
        </span>
      </label>
    </Modal>
  )
}
