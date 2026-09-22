# Conectar constructorapesam.com con Cloudflare

Guía específica para **este** dominio, con el estado real medido el 2026-09-22.
No es genérica: los valores de abajo son los que tiene tu dominio ahora mismo.

---

## 1. Punto de partida (medido, no supuesto)

| Qué | Valor actual |
|---|---|
| Registrador | GoDaddy |
| Nameservers | `ns49.domaincontrol.com`, `ns50.domaincontrol.com` → **GoDaddy, no Cloudflare** |
| Registro A (raíz) | `13.248.243.5`, `76.223.105.230` → infraestructura de GoDaddy |
| `www` | `CNAME` → `constructorapesam.com` |
| MX (correo) | **ninguno** — el dominio no recibe correo |
| TXT | **ninguno** — sin SPF, sin DKIM, sin verificaciones |
| Subdominios | `docs`, `api`, `app`, `mail`, `blog`… todos **libres** |
| Sitio publicado | **Sí**: "Constructora Pesam", hecho con **GoDaddy Website Builder** (cabecera `Server: DPS/2.0.0`) |

**Lo que esto significa:** el dominio está casi vacío, así que la migración es de
bajo riesgo — *excepto* por un detalle que no se puede ignorar: hay una web de la
empresa publicada y funcionando. Ese es el único activo que hay que proteger.

---

## 2. Por qué hay que mover los nameservers (y no hay atajo)

Cloudflare Tunnel necesita crear registros que apuntan a `<UUID>.cfargotunnel.com`.
Ese destino **solo resuelve dentro de la red de Cloudflare**. Un CNAME creado desde
el panel DNS de GoDaddy hacia `cfargotunnel.com` no funciona: devuelve error de
resolución.

Las alternativas reales son tres, y solo una sirve:

| Alternativa | ¿Funciona? |
|---|---|
| Dejar el DNS en GoDaddy y crear ahí el CNAME al túnel | ❌ No. `cfargotunnel.com` no resuelve fuera de Cloudflare |
| CNAME setup / partial zone (mantener GoDaddy como DNS primario) | ❌ Requiere plan Business de Cloudflare (~200 USD/mes) |
| **Mover los nameservers a Cloudflare** | ✅ Sí. Gratis. Es la vía documentada |

---

## 3. El riesgo real y cómo se neutraliza

Al mover los nameservers, **Cloudflare pasa a responder todo el DNS del dominio**.
Si los registros de la web actual no se recrean ahí, `constructorapesam.com` deja
de resolver y la web de la empresa se cae.

La defensa es simple y se hace **antes** de tocar GoDaddy:

1. Cuando añades el dominio, Cloudflare escanea el DNS actual e **importa
   automáticamente** los registros que ya existen.
2. Verificas a mano que estén los tres que importan:

   | Tipo | Nombre | Contenido | Proxy |
   |---|---|---|---|
   | A | `constructorapesam.com` | `13.248.243.5` | **DNS only** (nube gris) |
   | A | `constructorapesam.com` | `76.223.105.230` | **DNS only** (nube gris) |
   | CNAME | `www` | `constructorapesam.com` | **DNS only** (nube gris) |

3. **Nube gris, no naranja.** Con "DNS only", Cloudflare solo responde la consulta
   DNS y el navegador va directo a GoDaddy, igual que hoy, con el certificado TLS
   de GoDaddy intacto. Cero cambios en el comportamiento de la web.

   Si pusieras nube naranja, Cloudflare se metería como intermediario TLS delante
   del Website Builder de GoDaddy. Suele funcionar, pero puede dar errores de
   certificado (`525`/`526`) y no hay ninguna ventaja que lo justifique ahora.

4. Los subdominios del túnel (`docs`, `api`, …) **sí** van con nube naranja: ahí el
   proxy de Cloudflare es justamente lo que da TLS, caché y protección.

**Es reversible.** Si algo sale mal, vuelves a poner `ns49`/`ns50.domaincontrol.com`
en GoDaddy y todo regresa al estado actual. La propagación tarda entre 10 minutos y
2 horas.

---

## 4. Pasos en Cloudflare (los haces tú; yo no puedo iniciar sesión por ti)

1. Entra en **https://dash.cloudflare.com/sign-up** y crea la cuenta (o inicia
   sesión si ya tienes una). Usa un correo de la empresa al que tengas acceso
   permanente, no uno personal.
2. **Add a site** → escribe `constructorapesam.com` → **Continue**.
3. Elige el plan **Free** ($0).
4. Cloudflare escanea el DNS. **Revisa la lista** contra la tabla del punto 3.
   Si falta alguno, añádelo a mano. Si sobra algo que no reconoces, déjalo: no
   borres nada en este paso.
5. Pon los tres registros de la web en **DNS only** (clic en la nube naranja hasta
   que quede gris).
6. **Continue** → Cloudflare te muestra **dos nameservers** con esta forma:

   ```
   xxxx.ns.cloudflare.com
   yyyy.ns.cloudflare.com
   ```

   Cópialos. Son únicos de tu cuenta: no sirven los de otro dominio.

---

## 5. Pasos en GoDaddy

