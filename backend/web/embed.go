// Package web sirve el frontend compilado desde dentro del binario.
// Tras `npm run build`, el contenido de frontend/dist se copia a web/dist y
// queda incrustado en el ejecutable: desplegar es copiar un solo archivo.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler devuelve el servidor de archivos estáticos con respaldo de SPA:
// cualquier ruta desconocida devuelve index.html para que el enrutador del
// frontend se encargue.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if clean == "" || clean == "." {
			clean = "index.html"
		}

		if _, err := fs.Stat(sub, clean); err != nil {
			// Ruta del frontend (por ejemplo /admin): se entrega la SPA.
			r = r.Clone(r.Context())
			r.URL.Path = "/"
			w.Header().Set("Cache-Control", "no-cache")
			files.ServeHTTP(w, r)
			return
		}

		// Los recursos con huella digital en el nombre se cachean fuerte.
		if strings.HasPrefix(clean, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
