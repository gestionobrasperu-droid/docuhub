# Roadmap por fases

Cada fase es entregable por sí sola: al terminarla la plataforma sigue funcionando y aporta valor.
Lo marcado como ✅ ya está escrito en este repositorio; lo marcado como 📋 está diseñado y documentado
para implementarse después.

---

## Fase 0 — Preparación (medio día)

Sin código. Deja el terreno listo.

1. Instalar toolchain en la laptop: Go, Node.js, Docker Desktop, Git, cloudflared
   → `scripts/setup-windows.ps1` lo hace todo con `winget`.
2. Crear el proyecto en Google Cloud, habilitar el API de Drive, crear credenciales OAuth
   → `docs/02-GOOGLE-DRIVE-SETUP.md`.
3. Crear una **Unidad Compartida** (Shared Drive) en el Google Workspace de la empresa llamada
   `DocuHub-Storage`. Si la empresa usa Gmail normal en lugar de Workspace, funciona igual con
   "Mi unidad", pero pierdes la propiedad centralizada de los archivos.
4. Mover el DNS del dominio de la empresa a Cloudflare (gratis) → `docs/01-INFRAESTRUCTURA.md`.
5. Decidir la política: quién es admin, qué cuota por usuario, cuántos días viven los enlaces públicos.

**Criterio de salida:** `docker --version`, `go version` y `cloudflared --version` responden, y tienes
el `client_id` / `client_secret` de Google en la mano.

---

## Fase 1 — Núcleo ✅

Lo mínimo que ya es útil: una plataforma donde entras, subes y bajas con control.

- Autenticación con email + contraseña, hash **Argon2id**, sesiones en cookie `HttpOnly` + `SameSite=Lax`.
- Roles globales: `admin`, `manager`, `member`, `guest`.
- Enlazado de la cuenta de Google por **OAuth 2.0** con `access_type=offline`; el refresh token se
  guarda cifrado con AES-256-GCM. Soporta **varias cuentas** enlazadas a la vez.
- Árbol de carpetas propio (en Postgres) espejado contra carpetas reales de Drive.
- Subida y descarga de archivos; el archivo nunca se escribe en el disco de la laptop, se canaliza.
- `audit_log`: cada acción con actor, IP, user-agent, recurso y metadatos en JSONB.

**Archivos clave:** `backend/internal/httpapi/auth_handlers.go`, `backend/internal/drive/drive.go`,
`backend/internal/store/migrations/0001_init.sql`.

---

## Fase 2 — Control de uso ✅

Aquí es donde "administrar por completo los usos" se vuelve real.

- **Cuota de almacenamiento por usuario** (`users.quota_bytes`) verificada *antes* de abrir la subida.
- **Cuota de ancho de banda mensual** por usuario, contabilizada en `bandwidth_log` en cada descarga.
- Panel de administración: usuarios activos, top de archivos por descargas, bytes por usuario y por mes,
  espacio disponible real en la cuenta de Drive (`/about` del API).
- Rate limiting por IP y por usuario en los endpoints sensibles.
- Bitácora consultable y filtrable desde la interfaz.

**Archivos clave:** `backend/internal/quota/quota.go`, `backend/internal/httpapi/admin_handlers.go`.

---

## Fase 3 — Compartición y permisos ✅

- Permisos granulares por carpeta o archivo, con herencia hacia abajo:
  `viewer` < `downloader` < `editor` < `manager`.
- Sujetos de permiso: usuario individual o grupo.
- **Enlaces públicos** (`/s/{token}`) con: expiración, contraseña opcional, número máximo de descargas,
  y revocación inmediata. El token se guarda **hasheado**, igual que una contraseña.
- Cada apertura de un enlace público queda auditada con IP.

**Archivos clave:** `backend/internal/httpapi/share_handlers.go`, `backend/internal/repo/permissions.go`.

---

## Fase 4 — Archivos pesados ✅ (base) / 📋 (avanzado)

- ✅ **Subida resumible por chunks**: el navegador parte el archivo en trozos de 8 MB y los manda uno a
  uno; el backend los reenvía a la sesión resumible de Google. Si se corta la luz o el internet, la
  subida se reanuda desde el último byte confirmado (`uploads.bytes_received`).
- ✅ Descarga por streaming con soporte de `Range` (permite reanudar descargas y hacer seek en video).
- ✅ Versionado: cada re-subida de un archivo con el mismo nombre crea una fila en `file_versions`.
- 📋 Checksum MD5 comparado contra el que devuelve Drive, para detectar corrupción.
- 📋 Generación de miniaturas y previsualización de PDF/video sin descargar el original completo.
- 📋 Escaneo antivirus con ClamAV en contenedor antes de publicar el archivo.

---

## Fase 5 — Operación 24/7 (scripts listos)

- Publicación con Cloudflare Tunnel y certificado TLS automático.
- `scripts/keep-alive.ps1`: impide la suspensión y mantiene la sesión de Windows viva.
- `scripts/health-check.ps1`: watchdog cada 5 minutos; reinicia contenedores caídos y avisa por correo.
- `scripts/backup.ps1`: `pg_dump` diario cifrado, subido al propio Drive en una carpeta de sistema.
- Arranque automático tras corte de luz (BIOS + Task Scheduler).

Todo el detalle está en [`01-INFRAESTRUCTURA.md`](01-INFRAESTRUCTURA.md) (opciones y decisiones),
[`03-CLOUDFLARE-DOMINIO.md`](03-CLOUDFLARE-DOMINIO.md) (el dominio de la empresa, paso a paso) y
[`04-OPERACION.md`](04-OPERACION.md) (el día a día y la recuperación ante desastre).

---

## Fase 6 — Extras 📋

Orden sugerido según el retorno que dan:

1. **Google Sign-In** para los usuarios internos (quita el manejo de contraseñas).
2. **2FA TOTP** para las cuentas `admin`.
3. **Búsqueda full-text** con `tsvector` de Postgres sobre nombre, etiquetas y contenido extraído.
4. **Webhooks / notificaciones** a Slack o correo cuando entra un documento en una carpeta vigilada.
5. **Pooling multi-cuenta**: cuando una cuenta de Drive llega al 90%, los archivos nuevos van a la
   siguiente cuenta enlazada. La tabla `drive_accounts` ya está diseñada para esto.
6. **Cliente de sincronización de escritorio** (carpeta local ↔ DocuHub) en Go con `fsnotify`.
