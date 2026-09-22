# Configurar Google Drive como almacén

Toma unos 20 minutos, una sola vez. Al terminar tendrás `GOOGLE_CLIENT_ID` y `GOOGLE_CLIENT_SECRET`
para pegar en `deploy/.env`, y la plataforma podrá escribir en el Drive de la empresa.

---

## 0. Tu caso concreto: `gestion.obrasperu@gmail.com`

Vas a usar una cuenta **Gmail normal** (no Google Workspace) y quieres seguir entrando al Drive por
tu cuenta para subir cosas a mano. Eso está previsto, pero hay tres cosas que debes saber antes de
empezar, porque cambian decisiones:

**1. Tienes 15 GB, compartidos con Gmail y Google Fotos.**
Ese es el techo real de la plataforma. Compruébalo en <https://one.google.com/storage>. Cuando te
acerques, las salidas son: enlazar una segunda cuenta de Google (la plataforma admite varias, botón
*Conectar cuenta*), o pasar a Google One / Workspace.

**2. Publica la aplicación en Google Cloud, no la dejes en modo «Prueba».**
Esto es lo importante. En el paso 3 vas a crear la pantalla de consentimiento como **Externa**. Si
la dejas en estado *Prueba/Testing*, **Google caduca el permiso cada 7 días** y tendrías que volver
a conectar la cuenta cada semana. Para evitarlo, en *Pantalla de consentimiento de OAuth* pulsa
**PUBLICAR APLICACIÓN** y confirma. Pasa al estado *En producción*.

Al conectar verás una advertencia de «Google no ha verificado esta aplicación» → **Configuración
avanzada → Ir a DocuHub (no seguro)**. Es esperado y correcto: la aplicación es tuya, corre en tu
laptop y nadie más la usa. La verificación formal de Google solo hace falta para distribuir una app
a terceros; aquí el único que autoriza eres tú, con tu propia cuenta.

**3. Como abrirás el Drive a mano, usa el permiso completo.**
Deja `DRIVE_SCOPE=https://www.googleapis.com/auth/drive` en el `.env` (es el valor por defecto).
El permiso reducido `drive.file` solo dejaría a la plataforma ver los archivos que ella misma creó:
lo que tú arrastraras desde drive.google.com sería invisible para DocuHub.

Con el permiso completo, después de subir algo a mano entras a **Administración → Google Drive →
Escanear Drive** y esos archivos quedan registrados en la plataforma, con sus permisos, cuotas y
auditoría, igual que si los hubieras subido desde la web.

> Trabaja siempre dentro de la carpeta **DocuHub** que la plataforma crea en tu Drive. Lo que dejes
> fuera de ella no se escanea.

Y como `DRIVE_ID` es solo para Unidades Compartidas de Workspace, en tu caso **déjalo vacío**.

---

## 1. Elegir el modo de enlace

| Modo | Cuándo usarlo | Cómo se ve |
|---|---|---|
| **OAuth de una cuenta** ⭐ | Lo que pediste: enlazar *una cuenta* y que todo viva ahí. Funciona con Gmail normal o con Workspace | Un administrador entra una vez a `Administración → Google Drive → Conectar` y autoriza |
| Cuenta de servicio | Solo Google Workspace, para que nadie "sea dueño" personal de los archivos | Se sube un JSON de credenciales; requiere delegación a nivel de dominio |

Este repositorio implementa el **modo OAuth**. La cuenta que autorices será la propietaria de todos los
archivos, así que usa una cuenta institucional (`sistemas@tuempresa.com`), nunca la personal de alguien.

> Consejo fuerte: si la empresa tiene Google Workspace, crea primero una **Unidad Compartida**
> (`Drive → Unidades compartidas → Nueva`) llamada `DocuHub-Storage` y da acceso de Administrador de
> contenido a la cuenta que vas a enlazar. Así los archivos pertenecen a la organización y no se pierden
> si esa cuenta se elimina. Pon el ID de la unidad en `DRIVE_ID` dentro de `.env`.

---

## 2. Crear el proyecto en Google Cloud

