package drive

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/docuhub/docuhub/internal/models"
)

// ListChildren devuelve el contenido directo de una carpeta de Drive, página
// a página. Es lo que permite descubrir archivos que alguien subió a mano
// desde drive.google.com y que la plataforma todavía no conoce.
//
// Devuelve los elementos y el token de la página siguiente ("" si terminó).
func (c *Client) ListChildren(ctx context.Context, acc *models.DriveAccount, folderID, pageToken string) ([]DriveFile, string, error) {
	if folderID == "" {
		return nil, "", fmt.Errorf("se necesita el id de la carpeta")
	}

	params := url.Values{}
	params.Set("q", fmt.Sprintf("'%s' in parents and trashed = false", escapeQuery(folderID)))
	params.Set("fields", "nextPageToken, files(id,name,mimeType,size,md5Checksum,parents,trashed)")
	params.Set("pageSize", "200")
	params.Set("orderBy", "folder,name")
	params.Set("supportsAllDrives", "true")
	params.Set("includeItemsFromAllDrives", "true")
	if acc.DriveID != "" {
		params.Set("corpora", "drive")
		params.Set("driveId", acc.DriveID)
	}
	if pageToken != "" {
		params.Set("pageToken", pageToken)
	}

	var out struct {
		NextPageToken string      `json:"nextPageToken"`
		Files         []DriveFile `json:"files"`
	}
	if err := c.doJSON(ctx, acc, http.MethodGet, apiBase+"/files?"+params.Encode(), nil, &out); err != nil {
		return nil, "", err
	}
	return out.Files, out.NextPageToken, nil
}

// IsFolder distingue carpetas de archivos por su tipo MIME.
func (f *DriveFile) IsFolder() bool { return f.MimeType == folderMime }

// IsGoogleDoc indica si es un documento nativo de Google (Docs, Sheets…).
// Estos no tienen bytes descargables directamente: hay que exportarlos, y su
// tamaño no cuenta contra la cuota de la misma manera.
func (f *DriveFile) IsGoogleDoc() bool {
	return len(f.MimeType) > 28 && f.MimeType[:28] == "application/vnd.google-apps."
}
