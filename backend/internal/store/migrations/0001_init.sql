-- DocuHub — esquema inicial.
-- Principio de diseño: Drive guarda los bytes, Postgres guarda la verdad sobre
-- quién puede hacer qué con esos bytes y qué se hizo realmente.

-- ---------------------------------------------------------------- usuarios --

CREATE TABLE IF NOT EXISTS users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email           TEXT NOT NULL UNIQUE,
    name            TEXT NOT NULL DEFAULT '',
    password_hash   TEXT NOT NULL,
    -- admin: todo. manager: gestiona carpetas y usuarios no-admin.
    -- member: sube y descarga donde tenga permiso. guest: solo lectura.
    role            TEXT NOT NULL DEFAULT 'member'
                    CHECK (role IN ('admin','manager','member','guest')),
    status          TEXT NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active','suspended')),
    quota_bytes     BIGINT NOT NULL DEFAULT 0,  -- 0 = hereda el valor por defecto del sistema
    bandwidth_bytes BIGINT NOT NULL DEFAULT 0,  -- tope mensual de descarga; 0 = valor por defecto
    used_bytes      BIGINT NOT NULL DEFAULT 0,  -- espacio ocupado por sus archivos vivos
    must_change_pw  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at   TIMESTAMPTZ,
    disabled_at     TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS groups (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS group_members (
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);

CREATE TABLE IF NOT EXISTS sessions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,   -- SHA-256 del token que viaja en la cookie
    ip          TEXT NOT NULL DEFAULT '',
    user_agent  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

-- ------------------------------------------------- cuentas de Google Drive --

-- Varias filas = varias cuentas de Drive enlazadas. Permite repartir la carga
-- cuando una cuenta se queda sin espacio (Fase 6).
CREATE TABLE IF NOT EXISTS drive_accounts (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email              TEXT NOT NULL,
    display_name       TEXT NOT NULL DEFAULT '',
    refresh_token_enc  TEXT NOT NULL,          -- AES-256-GCM
    access_token_enc   TEXT NOT NULL DEFAULT '',
    access_expires_at  TIMESTAMPTZ,
    drive_id           TEXT NOT NULL DEFAULT '', -- ID de la Unidad Compartida, si aplica
    root_folder_id     TEXT NOT NULL DEFAULT '', -- carpeta raíz de DocuHub dentro de Drive
    quota_total_bytes  BIGINT NOT NULL DEFAULT 0,
    quota_used_bytes   BIGINT NOT NULL DEFAULT 0,
    quota_checked_at   TIMESTAMPTZ,
    is_primary         BOOLEAN NOT NULL DEFAULT FALSE,
    status             TEXT NOT NULL DEFAULT 'active'
                       CHECK (status IN ('active','error','disabled')),
    last_error         TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_drive_accounts_email ON drive_accounts(lower(email));

-- ------------------------------------------------------ carpetas y archivos --

CREATE TABLE IF NOT EXISTS folders (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id       UUID REFERENCES folders(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    -- path materializado: acelera la resolución de permisos heredados.
    path            TEXT NOT NULL DEFAULT '/',
    drive_folder_id TEXT NOT NULL DEFAULT '',
    drive_account_id UUID REFERENCES drive_accounts(id) ON DELETE SET NULL,
    owner_id        UUID REFERENCES users(id) ON DELETE SET NULL,
    is_root         BOOLEAN NOT NULL DEFAULT FALSE,
    -- restricted = TRUE corta el acceso por rol: dentro de esta carpeta (y de
    -- todo lo que cuelgue de ella) solo entra quien tenga permiso explícito.
    restricted      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_folders_parent ON folders(parent_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_folders_unique_name
    ON folders(parent_id, lower(name)) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS files (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    folder_id        UUID NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    mime_type        TEXT NOT NULL DEFAULT 'application/octet-stream',
    size_bytes       BIGINT NOT NULL DEFAULT 0,
    md5_checksum     TEXT NOT NULL DEFAULT '',
    drive_file_id    TEXT NOT NULL DEFAULT '',
    drive_account_id UUID REFERENCES drive_accounts(id) ON DELETE SET NULL,
    owner_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    version          INTEGER NOT NULL DEFAULT 1,
    download_count   BIGINT NOT NULL DEFAULT 0,
    status           TEXT NOT NULL DEFAULT 'ready'
                     CHECK (status IN ('uploading','ready','failed','quarantined')),
    tags             TEXT[] NOT NULL DEFAULT '{}',
    description      TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_access_at   TIMESTAMPTZ,
    deleted_at       TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_files_folder ON files(folder_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_files_owner ON files(owner_id);
CREATE INDEX IF NOT EXISTS idx_files_name_trgm ON files(lower(name));

CREATE TABLE IF NOT EXISTS file_versions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id       UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    version       INTEGER NOT NULL,
    drive_file_id TEXT NOT NULL,
    size_bytes    BIGINT NOT NULL DEFAULT 0,
    md5_checksum  TEXT NOT NULL DEFAULT '',
    created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (file_id, version)
);

-- ------------------------------------------------------------- permisos ----

-- Un permiso ata un sujeto (usuario o grupo) a un recurso (carpeta o archivo).
-- Los permisos de carpeta se heredan hacia abajo por el path materializado.
CREATE TABLE IF NOT EXISTS permissions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_type  TEXT NOT NULL CHECK (subject_type IN ('user','group')),
    subject_id    UUID NOT NULL,
    resource_type TEXT NOT NULL CHECK (resource_type IN ('folder','file')),
    resource_id   UUID NOT NULL,
    level         TEXT NOT NULL CHECK (level IN ('viewer','downloader','editor','manager')),
    granted_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ,
    UNIQUE (subject_type, subject_id, resource_type, resource_id)
);
CREATE INDEX IF NOT EXISTS idx_permissions_subject ON permissions(subject_type, subject_id);
CREATE INDEX IF NOT EXISTS idx_permissions_resource ON permissions(resource_type, resource_id);

-- --------------------------------------------------------- enlaces públicos --

CREATE TABLE IF NOT EXISTS share_links (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash     TEXT NOT NULL UNIQUE,
    file_id        UUID REFERENCES files(id) ON DELETE CASCADE,
    folder_id      UUID REFERENCES folders(id) ON DELETE CASCADE,
    created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    password_hash  TEXT NOT NULL DEFAULT '',
    max_downloads  INTEGER NOT NULL DEFAULT 0,  -- 0 = sin límite
    download_count INTEGER NOT NULL DEFAULT 0,
    allow_upload   BOOLEAN NOT NULL DEFAULT FALSE,
    note           TEXT NOT NULL DEFAULT '',
    expires_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at   TIMESTAMPTZ,
    revoked_at     TIMESTAMPTZ,
    CHECK (file_id IS NOT NULL OR folder_id IS NOT NULL)
);

-- ------------------------------------------------- subidas reanudables -----

CREATE TABLE IF NOT EXISTS uploads (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    folder_id        UUID NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    file_id          UUID REFERENCES files(id) ON DELETE SET NULL,
    drive_account_id UUID REFERENCES drive_accounts(id) ON DELETE SET NULL,
    file_name        TEXT NOT NULL,
    mime_type        TEXT NOT NULL DEFAULT 'application/octet-stream',
    size_bytes       BIGINT NOT NULL,
    bytes_received   BIGINT NOT NULL DEFAULT 0,
    -- URL de la sesión resumible de Google: es un secreto, va cifrada.
    session_url_enc  TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'active'
                     CHECK (status IN ('active','completed','failed','aborted')),
    error            TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_uploads_user ON uploads(user_id, status);

-- ------------------------------------------------------ auditoría y uso ----

CREATE TABLE IF NOT EXISTS audit_log (
    id            BIGSERIAL PRIMARY KEY,
    actor_id      UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_email   TEXT NOT NULL DEFAULT '',  -- desnormalizado: sobrevive al borrado del usuario
    action        TEXT NOT NULL,
    resource_type TEXT NOT NULL DEFAULT '',
    resource_id   TEXT NOT NULL DEFAULT '',
    resource_name TEXT NOT NULL DEFAULT '',
    ip            TEXT NOT NULL DEFAULT '',
    user_agent    TEXT NOT NULL DEFAULT '',
    success       BOOLEAN NOT NULL DEFAULT TRUE,
    metadata      JSONB NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_log(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_actor ON audit_log(actor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_log(action, created_at DESC);

-- Contabilidad de bytes: alimenta las cuotas y los informes de consumo.
CREATE TABLE IF NOT EXISTS bandwidth_log (
    id         BIGSERIAL PRIMARY KEY,
    user_id    UUID REFERENCES users(id) ON DELETE SET NULL,
    file_id    UUID REFERENCES files(id) ON DELETE SET NULL,
    share_id   UUID REFERENCES share_links(id) ON DELETE SET NULL,
    direction  TEXT NOT NULL CHECK (direction IN ('upload','download')),
    bytes      BIGINT NOT NULL,
    ip         TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_bandwidth_user_date ON bandwidth_log(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value      JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
