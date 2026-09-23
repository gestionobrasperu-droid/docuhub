import { DragEvent, FormEvent, useCallback, useEffect, useRef, useState } from 'react'
import {
  api, ApiError, FileItem, Folder, FolderPayload, formatDate, humanBytes, plural, SessionInfo,
  uploadFile, UploadProgress,
} from '../api'
import Modal from '../components/Modal'
import ShareDialog from '../components/ShareDialog'
import PermissionsDialog from '../components/PermissionsDialog'
import { iconFor } from '../components/fileIcon'

interface Props {
  folderId: string | null
  session: SessionInfo
  navigate: (to: string) => void
}

const NIVELES: Record<string, string> = {
  viewer: 'Solo lectura',
  downloader: 'Puedes descargar',
  editor: 'Puedes subir y editar',
  manager: 'Eres responsable',
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

  /* ---------------------------------------------------------- subidas -- */

  const startUploads = useCallback(
    async (files: FileList | File[]) => {
      if (!data) return
      for (const file of Array.from(files)) {
        const update = (p: UploadProgress) =>
          setUploads((prev) => [...prev.filter((u) => u.fileName !== p.fileName), p])

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
      // Las terminadas se retiran solas; los errores se quedan para leerlos.
      setTimeout(() => setUploads((prev) => prev.filter((u) => u.status === 'error')), 6000)
    },
    [data, load],
  )

  function onDrop(e: DragEvent) {
    e.preventDefault()
    setDragging(false)
    if (e.dataTransfer.files.length) void startUploads(e.dataTransfer.files)
  }

  /* --------------------------------------------------------- acciones -- */

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
    if (!confirm(`¿Eliminar "${file.name}"?\n\nVa a la papelera de Google Drive, donde se puede recuperar durante 30 días.`))
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
      setResults((await api.search(search.trim())).files)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'La búsqueda falló')
    }
  }

  /* ------------------------------------------------------------ vista -- */

  if (loading && !data) return <div className="muted">Cargando…</div>

  const subiendo = uploads.filter((u) => u.status === 'subiendo' || u.status === 'preparando')

  return (
    <div
      className={dragging ? 'drag-overlay' : ''}
      onDragOver={(e) => {
        e.preventDefault()
        if (data?.can_upload) setDragging(true)
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={onDrop}
    >
      <div className="page-head row between">
        <form className="search" onSubmit={runSearch} style={{ flex: '0 1 420px' }}>
          <span className="icon" aria-hidden>
            🔍
          </span>
          <input
            placeholder="Buscar en toda la plataforma…"
            value={search}
            onChange={(e) => {
              setSearch(e.target.value)
              if (!e.target.value) setResults(null)
            }}
          />
        </form>

        {data?.can_upload && (
          <div className="row">
            <button onClick={() => setNewFolder(true)}>📁 Nueva carpeta</button>
            <button className="primary" onClick={() => fileInput.current?.click()}>
              ⬆ Subir archivos
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

      {error && (
        <div className="alert error" onClick={() => setError('')}>
          <span className="ico">⚠️</span>
          <span>{error}</span>
        </div>
      )}

      {uploads.length > 0 && (
        <div className="card">
          <div className="card-head">
            <h2>Subidas {subiendo.length > 0 && <span className="dim">({subiendo.length} en curso)</span>}</h2>
          </div>
          <div className="card-body stack">
            {uploads.map((u) => (
              <div key={u.fileName}>
                <div className="row between" style={{ marginBottom: '.25rem' }}>
                  <span className="truncate" style={{ fontSize: '.85rem', maxWidth: '60%' }}>
                    {u.fileName}
                  </span>
                  <span className="dim">
                    {u.status === 'error'
                      ? u.message
                      : u.status === 'listo'
                        ? '✓ Completado'
                        : `${u.percent}% · ${humanBytes(u.sent)} de ${humanBytes(u.total)}`}
                  </span>
                </div>
                <div className="progress">
                  <div
                    className={u.status === 'error' ? 'err' : u.status === 'listo' ? 'ok' : ''}
                    style={{ width: `${u.status === 'error' ? 100 : u.percent}%` }}
                  />
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {results ? (
        <div className="card">
          <div className="card-head">
            <h2>Resultados de «{search}»</h2>
            <div className="spacer" />
            <span className="dim">{results.length} archivos</span>
            <button className="ghost sm" onClick={() => setResults(null)}>
              Volver a la carpeta
            </button>
          </div>
          {results.length === 0 ? (
            <div className="empty">Sin coincidencias.</div>
          ) : (
            results.map((f) => (
              <FileRow
                key={f.id}
                file={f}
                canEdit={false}
                onOpenFolder={() => navigate(`/f/${f.folder_id}`)}
                onShare={() => setShareTarget({ file: f })}
                onPermissions={() => setPermTarget({ type: 'file', id: f.id, name: f.name })}
                onRename={() => rename('file', f.id, f.name)}
                onDelete={() => removeFile(f)}
              />
            ))
          )}
        </div>
      ) : (
        data && (
          <>
            <div className="row between" style={{ marginBottom: '.8rem' }}>
              <nav className="breadcrumb">
                {data.breadcrumb.map((f) => (
                  <span key={f.id}>
                    <button onClick={() => navigate(f.is_root ? '/archivos' : `/f/${f.id}`)}>{f.name}</button>
                    <span className="sep">/</span>
                  </span>
                ))}
                <b>{data.folder.name}</b>
                {data.folder.restricted && (
                  <span className="badge warn" title="Solo entra quien tenga permiso explícito">
                    🔒 restringida
                  </span>
                )}
              </nav>

              <div className="row">
                <span className="dim">
                  {plural(data.file_count, "archivo", "archivos")} · {humanBytes(data.total_bytes)}
                </span>
                <span className="badge" title="Tu nivel de acceso en esta carpeta">
                  {NIVELES[data.level] ?? data.level}
                </span>
              </div>
            </div>

            <div className="card">
              {data.folders.length === 0 && data.files.length === 0 ? (
                <div className="card-body">
                  <div className={`dropzone${dragging ? ' active' : ''}`}>
                    {data.can_upload ? (
                      <>
                        <div style={{ fontSize: '1.6rem', marginBottom: '.4rem' }}>⬆</div>
                        Arrastra archivos aquí, o usa <b>Subir archivos</b>
                        <div className="dim" style={{ marginTop: '.4rem' }}>
                          Se trocean solos: un archivo de 20 GB se sube igual que uno de 20 MB
                        </div>
                      </>
                    ) : (
                      'Esta carpeta está vacía'
                    )}
                  </div>
                </div>
              ) : (
                <>
                  {data.folders.map((f) => (
                    <div className="item" key={f.id}>
                      <span className="icon" aria-hidden>
                        {f.restricted ? '🔒' : '📁'}
                      </span>
                      <div className="name">
                        <b>
                          <a
                            href={`#/f/${f.id}`}
                            onClick={(e) => {
                              e.preventDefault()
                              navigate(`/f/${f.id}`)
                            }}
                          >
                            {f.name}
                          </a>
                        </b>
                        <span>Carpeta{f.restricted ? ' restringida' : ''} · {formatDate(f.created_at)}</span>
                      </div>
                      <div className="actions">
                        <button className="ghost sm" onClick={() => setShareTarget({ folder: f })}>
                          Compartir
                        </button>
                        {data.can_manage && (
                          <>
                            <button
                              className="ghost sm"
                              onClick={() => setPermTarget({ type: 'folder', id: f.id, name: f.name })}
                            >
                              Permisos
                            </button>
                            <button className="ghost sm" onClick={() => rename('folder', f.id, f.name)}>
                              Renombrar
                            </button>
                            <button className="danger sm" onClick={() => removeFolder(f)}>
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
                      canEdit={data.can_upload}
                      canManage={data.can_manage}
                      onShare={() => setShareTarget({ file: f })}
                      onPermissions={() => setPermTarget({ type: 'file', id: f.id, name: f.name })}
                      onRename={() => rename('file', f.id, f.name)}
                      onDelete={() => removeFile(f)}
                    />
                  ))}
                </>
              )}
            </div>
          </>
        )
      )}

      {newFolder && <NewFolderModal onClose={() => setNewFolder(false)} onCreate={createFolder} />}

      {shareTarget && (
        <ShareDialog file={shareTarget.file} folder={shareTarget.folder} onClose={() => setShareTarget(null)} />
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
  file, canEdit, canManage, onShare, onPermissions, onRename, onDelete, onOpenFolder,
}: {
  file: FileItem
  canEdit: boolean
  canManage?: boolean
  onShare: () => void
  onPermissions: () => void
  onRename: () => void
  onDelete: () => void
  onOpenFolder?: () => void
}) {
  return (
    <div className="item">
      <span className="icon" aria-hidden>
        {iconFor(file.mime_type)}
      </span>
      <div className="name">
        <b title={file.name}>{file.name}</b>
        <span>
          {humanBytes(file.size_bytes)}
          {file.version > 1 && ` · versión ${file.version}`}
          {file.download_count > 0 && ` · ${plural(file.download_count, "descarga", "descargas")}`}
          {file.owner_email && ` · ${file.owner_email.split('@')[0]}`}
          {` · ${formatDate(file.updated_at)}`}
        </span>
      </div>
      <div className="actions">
        {onOpenFolder && (
          <button className="ghost sm" onClick={onOpenFolder}>
            Ir a la carpeta
          </button>
        )}
        <a className="btn sm" href={api.downloadUrl(file.id)}>
          ⬇ Descargar
        </a>
        <button className="ghost sm" onClick={onShare}>
          Compartir
        </button>
        {canEdit && (
          <>
            {canManage && (
              <button className="ghost sm" onClick={onPermissions}>
                Permisos
              </button>
            )}
            <button className="ghost sm" onClick={onRename}>
              Renombrar
            </button>
            <button className="danger sm" onClick={onDelete}>
              Eliminar
            </button>
          </>
        )}
      </div>
    </div>
  )
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
            Crear carpeta
          </button>
        </>
      }
    >
      <div className="field">
        <label htmlFor="fname">Nombre</label>
        <input
          id="fname"
          value={name}
          autoFocus
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && name.trim() && onCreate(name.trim(), restricted)}
          placeholder="Ej.: Expediente técnico 2026"
        />
      </div>

      <label className="check">
        <input type="checkbox" checked={restricted} onChange={(e) => setRestricted(e.target.checked)} />
        <span>
          <b>Carpeta restringida</b>
          <div className="help">
            Solo la verá quien reciba permiso explícito. Para el resto de la empresa será como si no
            existiera, aunque tengan rol de miembro.
          </div>
        </span>
      </label>
    </Modal>
  )
}
