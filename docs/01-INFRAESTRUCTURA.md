# Infraestructura: cómo publicar esto sin servidor ni VPS

Tienes una laptop y un dominio. Es suficiente. Este documento cubre las dos mitades del problema:
**(A)** cómo se ve tu laptop desde internet, y **(B)** cómo logras que la laptop no se apague nunca.

---

## Parte A — Exponer la laptop a internet

### Opción 1 — Cloudflare Tunnel ⭐ recomendada

Un proceso (`cloudflared`) abre una conexión **saliente** desde la laptop hacia Cloudflare. El tráfico de
`docs.tuempresa.com` entra por Cloudflare y baja por ese túnel. Costo: **$0**.

Por qué es la mejor para tu caso:

- No necesitas IP pública ni IP fija — funciona detrás del router de la oficina o de un módem 4G.
- No abres ni un solo puerto del router. La laptop es invisible desde internet; solo habla hacia afuera.
- TLS (candado verde) automático y gratis, renovado solo.
- Cloudflare absorbe ataques y bots antes de que lleguen a la laptop.
- Puedes poner **Cloudflare Access** encima: obliga a autenticarse con el Google de la empresa *antes*
  de que la petición toque tu aplicación. Doble candado, gratis hasta 50 usuarios.

Límite importante que debes conocer: el plan gratuito de Cloudflare **limita el tamaño de subida a
100 MB por petición HTTP**. Por eso la plataforma sube en trozos de 8 MB — un archivo de 40 GB pasa sin
problema porque ninguna petición individual supera el límite. Las descargas no tienen ese tope.

**Instalación:**

```powershell
winget install --id Cloudflare.cloudflared
cloudflared tunnel login          # abre el navegador, eliges el dominio de la empresa
cloudflared tunnel create docuhub # guarda el archivo de credenciales y muestra el UUID
cloudflared tunnel route dns docuhub docs.tuempresa.com
```

Copia `deploy/cloudflared/config.example.yml` a `%USERPROFILE%\.cloudflared\config.yml`, pon tu UUID y
tu dominio, y registra el túnel como servicio de Windows para que arranque solo:

```powershell
cloudflared service install
```

Verifica: `Get-Service cloudflared` debe decir `Running`.

### Opción 2 — Tailscale Funnel

Alternativa si prefieres no depender de Cloudflare. Instalas Tailscale, activas `tailscale funnel 8080`
y obtienes una URL pública `https://laptop.tu-tailnet.ts.net`. Costo $0.

Ventaja: además te da una **VPN privada** — puedes dejar la plataforma accesible *solo* para dispositivos
de la empresa (`tailscale serve` en vez de `funnel`), que es más seguro que exponerla al público.
Desventaja: el dominio propio de la empresa no se puede usar directamente en el modo Funnel; quedas atado
al subdominio `.ts.net`.

**Combinación buena:** Funnel/Cloudflare para clientes externos, Tailscale privado para el personal.

### Opción 3 — Port forwarding clásico ❌ no recomendada

Abrir el puerto 443 del router hacia la laptop. Requiere IP pública (muchos ISP en Perú dan CGNAT, así que
ni siquiera es posible), IP fija o DDNS, gestionar certificados, y expone la laptop directamente a los
escaneos de internet. Solo si las opciones 1 y 2 están bloqueadas por política de red.

### Opción 4 — Plan B sin laptop, también a costo $0

Si más adelante la laptop se vuelve un problema, la aplicación está hecha para moverse sin reescribir nada,
porque **los archivos pesados no viven en el servidor, viven en Drive**. El servidor solo necesita CPU
para canalizar. Alternativas gratuitas reales:

| Opción | Qué da gratis | Trampa a vigilar |
|---|---|---|
| **Oracle Cloud Always Free** | 4 vCPU ARM + 24 GB RAM + 200 GB disco, permanente | Puede reclamar instancias inactivas; la disponibilidad de ARM varía por región |
| **Google Cloud Run** | 2M peticiones/mes, escala a cero | Tope de 60 min por petición; necesitas Postgres aparte |
| **Neon / Supabase** | Postgres administrado gratis | Neon suspende la base tras inactividad (arranque en frío de ~1 s) |
| **Fly.io** | Máquinas pequeñas en capa gratuita | Requiere tarjeta registrada aunque no cobre |

La ruta más limpia de esa lista: **Cloud Run (aplicación) + Neon (base de datos)** = $0/mes, sin hardware,
y tu `docker-compose.yml` ya define la imagen que necesitas. Lo dejo documentado, no implementado, porque
hoy tu decisión es usar la laptop.

### Opción 5 — Híbrido (lo que yo haría en 6 meses)

Laptop como equipo principal + una copia dormida en Oracle Free. Cloudflare hace *failover* automático:
si el túnel de la laptop se cae, el tráfico va a la copia. Cero tiempo fuera de servicio, cero costo.

---

## Parte B — Que la laptop no se apague nunca

Este es el punto delicado. Una laptop puede funcionar 24/7 durante años, pero hay que configurarla para
eso y aceptar dos desgastes: la batería y el ventilador.

