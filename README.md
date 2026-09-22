# DocuHub — Plataforma de gestión documental sobre Google Drive

Plataforma privada de la empresa para **almacenar, compartir y auditar documentos pesados** (planos, videos,
expedientes, CAD, backups) usando **Google Drive como almacén** y una aplicación propia como **capa de control
total**: quién sube, quién descarga, cuánto, cuándo, desde dónde, y por cuánto tiempo.

La idea central: Google Drive guarda los bytes; DocuHub manda. Los usuarios **nunca** tocan el Drive
directamente ni reciben credenciales de Google. Todo pasa por la plataforma, que registra y limita cada acción.

---

## 1. Por qué esta arquitectura

| Necesidad | Solución |
|---|---|
| Sin servidor / sin VPS | Todo corre en una laptop Windows con Docker; se publica al mundo con **Cloudflare Tunnel** (gratis, sin IP pública, sin abrir puertos del router) |
| Dominio propio de la empresa | DNS en Cloudflare → `docs.tuempresa.com` apunta al túnel |
| Archivos pesados | Subida **resumible por chunks** directo al API de Drive; descarga por **streaming** (la laptop no almacena el archivo, solo lo canaliza) |
| Espacio "infinito" | El almacenamiento es la cuota de Drive de la empresa; se pueden **enlazar varias cuentas** y repartir la carga |
| Control total de usos | Cada byte que entra o sale queda en `bandwidth_log` + `audit_log`; cuotas por usuario, expiración de enlaces, límites de descarga |
| Bajo consumo de recursos | Backend en **Go**: un binario, ~30–60 MB de RAM en reposo |

### Stack elegido

Me pediste elegir entre Go y Python: **Go** para el backend. Razón concreta: el cuello de botella de esta
plataforma es mover archivos de varios GB entre el navegador y Google Drive de forma concurrente. Go hace eso con
`io.Copy` sobre goroutines usando memoria constante (~64 KB por transferencia), mientras que un equivalente en
Python/Django necesitaría workers ASGI + tuning para no comerse la RAM de la laptop. Además compila a un
`.exe` único, lo que simplifica correrlo como servicio de Windows 24/7.

| Capa | Tecnología |
|---|---|
| Backend | Go 1.22 + chi (router) — REST API, cliente propio del API de Drive v3 |
| Base de datos | PostgreSQL 16 (Docker) |
| Frontend | React 18 + TypeScript + Vite (build estático embebido en el binario Go) |
| Reverse proxy / TLS | Caddy (certificados automáticos) |
| Publicación a internet | Cloudflare Tunnel (`cloudflared`) |
| Almacenamiento | Google Drive API v3 (Unidad Compartida) |
| Auth | Sesiones con cookie `HttpOnly` + Argon2id; opcional Google Sign-In (Fase 6) |

---

## 2. Mapa del repositorio

```
backend/         API en Go (el corazón)
  cmd/server/          main.go, arranque y apagado limpio
  internal/config/     configuración por variables de entorno
  internal/crypto/     Argon2id, AES-GCM para tokens de Google, tokens de sesión
  internal/store/      pool de Postgres + migraciones SQL embebidas
  internal/models/     structs del dominio
  internal/repo/       acceso a datos (users, files, shares, uploads, audit)
  internal/drive/      cliente del API de Drive (OAuth, resumable upload, streaming)
  internal/httpapi/    handlers HTTP, middleware, RBAC
  internal/quota/      cuotas de almacenamiento y ancho de banda
  web/                 embed del frontend compilado
frontend/        SPA en React + TypeScript
deploy/          docker-compose, Caddyfile, config de cloudflared, .env.example
scripts/         PowerShell: setup, mantener la laptop viva, backup, watchdog
docs/            Roadmap, infraestructura 24/7, configuración de Google, operación
```

---

## 3. Fases del proyecto

Detalle completo en [`docs/00-ROADMAP.md`](docs/00-ROADMAP.md). Resumen:

| Fase | Qué entrega | Estado |
|---|---|---|
| **0** | Preparación: toolchain, proyecto en Google Cloud, dominio en Cloudflare, Unidad Compartida | Documentada |
| **1** | Núcleo: login, usuarios, roles, enlazar cuenta de Drive, carpetas, subir/descargar, auditoría | **Implementada** |
| **2** | Control de uso: cuotas, límites de ancho de banda, estadísticas, panel de administración | **Implementada** |
| **3** | Compartición: permisos por carpeta/archivo, grupos, enlaces públicos con expiración y contraseña | **Implementada** |
| **4** | Archivos pesados: chunks resumibles, reanudación, versiones, checksums, previsualización | **Implementada (base)** |
| **5** | Operación 24/7: túnel, TLS, backups automáticos, watchdog, alertas | Scripts listos |
| **6** | Extras: SSO Workspace, 2FA, búsqueda full-text, webhooks, cliente de sincronización | Diseñada |

---

## 4. Arranque rápido

Requisitos (ninguno está instalado en esta laptop todavía — el script los instala):

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\setup-windows.ps1
```

Luego:

1. Sigue [`docs/02-GOOGLE-DRIVE-SETUP.md`](docs/02-GOOGLE-DRIVE-SETUP.md) para obtener `GOOGLE_CLIENT_ID` y `GOOGLE_CLIENT_SECRET`.
2. Copia `deploy/.env.example` a `deploy/.env` y complétalo (el script genera las claves secretas).
3. Levanta todo:

```bash
cd deploy && docker compose up -d --build
```

4. Abre `http://localhost:8080`, entra con el usuario administrador inicial que imprime el log del backend,
   ve a **Administración → Google Drive → Conectar cuenta** y autoriza la cuenta de la empresa.
5. Para publicarlo en `docs.constructorapesam.com`, sigue
   [`docs/03-CLOUDFLARE-DOMINIO.md`](docs/03-CLOUDFLARE-DOMINIO.md) — guía concreta para este dominio,
   con el estado real de su DNS. La discusión general de alternativas está en
   [`docs/01-INFRAESTRUCTURA.md`](docs/01-INFRAESTRUCTURA.md).

> **¿Primera vez?** Ve directo a [`docs/05-PRUEBA-RAPIDA.md`](docs/05-PRUEBA-RAPIDA.md): es el guion
> de 20 minutos para vincular la cuenta de Google, subir un archivo pesado, dar acceso a un compañero
> y mandar un enlace a alguien de fuera, todo en local antes de tocar el dominio.

Para dejar la laptop trabajando sola (arranque automático, watchdog cada 5 minutos y respaldo
diario), ejecuta como administrador:

```powershell
.\scripts\keep-alive.ps1
.\scripts\install-tasks.ps1
```

El día a día —qué mirar, qué hacer cuando algo falla, cómo recuperarse de un desastre— está en
[`docs/04-OPERACION.md`](docs/04-OPERACION.md).

---

## 5. Modelo de seguridad en una línea

El refresh token de Google se guarda **cifrado con AES-256-GCM** en la base de datos; los usuarios de la
plataforma jamás reciben un enlace de Drive, solo URLs de DocuHub que se validan contra permisos, cuota y
expiración en cada petición.
