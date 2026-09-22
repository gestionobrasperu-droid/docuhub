# Prueba rápida: vincular Drive y compartir accesos

Guion de 20 minutos para dejar la plataforma funcionando en la laptop y probar el ciclo completo:
conectar la cuenta de Google, subir un archivo pesado, dar acceso a un compañero y mandar un enlace
a alguien de fuera.

Todo esto se hace en `http://localhost:8080`, sin dominio ni túnel. El dominio se conecta después.

---

## Paso 1 — Credenciales de Google (10 min, una sola vez)

Sigue [`02-GOOGLE-DRIVE-SETUP.md`](02-GOOGLE-DRIVE-SETUP.md). Solo necesitas llegar a tener estos dos
valores:

```
GOOGLE_CLIENT_ID=...apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=GOCSPX-...
```

**Para la prueba local, la URI de redirección que debes autorizar en Google Cloud es exactamente:**

```
http://localhost:8080/api/admin/drive/callback
```

Añade también la de producción, así no repites el trámite después:

```
https://docs.constructorapesam.com/api/admin/drive/callback
```

Y no te saltes el apartado 0 de ese documento: con una cuenta Gmail hay que **publicar** la
aplicación en Google Cloud, o el permiso caduca cada 7 días y tendrás que reconectar la cuenta cada
semana.

## Paso 2 — Configurar y levantar

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\setup-windows.ps1
```

Eso instala lo que falte y crea `deploy\.env` con claves nuevas. Abre ese archivo y pega las dos
credenciales de Google. Para la prueba local, ajusta también estas dos líneas:

```ini
APP_BASE_URL=http://localhost:8080
COOKIE_SECURE=false
```

> `COOKIE_SECURE=true` con `http://` haría que el navegador descarte la cookie de sesión y no
> pudieras entrar. Cuando pases al dominio con HTTPS, se vuelve a poner en `true`.

Levanta:

```powershell
cd deploy
docker compose up -d --build
```

La primera vez tarda unos minutos (compila el frontend y el backend). Cuando termine:

```powershell
docker compose logs app
```

Busca el bloque con la **contraseña temporal del administrador**. Aparece una sola vez.

## Paso 3 — Vincular Google Drive

1. Abre `http://localhost:8080` y entra con `gestion.obrasperu@gmail.com` y la contraseña del log.
2. **Administración → Google Drive → Conectar cuenta**.
3. Google te pide permiso. Acepta con la cuenta de la empresa.
4. Vuelves a DocuHub y deberías ver la cuenta enlazada, con su espacio total y usado.

Comprueba en drive.google.com que apareció una carpeta llamada **DocuHub**. Ahí vivirá todo.

**Si Google responde `redirect_uri_mismatch`:** la URI autorizada no coincide con `APP_BASE_URL`
carácter por carácter. La pantalla de Administración muestra la URI exacta que espera la plataforma;
cópiala tal cual a Google Cloud.

## Paso 4 — Probar una subida pesada

1. Pestaña **Archivos**.
2. Arrastra un archivo grande (uno de 1–2 GB es una buena prueba: verifica el troceado).
3. Verás la barra de progreso avanzar por trozos de 16 MB.
4. **Prueba de reanudación**: a mitad de la subida, desconecta el WiFi 20 segundos y vuelve a
   conectarlo. El trozo en curso falla, se reintenta solo y la subida continúa desde donde estaba —
   no desde cero.
5. Al terminar, el archivo aparece en la lista y también en la carpeta DocuHub de Drive.

Descárgalo desde la plataforma para comprobar el otro sentido. Si es un video, salta al minuto 10:
el soporte de `Range` permite hacer seek sin descargar todo.

## Paso 5 — Dar acceso a un compañero (acceso interno)

1. **Administración → Usuarios → Nuevo usuario**.
   - Correo del compañero, rol **Miembro**, cuota 5 GB.
   - Copia la contraseña temporal que aparece: no se vuelve a mostrar.
2. Entra en incógnito con esa cuenta y comprueba que ve los archivos y puede subir.

Ahora la parte interesante, **restringir una carpeta**:

3. Como administrador: **Nueva carpeta** → nombre `Contabilidad` → marca **Carpeta restringida**.
4. Sube algo dentro.
5. En la ventana de incógnito, recarga: el compañero **no ve** la carpeta. Existe, pero para él no.
6. Vuelve como administrador, botón **Permisos** de esa carpeta, escribe el correo del compañero y
   dale nivel **Descargar**.
7. En incógnito, recarga: ahora la ve y puede descargar, pero no subir ni borrar.

Eso es el modelo completo: las carpetas normales se rigen por el rol, las restringidas solo por
permisos explícitos, y los permisos solo suman.

## Paso 5.5 — Probar el escaneo del Drive

Esto es para tu forma de trabajar: seguir usando drive.google.com directamente.