1. Entra a <https://console.cloud.google.com/> con la cuenta de la empresa.
2. Arriba a la izquierda: **Seleccionar proyecto → Proyecto nuevo**. Nombre: `DocuHub`. Crear.
3. Con el proyecto `DocuHub` seleccionado, ve a **APIs y servicios → Biblioteca**.
4. Busca **Google Drive API** → **Habilitar**.

## 3. Pantalla de consentimiento

1. **APIs y servicios → Pantalla de consentimiento de OAuth**.
2. Tipo de usuario:
   - Con Workspace: **Interno** (recomendado — sin verificación de Google, sin límites molestos).
   - Con Gmail normal: **Externo**, y luego agrega la cuenta de la empresa como *usuario de prueba*.
3. Nombre de la aplicación: `DocuHub`. Correo de asistencia: el de la empresa. Guardar.
4. **Permisos (scopes) → Agregar o quitar** → añade manualmente:

   ```
   https://www.googleapis.com/auth/drive
   ```

   Es el permiso completo sobre Drive; lo necesitas para crear carpetas, subir, mover y borrar en nombre
   de la plataforma. Si prefieres el mínimo posible y aceptas que DocuHub solo vea lo que él mismo creó,
   usa `https://www.googleapis.com/auth/drive.file` y cambia `DRIVE_SCOPE` en `.env`.

5. Si elegiste **Externo**, en *Usuarios de prueba* agrega la cuenta que vas a enlazar.

## 4. Credenciales OAuth

1. **APIs y servicios → Credenciales → Crear credenciales → ID de cliente de OAuth**.
2. Tipo de aplicación: **Aplicación web**. Nombre: `DocuHub Web`.
3. **URI de redireccionamiento autorizados** — agrega las dos:

   ```
   http://localhost:8080/api/admin/drive/callback
   https://docs.tuempresa.com/api/admin/drive/callback
   ```

   La primera te deja probar en la laptop; la segunda es la de producción. Deben coincidir **carácter por
   carácter** con `APP_BASE_URL` de tu `.env`, o Google devolverá `redirect_uri_mismatch`.

4. Crear. Copia el **ID de cliente** y el **Secreto de cliente** a `deploy/.env`:

   ```ini
   GOOGLE_CLIENT_ID=123456789-abc.apps.googleusercontent.com
   GOOGLE_CLIENT_SECRET=GOCSPX-xxxxxxxxxxxxxxxx
   ```

## 5. Conectar desde la plataforma

1. Levanta la plataforma (`docker compose up -d` en `deploy/`).
2. Entra como administrador → **Administración → Google Drive → Conectar cuenta**.
3. Google te pedirá permiso; acepta con la cuenta institucional.
4. Al volver, DocuHub guarda el *refresh token* **cifrado con AES-256-GCM** y crea la carpeta raíz
   `DocuHub` en el Drive. La pantalla mostrará el espacio total y el usado de esa cuenta.

Si la cuenta se desconecta (cambio de contraseña, revocación de permisos), la plataforma lo detecta y
muestra el aviso en el panel; basta volver a pulsar **Conectar**.

---

## 6. Sobre el espacio disponible

La plataforma no almacena archivos en la laptop, así que tu límite es el de Google:

| Plan | Espacio |
|---|---|
| Gmail gratuito | 15 GB (compartidos con correo y fotos) |
| Workspace Business Starter | 30 GB por usuario |
| Workspace Business Standard | 2 TB por usuario, agrupados |
| Workspace Business Plus | 5 TB por usuario, agrupados |

Cuando te acerques al límite tienes dos salidas sin cambiar de plataforma:

1. **Enlazar una segunda cuenta** de Google. La tabla `drive_accounts` admite varias; la Fase 6 activa el
   reparto automático cuando una llega al 90%.
2. Subir de plan en Workspace, que suele salir más barato que cualquier almacenamiento equivalente.

---

## 7. Límites del API que conviene conocer

- **750 GB de subida por día y por cuenta.** Es mucho, pero si migras un archivo histórico grande,
  repártelo en varios días o entre varias cuentas enlazadas.
- **Cuota de peticiones:** 12 000 por minuto por proyecto. La plataforma reintenta con retroceso
  exponencial al recibir `403 rateLimitExceeded` o `429`, así que no tendrás que hacer nada.
- Un archivo individual puede pesar hasta **5 TB**.