### B.1 — Energía de Windows

Ejecuta `scripts\keep-alive.ps1` una sola vez como administrador. Hace esto:

```powershell
powercfg /change standby-timeout-ac 0      # nunca suspender conectada
powercfg /change hibernate-timeout-ac 0    # nunca hibernar
powercfg /change monitor-timeout-ac 10     # la pantalla sí se apaga (ahorra energía, no afecta al servicio)
powercfg /change disk-timeout-ac 0         # el disco no se duerme
powercfg /hibernate off                    # libera varios GB y elimina estados raros
powercfg /setacvalueindex SCHEME_CURRENT SUB_BUTTONS LIDACTION 0   # cerrar la tapa NO hace nada
powercfg /setactive SCHEME_CURRENT
```

Cerrar la tapa sin que se suspenda es lo que te permite tenerla cerrada en un rincón, ventilada y sin
ocupar escritorio.

### B.2 — Reinicio automático tras corte de luz

Aquí la laptop te gana a cualquier PC de escritorio: **la batería es un UPS incorporado**. Un corte de
2 horas no la apaga. Aun así, configura la recuperación:

1. En la BIOS (F10 al arrancar en HP): busca `Restore on AC Power Loss` o `After Power Loss` → ponlo en
   **Power On**. Así, si la batería se agota por completo y vuelve la luz, arranca sola.
2. Windows: activa el inicio de sesión automático para que los servicios suban sin que nadie escriba la
   contraseña. Los contenedores y `cloudflared` ya están como servicios/tareas, así que no dependen de
   que alguien inicie sesión — pero Docker Desktop sí. Si quieres evitar el auto-login (más seguro),
   usa **Docker Engine sin Desktop vía WSL2**, que corre como servicio real.
3. Tarea programada `DocuHub-Boot` que levanta `docker compose up -d` al arrancar (`install-task.ps1`).

### B.3 — Salud de la batería (importante, no lo saltes)

Una batería de litio enchufada al 100% permanentemente se hincha en 1–2 años. Dos defensas:

- **HP Battery Health Manager** (BIOS → Advanced → Built-in Device Options): ponlo en
  `Maximize my battery health`. Limita la carga al ~80% y elimina la mayor parte del daño.
- Si tu modelo no lo trae, revisa la batería cada 6 meses. Si se hincha, **reemplázala o retírala**
  (muchas laptops funcionan enchufadas sin batería — pierdes el UPS pero eliminas el riesgo de incendio).

### B.4 — Ventilación y temperatura

Elévala sobre algo rígido que deje pasar aire por abajo (una base con rejilla, no una cama ni un escritorio
cerrado). Limpia el ventilador con aire comprimido cada 6 meses. El watchdog registra la temperatura en
cada chequeo, así ves la tendencia antes de que sea un problema.

### B.5 — Actualizaciones de Windows

Lo que más tumba servicios caseros: un reinicio de Windows Update a las 3 a.m.

```powershell
# Horas activas amplias: Windows no reinicia dentro de esta ventana
Set-ItemProperty -Path 'HKLM:\SOFTWARE\Microsoft\WindowsUpdate\UX\Settings' -Name 'ActiveHoursStart' -Value 6
Set-ItemProperty -Path 'HKLM:\SOFTWARE\Microsoft\WindowsUpdate\UX\Settings' -Name 'ActiveHoursEnd' -Value 23
```

Y como los servicios arrancan solos con la máquina, un reinicio ocasional solo cuesta ~90 segundos fuera
de línea. No pelees contra las actualizaciones: son tu parche de seguridad.

### B.6 — Internet

El enlace de la oficina es ahora un punto único de falla. Mitigación barata: deja un celular con datos
conectado por USB como respaldo, y en el adaptador Wi-Fi/Ethernet configura una métrica más baja para el
enlace principal. Si se cae el principal, Windows usa el celular y `cloudflared` reconecta solo en segundos.

### B.7 — Consumo eléctrico

Una laptop moderna sirviendo archivos consume entre 15 W (en reposo, pantalla apagada) y 45 W (subiendo a
tope). Un promedio realista de 25 W son ~18 kWh al mes. A una tarifa residencial de ~S/ 0.80 por kWh son
**alrededor de S/ 15 mensuales**. Menos que cualquier VPS.

Si dentro de un año quieres liberar la laptop, un mini PC usado (N100, ~US$ 120) consume 7 W y hace
exactamente el mismo trabajo. No hace falta hoy.

---

## Cuadro de decisión rápido

| Si tu prioridad es… | Haz esto |
|---|---|
| Arrancar hoy con el dominio de la empresa | Laptop + Cloudflare Tunnel (Opción 1) |
| Que solo el personal entre, máxima seguridad | Laptop + Tailscale privado (Opción 2) |
| Dejar de depender de la laptop sin pagar | Cloud Run + Neon (Opción 4) |
| Cero tiempo fuera de servicio | Híbrido laptop + Oracle Free (Opción 5) |
