import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// El build sale directo a backend/web/dist, que es lo que el binario de Go
// incrusta con go:embed. Así "compilar el frontend" y "actualizar el servidor"
// son el mismo paso.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../backend/web/dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1200,
  },
  server: {
    port: 5173,
    proxy: {
      // En desarrollo el frontend corre aparte y habla con el backend real.
      '/api': { target: 'http://localhost:8080', changeOrigin: true },
      '/s': { target: 'http://localhost:8080', changeOrigin: true },
      '/healthz': { target: 'http://localhost:8080', changeOrigin: true },
    },
  },
})
