package drive

import (
	"context"
	"net/http"
	"net/url"

	"github.com/docuhub/docuhub/internal/models"
)

// Download abre el contenido del archivo en Drive y devuelve la respuesta HTTP
// sin leerla: el handler la canaliza directamente hacia el navegador con
// io.Copy, de modo que un archivo de 30 GB pasa por la laptop usando unos
// pocos KB de memoria y sin tocar el disco.
//
// `rangeHeader` se reenvía tal cual (por ejemplo "bytes=1048576-"), lo que
// habilita reanudar descargas y hacer seek en audio y video.
//
// Quien llame es responsable de cerrar resp.Body.
func (c *Client) Download(ctx context.Context, acc *models.DriveAccount, fileID, rangeHeader string) (*http.Response, error) {
	token, err := c.AccessToken(ctx, acc)
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Set("alt", "media")
	params.Set("supportsAllDrives", "true")
	// acknowledgeAbuseError permite bajar archivos que Google marcó como
	// sospechosos; son documentos propios de la empresa y el escaneo real
	// lo hace la Fase 4 con ClamAV.
	params.Set("acknowledgeAbuse", "true")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL(fileID, params), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, err
	}

	switch {
	case resp.StatusCode == http.StatusNotFound:
		resp.Body.Close()
		return nil, ErrNotFound
	case resp.StatusCode >= 400:
		err := apiError(resp)
		resp.Body.Close()
		return nil, err
	}
	return resp, nil
}
