// Cliente de la API. Todo pasa por `request`, que unifica el manejo de
// errores: el backend siempre responde { message, code } cuando algo falla.

export interface User {
  id: string
  email: string
  name: string
  role: 'admin' | 'manager' | 'member' | 'guest'
  status: 'active' | 'suspended'
  quota_bytes: number
  bandwidth_bytes: number
  used_bytes: number
  must_change_password: boolean
  created_at: string
  last_login_at?: string
}

export interface Folder {
  id: string
  parent_id?: string
  name: string
  path: string
  is_root: boolean
  restricted: boolean
  created_at: string
}

export interface FileItem {
  id: string
  folder_id: string
  name: string
  mime_type: string
  size_bytes: number
  version: number
  download_count: number
  status: string
  tags: string[]
  description: string
  owner_email?: string
  created_at: string
  updated_at: string
}

export interface FolderPayload {
  folder: Folder
  breadcrumb: Folder[]
  folders: Folder[]
  files: FileItem[]
  level: string
  total_bytes: number
  file_count: number
  can_upload: boolean
  can_manage: boolean
}

export interface SessionInfo {
  user: User
  quota_bytes: number
  bandwidth_bytes: number
  bandwidth_used_month: number
  drive_connected: boolean
  max_chunk_bytes: number
}

export class ApiError extends Error {
  code?: string
  status: number
  payload: any
  constructor(message: string, status: number, code?: string, payload?: any) {
    super(message)
    this.status = status
    this.code = code
    this.payload = payload
  }
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    headers:
      options.body && !(options.body instanceof Blob)
        ? { 'Content-Type': 'application/json', ...(options.headers || {}) }
        : options.headers,
    ...options,
  })

  const text = await res.text()
  const data = text ? safeParse(text) : null

  if (!res.ok) {
    const message = (data && (data.message as string)) || `Error ${res.status}`
    throw new ApiError(message, res.status, data?.code, data)
  }
  return data as T
}

function safeParse(text: string): any {
  try {
    return JSON.parse(text)
  } catch {
    return { message: text }
  }
}

