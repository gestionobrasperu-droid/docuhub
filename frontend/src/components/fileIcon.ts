// Iconos por tipo de archivo. Vive aparte porque lo usan el explorador, la
// pantalla de inicio y los enlaces públicos, y conviene que un PDF se vea
// igual en los tres sitios.
export function iconFor(mime: string): string {
  const m = (mime || '').toLowerCase()
  if (m.startsWith('image/')) return '🖼️'
  if (m.startsWith('video/')) return '🎬'
  if (m.startsWith('audio/')) return '🎵'
  if (m.includes('pdf')) return '📕'
  if (m.includes('zip') || m.includes('rar') || m.includes('7z') || m.includes('compress')) return '🗜️'
  if (m.includes('sheet') || m.includes('excel') || m.includes('csv')) return '📊'
  if (m.includes('word') || m.includes('document')) return '📝'
  if (m.includes('presentation') || m.includes('powerpoint')) return '📽️'
  // Planos y modelos: lo que más pesa en una constructora.
  if (m.includes('dwg') || m.includes('dxf') || m.includes('cad')) return '📐'
  return '📄'
}
