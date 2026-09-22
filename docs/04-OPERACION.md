# Operación diaria

Qué hacer cuando algo va mal, y qué revisar de vez en cuando para que no vaya mal.

---

## Comandos que vas a usar

```powershell
cd deploy

docker compose ps                  # ¿qué está corriendo?
docker compose logs -f app         # log del backend en vivo
docker compose logs --tail 100 db  # log de la base
docker compose restart app         # reiniciar solo la aplicación
docker compose up -d --build       # reconstruir tras actualizar el código
docker compose down                # detener todo (los datos se conservan)
```

Salud del servicio desde fuera:

```powershell
Invoke-RestMethod http://localhost:8080/healthz
```

Debe responder `status: ok`. Si responde `degradado`, la aplicación está viva pero no alcanza la
base de datos.

---

## Los tres problemas que verás en la práctica

### 1. «Google pide volver a conectar la cuenta»

Pasa cuando alguien revoca el acceso de la app desde la cuenta de Google, cuando se cambia la
contraseña de esa cuenta, o si el refresh token lleva meses sin usarse en un proyecto en modo
*Testing* (Google los caduca a los 7 días en ese modo — otra razón para publicar la app como
**Interna** si tienes Workspace).

Solución: **Administración → Google Drive → Conectar cuenta**. Los archivos no se pierden: los ids
de Drive siguen en la base y se vuelven a usar en cuanto hay token nuevo.

### 2. Una subida grande se corta

No hay que hacer nada especial: la sesión resumible vive 6 días. Al volver a intentarlo, el
navegador pregunta por el avance (`GET /api/uploads/{id}`) y continúa desde el último byte que Drive
confirmó. Si el usuario cerró la pestaña, la subida aparece en `/api/uploads` y se puede retomar o
cancelar.

Si el archivo se subió pero no aparece: `docker compose logs app | Select-String "upload"`.

### 3. La laptop se reinició

El watchdog la recupera sola en menos de 5 minutos. Para comprobarlo:

```powershell
Get-Content logs\health.log -Tail 20
```

Si ves varias líneas `ERROR` seguidas, entra y mira `docker compose logs app`.

---

## Revisión mensual (10 minutos)

1. **Espacio en Drive** — Administración → Google Drive. Por encima del 85%, decide: liberar,
   subir de plan o enlazar una segunda cuenta.
2. **Espacio en la laptop** — el watchdog avisa bajo 5 GB, pero míralo. Las imágenes viejas de
   Docker se limpian con `docker system prune -a` (cuidado: reconstruir tarda unos minutos).
3. **Bitácora** — Administración → Bitácora, filtro «Inicios de sesión». Busca accesos a horas raras
   o IPs desconocidas.
4. **Enlaces públicos** — pestaña Enlaces. Revoca los que ya cumplieron su función.
5. **Respaldos** — que `backups\` tenga archivos recientes y que pesen algo. Copia uno fuera de la
   laptop.
6. **Batería y ventilador** — si la batería se hinchó (el touchpad se siente abombado), cámbiala o
   retírala. Sopla el ventilador con aire comprimido.

---

## Actualizar la plataforma

```powershell
git pull
cd deploy
docker compose up -d --build
```

Las migraciones de base de datos se aplican solas al arrancar el backend y son acumulativas: nunca
se ejecuta dos veces la misma. Antes de una actualización grande, lanza `scripts\backup.ps1`.

---

## Recuperación ante desastre

**Escenario: la laptop se pierde, se roba o se quema.**

Lo que necesitas para revivir la plataforma en otro equipo:

1. El repositorio (está en GitHub).
2. El último `backups\docuhub_*.sql.zip`.
3. El valor de `APP_ENCRYPTION_KEY` del `.env`.

Con eso: instalas Docker, restauras el respaldo, pones la misma clave de cifrado en el `.env` nuevo y
levantas. Los archivos nunca se perdieron — siempre estuvieron en Google Drive.

**Si pierdes `APP_ENCRYPTION_KEY`:** la base se restaura igual, pero el refresh token de Google
queda ilegible. Se arregla reconectando la cuenta desde Administración; no se pierde ningún archivo.
Aun así, guarda esa clave en el gestor de contraseñas de la empresa.

**Si alguien borra archivos por error:** van a la papelera de Google Drive, donde viven 30 días.
Se restauran desde drive.google.com y vuelven a aparecer en DocuHub porque el id no cambia.

---

## Comprobar las tareas automáticas (y no asustarse)

Las tres tareas (`DocuHub-Arranque`, `DocuHub-Watchdog`, `DocuHub-Respaldo`) corren como **SYSTEM**
con nivel más alto. Eso tiene una consecuencia que despista mucho: **desde una terminal normal no se
ven**. `Get-ScheduledTask -TaskName 'DocuHub-*'` devuelve cero resultados y `schtasks /run` responde
«Acceso denegado», aunque las tareas existan y se estén ejecutando cada 5 minutos.

Para verlas hay que consultar con privilegios:

```powershell
Start-Process powershell -Verb RunAs -ArgumentList '-Command','schtasks /query /fo table /nh | findstr DocuHub; pause'
```

La comprobación que sí funciona sin privilegios —y la que de verdad importa, porque mide el trabajo
hecho y no la existencia del registro— es mirar el log:

```powershell
Get-Content logs\health.log -Tail 10
```

Una línea `[OK] servicio respondiendo; disco_libre=…GB temp=…C` cada cinco minutos significa que el
watchdog está vivo y que la plataforma responde. Si el log deja de crecer, la tarea es lo primero
que hay que revisar.
