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

---

## Disponibilidad real: cuántas horas al día está en pie

Cuando el servidor es una laptop, la pregunta no es si el servicio responde ahora, sino cuántas
horas al día responde. El watchdog escribe una línea cada 5 minutos mientras el equipo está
encendido, así que el dato se puede medir:

```powershell
.\scripts\uptime-report.ps1 -Days 14
```

Devuelve las horas encendida, la media diaria, el porcentaje de disponibilidad y los períodos en que
el servicio no estuvo accesible.

**Medición del 5 de octubre de 2026 (primeros 14 días):** 37 h de 336 posibles — **11% de
disponibilidad, 2.6 h al día**. La causa no fue técnica: el registro de eventos de Windows no
muestra ni una sola suspensión en 7 días, y sí 25 apagados manuales. La laptop se apaga al terminar
la jornada.

Esto no es un fallo que arreglar en el código, es una decisión de cómo se usa el equipo. Las
opciones, en orden de esfuerzo:

1. **Dejarla encendida.** Ya está configurada para no suspenderse ni al cerrar la tapa. Cuesta unos
   S/ 15 al mes de electricidad. Si el equipo se queda en la oficina, es lo más sencillo.
2. **Aceptar el horario de oficina.** Si quien usa DocuHub trabaja en ese horario, 11% basta. Lo que
   no se puede es prometer a un cliente externo un enlace de descarga a las 10 de la noche.
3. **Mover la plataforma a hardware que no se mueve.** Un mini PC de ~US$ 120 consume 7 W y hace el
   mismo trabajo, o Cloud Run + Neon a coste cero (ver `01-INFRAESTRUCTURA.md`). Esta es la salida si
   la laptop se usa fuera de la oficina: un equipo portátil no puede ser un servidor permanente.

> Mientras la laptop está apagada, quien abra `docs.constructorapesam.com` recibe un error de
> Cloudflare, no una página de la empresa. Si se van a repartir enlaces fuera, conviene una página
> de cortesía con un Worker de Cloudflare (gratis) que explique el horario.

### Las tareas no se ejecutaban con batería

Hasta el 5 de octubre de 2026, el respaldo diario fallaba con el código `-2147020576`
(`0x800710E0`, «el operador o administrador ha rechazado la solicitud»). No era un error del script:
`schtasks` crea las tareas con dos condiciones de energía que, en una laptop, equivalen a
desactivarlas — *no iniciar si el equipo está con batería* y *detener si se pasa a batería*.

El resultado: en 13 días solo se había generado **un** respaldo, el único día que el equipo estaba
enchufado a las 03:15.

Ya está corregido, y `install-tasks.ps1` aplica el ajuste al crear las tareas, así que no vuelve a
pasar al reinstalar. Las tres tareas tienen ahora:

- **Iniciar aunque esté con batería** y no detenerse al pasar a batería.
- **Recuperar ejecuciones perdidas** (`StartWhenAvailable`): si a las 03:15 el equipo estaba
  apagado, el respaldo se hace al encenderlo.

Comprobación (elevada, porque corren como SYSTEM):

```powershell
Start-Process powershell -Verb RunAs -ArgumentList '-Command','schtasks /query /tn DocuHub-Respaldo /fo list /v | findstr /i "resultado ejecución"; pause'
```

Un **Último resultado: 0** es correcto. Cualquier otro valor merece una mirada.

### Dónde acaban los respaldos

El proyecto vive dentro de `C:\Users\HP\OneDrive\...`, así que la carpeta `backups\` **se sincroniza
sola a OneDrive**. Eso cumple la regla de que un respaldo no debe vivir solo en el equipo que puede
fallar.

Tiene una contrapartida que conviene conocer: `deploy\.env` también se sincroniza, y contiene la
clave de cifrado, la contraseña de Postgres y el secreto de Google. Quien entre a esa cuenta de
Microsoft tiene las llaves de la plataforma.

- A favor de dejarlo así: si la laptop desaparece, `APP_ENCRYPTION_KEY` sobrevive, y sin ella los
  tokens de Google guardados son ilegibles.
- Mitigación recomendada: **verificación en dos pasos en la cuenta de Microsoft**. Es gratis y
  cierra el único agujero real de este montaje.

---

## Los respaldos están verificados, no solo hechos

Un respaldo que nunca se ha restaurado no es un respaldo: es un archivo del que nadie sabe nada.
Enterarse de que no servía el día que hace falta es lo peor que puede pasar.

```powershell
.\scripts\verify-backup.ps1
```

Toma el respaldo más reciente, levanta un PostgreSQL temporal y aislado, lo restaura dentro y
comprueba lo que de verdad importa para recuperarse:

| Comprobación | Por qué importa |
|---|---|
| La restauración no da errores | Un volcado truncado se detecta aquí, no en la emergencia |
| Hay usuarios y su hash Argon2id está intacto | Si no, nadie podría entrar en el sistema recuperado |
| Las cuentas de Drive conservan su token cifrado | Si no, habría que volver a autorizar Google a mano |
| Cuántos archivos y entradas de bitácora vuelven | Confirma que hay datos, no solo tablas vacías |

Al terminar destruye el contenedor de prueba. **Nunca toca la base de producción.**

Se ejecuta solo los **domingos a las 04:00** (tarea `DocuHub-Verificar`), después del respaldo
diario. El resultado queda en `logs\backup-verify.log`:

```
2026-10-05 19:55:34 [OK] respaldo restaurable: 1 usuarios, 1 cuentas de Drive con token,
                         1 archivos, 45 entradas de bitácora
```

Si alguna semana aparece una línea `[ERROR]`, el respaldo de ese día no sirve y hay que mirar por
qué antes de seguir confiando en él. El script también avisa si el respaldo más reciente tiene más
de 3 días, que es la señal de que la tarea diaria dejó de ejecutarse.

**Primera verificación real:** 5 de octubre de 2026. El volcado restauró limpio, con el hash del
administrador y el token de Google intactos. Es decir: con el repositorio, un `.zip` de `backups\` y
el valor de `APP_ENCRYPTION_KEY`, la plataforma se levanta en otro equipo sin reconectar nada.

### Las cuatro tareas automáticas

| Tarea | Cuándo | Qué hace |
|---|---|---|
| `DocuHub-Arranque` | al encender | Espera a Docker y levanta la plataforma |
| `DocuHub-Watchdog` | cada 5 min | Comprueba `/healthz`, reinicia si no responde, anota los cortes |
| `DocuHub-Respaldo` | a diario, 03:15 | `pg_dump` comprimido en `backups\` |
| `DocuHub-Verificar` | domingos, 04:00 | Restaura el último respaldo y comprueba que sirve |
