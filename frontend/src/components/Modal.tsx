import { ReactNode, useEffect } from 'react'

interface Props {
  title: string
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
}

export default function Modal({ title, onClose, children, footer }: Props) {
  // Escape cierra: en una herramienta interna se usa el teclado todo el día.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()} role="dialog" aria-modal="true">
        <div className="row between" style={{ marginBottom: '.8rem' }}>
          <h2 style={{ margin: 0 }}>{title}</h2>
          <button className="ghost" onClick={onClose} aria-label="Cerrar">
            ✕
          </button>
        </div>
        {children}
        {footer && <div className="row" style={{ marginTop: '1rem', justifyContent: 'flex-end' }}>{footer}</div>}
      </div>
    </div>
  )
}
