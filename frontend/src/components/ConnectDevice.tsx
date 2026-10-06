import { useCallback, useEffect, useState } from 'react'
import { ApiError, Device, devices, formatDate } from '../api'

// Conectar un equipo: descarga un instalador que monta la plataforma como
// unidad de red en ese PC. La credencial que lleva dentro es de ese equipo,
// no la contraseña del usuario, así que se puede revocar sola.
export default function ConnectDevice() {
  const [lista, setLista] = useState<Device[]>([])
  const [error, setError] = useState('')
  const [nombre, setNombre] = useState('')
  const [letra, setLetra] = useState('W')
  const [descargado, setDescargado] = useState(false)

  const load = useCallback(async () => {
    try {
      setLista((await devices.list()).devices)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'No se pudieron listar tus equipos')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  function descargar() {
    const equipo = nombre.trim() || 'Mi PC'
    window.location.href = devices.installerUrl(equipo, letra)
    setDescargado(true)
    // El alta ocurre en el servidor al generar el archivo; se refresca después.
    setTimeout(() => void load(), 2500)
  }

  return (
    <div className="card">
      <div className="card-head">
        <h2>Usar DocuHub como una unidad de disco</h2>
      </div>
      <div className="card-body">
        {error && (
          <div className="alert error">
            <span className="ico">⚠️</span>
            <span>{error}</span>
          </div>
        )}

        <p className="muted" style={{ marginTop: 0 }}>
          Monta la plataforma como una unidad más del Explorador de Windows. Arrastras archivos y se
          suben solos; los abres y se descargan al momento. <b>No ocupan espacio en el equipo</b> y
          todo sigue pasando por tus permisos, tu cuota y la bitácora.
        </p>

        <div className="row" style={{ alignItems: 'flex-end', gap: '.6rem' }}>
          <div className="field" style={{ flex: 2, marginBottom: 0 }}>
            <label htmlFor="eq">Nombre de este equipo</label>
            <input
              id="eq"
              value={nombre}
              onChange={(e) => setNombre(e.target.value)}
              placeholder="Ej.: Laptop de obra, PC de oficina"
            />
          </div>
          <div className="field" style={{ width: 110, marginBottom: 0 }}>
            <label htmlFor="le">Unidad</label>
            <select id="le" value={letra} onChange={(e) => setLetra(e.target.value)}>
              {['W', 'X', 'Y', 'V', 'T', 'R'].map((l) => (
                <option key={l} value={l}>
                  {l}:
                </option>
              ))}
            </select>
          </div>
          <button className="primary" onClick={descargar}>
            ⬇ Descargar instalador
          </button>
        </div>

        {descargado && (
          <div className="alert ok" style={{ marginTop: '1rem' }}>
            <span className="ico">✓</span>
            <span>
              Se descargó <b>Conectar-DocuHub.bat</b>. Ábrelo con <b>doble clic</b> y acepta el aviso
              de Windows: eso es lo que permite subir el límite de archivo de 50 MB a 4 GB. Si lo
              rechazas, la unidad se monta igual, solo que con el límite pequeño.
              <div style={{ marginTop: '.4rem' }}>
                <a href={devices.installerUrl(nombre.trim() || 'Mi PC', letra, 'ps1')}>
                  Descargar la versión .ps1
                </a>{' '}
                <span className="dim">— si una política de la empresa bloquea los .bat</span>
              </div>
            </span>
          </div>
        )}

        <div className="alert info" style={{ marginTop: '1rem' }}>
          <span className="ico">🔒</span>
          <span>
            El instalador lleva dentro una credencial <b>solo para ese equipo</b>. Si lo pierdes o lo
            cambias, lo desconectas aquí abajo y esa credencial deja de valer — sin tocar tu
            contraseña ni los demás equipos.
          </span>
        </div>

        {lista.length > 0 && (
          <>
            <div className="section-title">Equipos conectados</div>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Equipo</th>
                    <th>Conectado el</th>
                    <th>Último uso</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {lista.map((d) => (
                    <tr key={d.id}>
                      <td>
                        <b>{d.device_name}</b>
                        {d.last_ip && <div className="dim mono">{d.last_ip}</div>}
                      </td>
                      <td className="dim">{formatDate(d.created_at)}</td>
                      <td className="dim">
                        {d.last_used_at ? formatDate(d.last_used_at) : 'nunca usado'}
                      </td>
                      <td style={{ textAlign: 'right' }}>
                        <button
                          className="danger sm"
                          onClick={async () => {
                            if (!confirm(`¿Desconectar "${d.device_name}"?\n\nEse equipo dejará de ver la unidad.`))
                              return
                            try {
                              await devices.revoke(d.id)
                              await load()
                            } catch (err) {
                              setError(err instanceof ApiError ? err.message : 'No se pudo desconectar')
                            }
                          }}
                        >
                          Desconectar
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