1. Entra a <https://drive.google.com> con `gestion.obrasperu@gmail.com`.
2. Dentro de la carpeta **DocuHub**, crea una carpeta `Pruebas-Manual` y arrastra ahí dos archivos.
3. Vuelve a DocuHub: **Administración → Google Drive → Escanear Drive**.
4. Te dirá cuántas carpetas y archivos nuevos importó.
5. Ve a **Archivos**: la carpeta y sus archivos ya están, y puedes darles permisos, compartirlos por
   enlace y ver quién los descarga — exactamente igual que si los hubieras subido desde la web.

El escaneo es manual a propósito: recorrer el Drive entero cada pocos minutos gastaría cuota del API
sin necesidad. Si algún día quieres que sea automático, se agenda con el Programador de tareas.

## Paso 6 — Compartir con alguien de fuera (enlace público)

1. Botón **Compartir** de un archivo.
2. Pon una contraseña, 7 días de caducidad y máximo 3 descargas.
3. Copia el enlace — igual que la contraseña del admin, se muestra una sola vez.
4. Ábrelo en otro navegador (o en el móvil). Te pedirá la contraseña y luego dejará descargar.
5. Vuelve a DocuHub, pestaña **Enlaces**: verás el contador de descargas subiendo.
6. Pulsa **Revocar** y prueba el enlace otra vez: deja de funcionar al instante.

## Paso 7 — Comprobar que todo quedó registrado

**Administración → Bitácora.** Debes ver, en orden inverso: tu login, la conexión de Drive, la
subida, la creación del usuario, el permiso otorgado, la creación del enlace, la descarga externa
con su IP, y la revocación.

**Administración → Uso y estadísticas**: bytes almacenados, tráfico por día, consumo por usuario y
los archivos más descargados.

Eso es el «administrar por completo los usos» funcionando de punta a punta.

---

## Cuando la prueba salga bien

1. Cambia en `deploy\.env`:
   ```ini
   APP_BASE_URL=https://docs.constructorapesam.com
   COOKIE_SECURE=true
   ```
2. `docker compose up -d` para aplicarlo.
3. Publica con el túnel: [`03-CLOUDFLARE-DOMINIO.md`](03-CLOUDFLARE-DOMINIO.md).
4. Deja la laptop lista para 24/7:
   ```powershell
   # como administrador
   .\scripts\keep-alive.ps1
   .\scripts\install-tasks.ps1
   ```

---

## Problemas frecuentes en la primera prueba

| Síntoma | Causa | Solución |
|---|---|---|
| No puedo iniciar sesión, la página recarga | `COOKIE_SECURE=true` sirviendo por `http://` | Ponlo en `false` para la prueba local |
| `redirect_uri_mismatch` | La URI de Google no coincide con `APP_BASE_URL` | Copia la URI que muestra la pantalla de Administración |
| «Google no devolvió refresh token» | La cuenta ya había autorizado la app antes | En la cuenta de Google, quita el acceso de DocuHub y vuelve a conectar |
| La subida se corta al 100 % sin confirmar | La sesión resumible caducó | Vuelve a subir; el registro anterior se limpia solo |
| `no hay ninguna cuenta de Google Drive conectada` | Falta el paso 3, o la cuenta quedó en estado `error` | Reconecta desde Administración → Google Drive |
| El contenedor `app` reinicia en bucle | Falta `APP_ENCRYPTION_KEY` o la base no arrancó | `docker compose logs app` dice cuál de las dos |

---

## Prueba de extremo a extremo ya ejecutada — 2026-09-22

Esto no es un guion pendiente: se ejecutó contra la instalación real, por el dominio, y estos fueron
los resultados.

| Paso | Resultado |
|---|---|
| Publicar la app en Google Cloud | Estado **En producción** |
| Permisos configurados | Solo no sensibles: `drive.file`, `userinfo.email`, `userinfo.profile`, `openid` |
| Pantalla de consentimiento | **Sin advertencia de app no verificada** |
| Conectar `gestion.obrasperu@gmail.com` | Cuenta activa, carpeta raíz `DocuHub` creada en Drive |
| Espacio detectado | **38.4 GB de 2.0 TB** (2%) |
| Crear carpeta | Creada en la plataforma y en Drive |
| Subir archivo de 3 MB | Completado; **el MD5 que devuelve Google coincide con el local** |
| Descargar | **Idéntico byte a byte** al original |
| Enlace público con contraseña, caducidad y tope de descargas | Creado |
| Abrir el enlace sin la contraseña | **HTTP 401**, como debe ser |
| Desbloquear y descargar | HTTP 200, archivo íntegro |
| Limpieza | Enlace revocado, archivo y carpeta de prueba eliminados |

Lo que queda por hacer es de uso, no de instalación: cambiar la contraseña temporal del
administrador, crear las cuentas del personal y empezar a subir documentos.
