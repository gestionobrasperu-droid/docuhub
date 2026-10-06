# Página de cortesía (Cloudflare Worker)

Cuando la laptop está apagada, Cloudflare devuelve su propia pantalla de error —el famoso
**Error 1033 · Argo Tunnel error**— con su logotipo y un texto técnico en inglés. Quien la ve suele
ser un cliente que acaba de recibir un enlace de descarga.

Este Worker la sustituye por una página con la identidad de la empresa que explica qué pasa, dice
que el enlace sigue siendo válido y se recarga sola hasta que el servicio vuelve.

**Cuando la plataforma está en marcha, el Worker no hace nada**: deja pasar la respuesta tal cual,
incluidas descargas y subidas por trozos, que siguen funcionando en streaming.

---

## Qué cuesta

Nada. El plan gratuito de Cloudflare incluye 100.000 peticiones de Worker al día. Una subida de
1 GB en trozos de 16 MB son unas 64 peticiones.

---

## Instalación (5 minutos, una sola vez)

1. Entra a <https://dash.cloudflare.com> → **Workers y páginas** → **Crear** → **Crear Worker**.
2. Nombre: `docuhub-cortesia`. **Implementar**.
3. **Editar código**: borra lo que trae y pega el contenido de [`cortesia.js`](cortesia.js).
   **Implementar**.
4. Vuelve a **Workers y páginas** → `docuhub-cortesia` → pestaña **Configuración** → **Dominios y
   rutas** → **Agregar** → **Ruta**:

   | Campo | Valor |
   |---|---|
   | Zona | `constructorapesam.com` |
   | Ruta | `docs.constructorapesam.com/*` |

5. Guardar.

## Comprobar que funciona

Con la plataforma **encendida**, abre <https://docs.constructorapesam.com>: debe entrar con
normalidad. Si entra, el Worker está dejando pasar el tráfico como debe.

Con la plataforma **apagada** (`Detener DocuHub` en el escritorio), recarga: debe aparecer la página
de cortesía en lugar del error 1033.

## Si algo va mal

El Worker está escrito para no romper nada: cualquier excepción se captura y acaba mostrando la
página de cortesía, nunca un error del propio Worker. Aun así, si hiciera falta quitarlo, basta con
borrar la **ruta** del paso 4 — el tráfico vuelve a ir directo al túnel al instante, sin tocar nada
más.

## Qué cambiar si cambian las cosas

Todo el texto visible está dentro de `paginaDeCortesia()` en `cortesia.js`. Si cambia el correo de
contacto o el horario de oficina, se edita ahí y se vuelve a implementar.

Si algún día la plataforma pasa a estar encendida de forma continua, este Worker deja de tener
sentido: se borra la ruta y listo.