export const api = {
  // ------------------------------------------------------------ sesión --
  me: () => request<SessionInfo>('/api/auth/me'),
  login: (email: string, password: string) =>
    request<{ user: User }>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ email, password }),
    }),
  logout: () => request<{ ok: boolean }>('/api/auth/logout', { method: 'POST' }),
  changePassword: (current_password: string, new_password: string) =>
    request<{ ok: boolean }>('/api/auth/password', {
      method: 'POST',
      body: JSON.stringify({ current_password, new_password }),
    }),

  // ---------------------------------------------------------- carpetas --
  myOverview: () =>
    request<{
      summary: { file_count: number; stored_bytes: number; download_count: number; active_shares: number }
      recent_files: FileItem[]
      usage: { storage_limit: number; storage_used: number; bandwidth_limit: number; bandwidth_used: number }
    }>('/api/me/overview'),

  root: () => request<FolderPayload>('/api/folders/root'),
  folder: (id: string) => request<FolderPayload>(`/api/folders/${id}`),
  createFolder: (parent_id: string | null, name: string, restricted = false) =>
    request<Folder>('/api/folders', {
      method: 'POST',
      body: JSON.stringify({ parent_id, name, restricted }),
    }),
  updateFolder: (id: string, body: { name?: string; restricted?: boolean }) =>
    request<Folder>(`/api/folders/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
  deleteFolder: (id: string) =>
    request<{ ok: boolean }>(`/api/folders/${id}`, { method: 'DELETE' }),

  // ---------------------------------------------------------- archivos --
  updateFile: (id: string, body: { name?: string; description?: string; tags?: string[] }) =>
    request<FileItem>(`/api/files/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
  deleteFile: (id: string) => request<{ ok: boolean }>(`/api/files/${id}`, { method: 'DELETE' }),
  downloadUrl: (id: string) => `/api/files/${id}/download`,
  search: (q: string) => request<{ files: FileItem[] }>(`/api/search?q=${encodeURIComponent(q)}`),

  // --------------------------------------------------------- compartir --
  shares: () => request<{ shares: any[] }>('/api/shares'),
  createShare: (body: {
    file_id?: string
    folder_id?: string
    password?: string
    max_downloads?: number
    expires_in_days?: number
    note?: string
  }) => request<any>('/api/shares', { method: 'POST', body: JSON.stringify(body) }),
  revokeShare: (id: string) => request<{ ok: boolean }>(`/api/shares/${id}`, { method: 'DELETE' }),

  // ---------------------------------------------------------- permisos --
  permissions: (resource_type: string, resource_id: string) =>
    request<{ permissions: any[] }>(
      `/api/permissions?resource_type=${resource_type}&resource_id=${resource_id}`,
    ),
  grantPermission: (body: {
    subject_email: string
    resource_type: string
    resource_id: string
    level: string
  }) => request<any>('/api/permissions', { method: 'POST', body: JSON.stringify(body) }),
  revokePermission: (id: string) =>
    request<{ ok: boolean }>(`/api/permissions/${id}`, { method: 'DELETE' }),

  // ---------------------------------------------------- administración --
  admin: {
    stats: () => request<any>('/api/admin/stats'),
    audit: (params = '') => request<{ entries: any[] }>(`/api/admin/audit?${params}`),
    users: () => request<{ users: User[]; default_quota: number; default_bandwidth: number }>(
      '/api/admin/users',
    ),
    createUser: (body: any) =>
      request<{ user: User; temporary_password?: string }>('/api/admin/users', {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    updateUser: (id: string, body: any) =>
      request<User>(`/api/admin/users/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
    resetPassword: (id: string) =>
      request<{ temporary_password: string }>(`/api/admin/users/${id}/password`, { method: 'POST' }),
    deleteUser: (id: string) =>
      request<{ ok: boolean }>(`/api/admin/users/${id}`, { method: 'DELETE' }),

    drive: () => request<any>('/api/admin/drive/'),
    driveConnect: () => request<{ auth_url: string }>('/api/admin/drive/connect'),
    driveSetPrimary: (id: string) =>
      request<{ ok: boolean }>(`/api/admin/drive/${id}/primary`, { method: 'POST' }),
    driveRefresh: (id: string) =>
      request<any>(`/api/admin/drive/${id}/refresh`, { method: 'POST' }),
    driveSync: (id: string) =>
      request<{
        folders_created: number
        files_imported: number
        files_skipped: number
        bytes_imported: number
        truncated: boolean
        warnings: string[]
      }>(`/api/admin/drive/${id}/sync`, { method: 'POST' }),
    driveDisconnect: (id: string) =>
      request<{ ok: boolean }>(`/api/admin/drive/${id}`, { method: 'DELETE' }),
  },

  // ---------------------------------------------------- enlaces públicos --
  publicInfo: (token: string) => request<any>(`/api/public/${token}`),
  publicUnlock: (token: string, password: string) =>
    request<{ unlocked: boolean; grant?: string }>(`/api/public/${token}/unlock`, {
      method: 'POST',
      body: JSON.stringify({ password }),
    }),
  publicDownloadUrl: (token: string, fileId?: string, grant?: string) => {
    const params = new URLSearchParams()
    if (fileId) params.set('file_id', fileId)
    if (grant) params.set('grant', grant)
    const qs = params.toString()
    return `/api/public/${token}/download${qs ? '?' + qs : ''}`
  },
}

// ------------------------------------------------------------- subidas ----

export interface UploadProgress {
  fileName: string
  sent: number
  total: number
  percent: number
  status: 'preparando' | 'subiendo' | 'listo' | 'error' | 'cancelado'
  message?: string
}

/**
 * Sube un archivo por trozos.
 *
 * El navegador corta el archivo con Blob.slice, así que nunca se carga
 * completo en memoria: un archivo de 20 GB usa tanta RAM como uno de 20 MB.
 * Cada trozo se confirma contra Drive antes de mandar el siguiente, y si uno
 * falla se reintenta preguntando al servidor por dónde iba.
 */
export async function uploadFile(
  file: File,
  folderId: string,
  onProgress: (p: UploadProgress) => void,
  signal?: AbortSignal,
): Promise<FileItem> {
  onProgress({ fileName: file.name, sent: 0, total: file.size, percent: 0, status: 'preparando' })

  const init = await request<{ upload_id: string; chunk_size: number }>('/api/uploads', {
    method: 'POST',
    body: JSON.stringify({
      folder_id: folderId,
      name: file.name,
      size_bytes: file.size,
      mime_type: file.type || 'application/octet-stream',
    }),
  })

  const uploadId = init.upload_id
  const chunkSize = init.chunk_size
  let offset = 0

  while (offset < file.size) {
    if (signal?.aborted) {
      await fetch(`/api/uploads/${uploadId}`, { method: 'DELETE', credentials: 'same-origin' })
      onProgress({
        fileName: file.name, sent: offset, total: file.size,
        percent: Math.round((offset / file.size) * 100), status: 'cancelado',
      })
      throw new ApiError('Subida cancelada', 0, 'aborted')
    }

    const end = Math.min(offset + chunkSize, file.size)
    const chunk = file.slice(offset, end)

    let attempt = 0
    for (;;) {
      try {
        const res = await fetch(`/api/uploads/${uploadId}/chunk?offset=${offset}`, {
          method: 'PUT',
          credentials: 'same-origin',
          body: chunk,
          signal,
        })
        const data = await res.json().catch(() => ({}))

        if (res.status === 409 && typeof data.bytes_received === 'number') {
          // El servidor sabe mejor que nosotros dónde quedó la subida.
          offset = data.bytes_received
          break
        }
        if (!res.ok) throw new ApiError(data.message || `Error ${res.status}`, res.status, data.code)

        offset = data.bytes_received ?? end
        if (data.complete) {
          onProgress({
            fileName: file.name, sent: file.size, total: file.size,
            percent: 100, status: 'listo',
          })
          return data.file as FileItem
        }
        break
      } catch (err) {
        attempt++
        const apiErr = err as ApiError
        // Un error de permisos o de cuota no mejora reintentando.
        if (apiErr.status >= 400 && apiErr.status < 500 && apiErr.status !== 429) throw err
        if (attempt >= 3) throw err
        await new Promise((r) => setTimeout(r, attempt * 2000))
      }
    }

    onProgress({
      fileName: file.name, sent: offset, total: file.size,
      percent: Math.round((offset / file.size) * 100), status: 'subiendo',
    })
  }

  // Tamaño cero o el servidor cerró la subida en el último trozo.
  const status = await request<any>(`/api/uploads/${uploadId}`)
  if (status.complete && status.file) return status.file as FileItem
  throw new ApiError('La subida terminó pero el servidor no la confirmó', 500)
}

// --------------------------------------------------------------- utilidades

export function humanBytes(bytes: number): string {
  if (!bytes || bytes < 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let i = 0
  let n = bytes
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

export function formatDate(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return d.toLocaleString('es-PE', { dateStyle: 'short', timeStyle: 'short' })
}

// Concordancia de número. Un "1 subidas hoy" en un panel de dirección resta
// más credibilidad de lo que parece.
export function plural(n: number, singular: string, plural: string): string {
  return `${n} ${n === 1 ? singular : plural}`
}

// ------------------------------------------------- equipos como unidad ----

export interface Device {
  id: string
  device_name: string
  created_at: string
  last_used_at?: string
  last_ip?: string
}

export const devices = {
  list: () => request<{ devices: Device[] }>('/api/me/equipos'),
  revoke: (id: string) => request<{ ok: boolean }>(`/api/me/equipos/${id}`, { method: 'DELETE' }),
  // La descarga va por navegación directa: el servidor responde con
  // Content-Disposition y el navegador guarda el archivo.
  installerUrl: (equipo: string, letra: string, formato: 'bat' | 'ps1' = 'bat') =>
    `/api/me/conectar-pc?equipo=${encodeURIComponent(equipo)}&letra=${letra}&formato=${formato}`,

  // Un administrador puede prepararlo para otra persona y mandárselo hecho,
  // en lugar de pedirle que entre a la plataforma a descargárselo.
  installerForUser: (userId: string, equipo: string, letra: string) =>
    `/api/admin/users/${userId}/instalador?equipo=${encodeURIComponent(equipo)}&letra=${letra}`,
}