1. Entra en **https://dcc.godaddy.com/control/portfolio** → `constructorapesam.com`.
2. **Domain Settings** → sección **Nameservers** → **Change** / **Cambiar**.
3. Elige **"I'll use my own nameservers"** / **"Usar mis propios servidores de nombres"**.
4. Borra `ns49.domaincontrol.com` y `ns50.domaincontrol.com`, y pega los dos de
   Cloudflare.
5. **Save**. GoDaddy avisará de que la gestión sale de su panel: es exactamente lo
   que quieres.

> ⚠️ A partir de aquí, **el DNS se edita en Cloudflare, no en GoDaddy**. El panel de
> GoDaddy seguirá mostrando registros viejos que ya no tienen efecto. Ignóralos.
> GoDaddy sigue siendo el registrador (la renovación anual se paga ahí).

---

## 6. Verificar la migración

Desde esta carpeta, en PowerShell:

```powershell
.\scripts\tunnel-status.ps1
```

El paso **2. Nameservers** debe decir `El dominio esta en Cloudflare.` Si todavía
muestra `domaincontrol.com`, no ha propagado: espera y repite. También puedes mirar:

```powershell
Resolve-DnsName constructorapesam.com -Type NS -Server 8.8.8.8
```

Y comprueba que la web sigue viva:

```powershell
Invoke-WebRequest https://constructorapesam.com -UseBasicParsing | Select-Object StatusCode
```

Debe seguir dando `200`.

---

## 7. Crear el túnel

Solo cuando el paso 6 confirme que el dominio está en Cloudflare:

```powershell
.\scripts\setup-tunnel.ps1
```

El script abre el navegador para que autentiques (**la contraseña la escribes tú**;
el script nunca la ve), crea el túnel `docuhub`, genera
`%USERPROFILE%\.cloudflared\config.yml` y registra los DNS.

Para dejarlo corriendo 24/7, en PowerShell **como administrador**:

```powershell
.\scripts\setup-tunnel.ps1 -InstallService -SkipLogin
```

Prueba inmediata: **https://test.constructorapesam.com** debe mostrar la página
`hello_world` de cloudflared. Esa página la sirve el propio túnel, así que
funciona aunque no haya ninguna aplicación instalada todavía. Si responde,
la cadena dominio → Cloudflare → laptop está completa.

---

## 8. Añadir aplicaciones

Un solo túnel sirve todas las aplicaciones que quieras, cada una en su subdominio:

```powershell
.\scripts\add-app.ps1 -Subdomain docs  -Port 8080   # DocuHub
.\scripts\add-app.ps1 -Subdomain api   -Port 3000
.\scripts\add-app.ps1 -Subdomain panel -Port 5173
```

Destinos que no son HTTP simple:

```powershell
# Origen HTTPS con certificado autofirmado
.\scripts\add-app.ps1 -Subdomain interno -Service "https://localhost:8443" -NoTLSVerify

# TCP puro (SSH, RDP, Postgres) — se accede con `cloudflared access tcp` desde el cliente
.\scripts\add-app.ps1 -Subdomain ssh -Service "ssh://localhost:22"
```

Quitar una:

```powershell
.\scripts\add-app.ps1 -Subdomain panel -Remove
```

Cada cambio se respalda (`config.yml.bak-<fecha>`) y se valida antes de aplicarse;
si la sintaxis queda mal, el script restaura el respaldo solo.

---

## 9. Límites del plan gratuito que debes tener presentes

| Límite | Valor | Cómo te afecta |
|---|---|---|
| Tamaño máximo por petición HTTP | **100 MB** | Por eso DocuHub sube en trozos de 8 MB. Un archivo de 40 GB pasa sin problema |
| Descargas | **sin límite de tamaño** | El streaming de archivos grandes no está afectado |
| Túneles por cuenta | sin límite práctico | Un solo túnel basta para todas las apps |
| Cloudflare Access | gratis hasta **50 usuarios** | Opcional: exige login corporativo *antes* de llegar a tu app |
| Ancho de banda | sin límite declarado | Sujeto a la política de uso aceptable (contenido no-HTML masivo) |

---

## 10. Endurecimiento recomendado (después de que todo funcione)

1. **Cloudflare Access** delante de `docs.*`: obliga a autenticarse con el Google de
   la empresa antes de que la petición toque la laptop. Zero Trust → Access →
   Applications.
2. **Registros de correo defensivos.** El dominio no usa correo, así que conviene
   impedir que alguien lo suplante:

   | Tipo | Nombre | Contenido |
   |---|---|---|
   | TXT | `@` | `v=spf1 -all` |
   | TXT | `_dmarc` | `v=DMARC1; p=reject; rua=mailto:tu-correo@…` |
   | TXT | `*._domainkey` | `v=DKIM1; p=` |

   Si algún día activas correo en el dominio, **hay que revertir esto primero** o
   tus propios correos se rechazarán.
3. **Always Use HTTPS** y **Automatic HTTPS Rewrites**: SSL/TLS → Edge Certificates.
4. **Modo de cifrado**: SSL/TLS → Overview → **Full (strict)**. Aplica a los
   subdominios proxeados; los grises (la web de GoDaddy) no se ven afectados.
