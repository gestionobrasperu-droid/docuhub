/**
 * Página de cortesía de DocuHub.
 *
 * Cuando la laptop que sirve la plataforma está apagada, Cloudflare devuelve
 * su propio error técnico (1033 "Argo Tunnel error", o un 502). Quien lo ve es
 * un cliente de la constructora que acaba de recibir un enlace, y lo que lee
 * es una pantalla de error ajena con el logotipo de otra empresa.
 *
 * Este Worker se interpone: si el origen no responde, devuelve una página con
 * la identidad de la empresa que explica qué pasa y cuándo volver. Si el
 * origen responde con normalidad, no toca nada — la respuesta pasa tal cual,
 * incluidas las descargas y las subidas por trozos, que siguen en streaming.
 *
 * Despliegue: ver README.md en esta misma carpeta.
 */

// Códigos con los que Cloudflare informa de que no alcanza al origen.
// El 530 es el del túnel caído, que es el caso de esta instalación.
const ORIGEN_CAIDO = new Set([502, 503, 521, 522, 523, 524, 525, 526, 530]);

export default {
  async fetch(request) {
    try {
      const respuesta = await fetch(request);

      if (ORIGEN_CAIDO.has(respuesta.status)) {
        return paginaDeCortesia(respuesta.status);
      }
      return respuesta;
    } catch (error) {
      // Cualquier fallo al alcanzar el origen acaba aquí. Nunca se deja que
      // una excepción del Worker llegue al visitante: eso daría un error 1101,
      // que es todavía menos explicativo que el que intentamos evitar.
      return paginaDeCortesia(0, error && error.message);
    }
  },
};

function paginaDeCortesia(estado, detalle) {
  const html = `<!doctype html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>DocuHub no está disponible ahora mismo</title>
<meta name="robots" content="noindex">
<!-- Reintenta sola: cuando la plataforma vuelve, el visitante entra sin hacer nada -->
<meta http-equiv="refresh" content="60">
<style>
  :root { color-scheme: light dark; }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100vh;
    display: grid; place-items: center; padding: 1.5rem;
    font: 16px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Arial, sans-serif;
    background: #f4f6f8; color: #16202e;
  }
  .caja {
    width: min(460px, 100%); background: #fff;
    border: 1px solid #dde3ea; border-radius: 12px;
    box-shadow: 0 1px 3px rgba(16,24,40,.08), 0 12px 32px -12px rgba(16,24,40,.18);
    padding: 2rem 1.8rem; text-align: center;
  }
  .marca {
    width: 50px; height: 50px; margin: 0 auto 1rem; border-radius: 13px;
    background: linear-gradient(135deg, #3b82f6, #1d4ed8);
    display: grid; place-items: center; font-size: 1.5rem;
    box-shadow: 0 6px 18px rgba(29,78,216,.35);
  }
  h1 { font-size: 1.3rem; margin: 0 0 .2rem; letter-spacing: -.02em; }
  .sub { color: #56646f; font-size: .9rem; margin: 0 0 1.4rem; }
  .aviso {
    background: #fdf1dc; color: #8a5a00; border-radius: 8px;
    padding: .8rem 1rem; font-size: .9rem; text-align: left; margin-bottom: 1.2rem;
  }
  .detalle { color: #56646f; font-size: .92rem; text-align: left; }
  .detalle p { margin: 0 0 .7rem; }
  .pie {
    margin-top: 1.5rem; padding-top: 1rem; border-top: 1px solid #dde3ea;
    font-size: .82rem; color: #8391a0;
  }
  a { color: #1d4ed8; }
  .contador { font-variant-numeric: tabular-nums; }
  @media (prefers-color-scheme: dark) {
    body { background: #0d1219; color: #e8edf3; }
    .caja { background: #151c26; border-color: #263141;
            box-shadow: 0 1px 3px rgba(0,0,0,.4), 0 12px 32px -12px rgba(0,0,0,.6); }
    .sub, .detalle { color: #9fb0c2; }
    .aviso { background: #2e2413; color: #e0b03f; }
    .pie { border-color: #263141; color: #708093; }
    a { color: #5b90ff; }
  }
</style>
</head>
<body>
  <main class="caja">
    <div class="marca" aria-hidden="true">📁</div>
    <h1>DocuHub</h1>
    <p class="sub">Constructora Pesam</p>

    <div class="aviso">
      <strong>El servicio no está disponible en este momento.</strong>
      No es un problema del enlace que recibiste: sigue siendo válido.
    </div>

    <div class="detalle">
      <p>
        La plataforma se atiende desde las oficinas de la empresa y en este
        momento está fuera de servicio. Suele estar disponible en horario de
        oficina, de lunes a viernes.
      </p>
      <p>
        <strong>No hace falta que hagas nada:</strong> esta página se actualiza
        sola y te dejará entrar en cuanto el servicio vuelva.
      </p>
      <p>
        Si necesitas el documento con urgencia, escribe a
        <a href="mailto:gestion.obrasperu@gmail.com">gestion.obrasperu@gmail.com</a>.
      </p>
    </div>

    <div class="pie">
      Reintentando en <span class="contador" id="c">60</span> s
      · <a href="https://constructorapesam.com">constructorapesam.com</a>
    </div>
  </main>

  <script>
    // El contador es solo informativo: quien recarga es la etiqueta meta.
    var n = 60;
    var el = document.getElementById('c');
    setInterval(function () { if (n > 0) { n--; el.textContent = n; } }, 1000);
  </script>
</body>
</html>`;

  return new Response(html, {
    // 503 es el código honesto: el servicio existe, pero no está disponible.
    // Retry-After evita que los buscadores interpreten esto como una baja.
    status: 503,
    headers: {
      'Content-Type': 'text/html; charset=utf-8',
      'Retry-After': '300',
      'Cache-Control': 'no-store',
      'X-DocuHub-Origen': estado ? String(estado) : (detalle || 'inalcanzable'),
    },
  });
}
