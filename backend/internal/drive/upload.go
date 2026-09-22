package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/docuhub/docuhub/internal/models"
)

// ErrUploadExpired: Google descarta las sesiones resumibles tras una semana,
// o antes si se aborta. Hay que empezar de nuevo.
var ErrUploadExpired = errors.New("la sesión de subida expiró o fue cancelada")

// StartResumable abre una sesión de subida y devuelve su URL, que es un
// secreto de un solo uso: quien la tenga puede escribir ese archivo.
func (c *Client) StartResumable(ctx context.Context, acc *models.DriveAccount, name, mimeType, parentID string, size int64) (string, error) {
	meta := map[string]any{"name": name}
	if parentID != "" {
		meta["parents"] = []string{parentID}
	}
	if mimeType != "" {
		meta["mimeType"] = mimeType
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}

	token, err := c.AccessToken(ctx, acc)
	if err != nil {
		return "", err
	}

	u := uploadBase + "/files?uploadType=resumable&supportsAllDrives=true&fields=id,name,size,md5Checksum,mimeType"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	if mimeType != "" {
		req.Header.Set("X-Upload-Content-Type", mimeType)
	}
	if size > 0 {
		req.Header.Set("X-Upload-Content-Length", strconv.FormatInt(size, 10))
	}

	resp, err := c.httpcNoRed.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", apiError(resp)
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", errors.New("Drive no devolvió la URL de la sesión de subida")
	}
	return loc, nil
}

// ChunkResult describe en qué quedó la sesión tras enviar un trozo.
type ChunkResult struct {
	Complete      bool       `json:"complete"`
	BytesReceived int64      `json:"bytes_received"` // siguiente offset que Drive espera
	File          *DriveFile `json:"file,omitempty"`
}

// UploadChunk reenvía un trozo del archivo a Drive sin guardarlo en disco.
//
// `body` se consume en streaming: la memoria usada es la del buffer de copia
// del transporte, no el tamaño del chunk. `offset` es el byte inicial dentro
// del archivo completo y `total` su tamaño final (-1 si se desconoce).
func (c *Client) UploadChunk(ctx context.Context, sessionURL string, offset, chunkLen, total int64, body io.Reader) (*ChunkResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, sessionURL, body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = chunkLen

	totalStr := "*"
	if total >= 0 {
		totalStr = strconv.FormatInt(total, 10)
	}
	req.Header.Set("Content-Range",
		fmt.Sprintf("bytes %d-%d/%s", offset, offset+chunkLen-1, totalStr))

	resp, err := c.httpcNoRed.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return parseChunkResponse(resp)
}

// ResumeOffset pregunta a Drive cuántos bytes tiene ya confirmados. Es lo que
// permite continuar una subida interrumpida por un corte de red o de luz.
func (c *Client) ResumeOffset(ctx context.Context, sessionURL string, total int64) (*ChunkResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, sessionURL, nil)
	if err != nil {
		return nil, err
	}
	req.ContentLength = 0
	totalStr := "*"
	if total >= 0 {
		totalStr = strconv.FormatInt(total, 10)
	}
	req.Header.Set("Content-Range", "bytes */"+totalStr)

	resp, err := c.httpcNoRed.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return parseChunkResponse(resp)
}

// AbortUpload cancela la sesión y libera los bytes parciales en Google.
func (c *Client) AbortUpload(ctx context.Context, sessionURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, sessionURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Length", "0")
	resp, err := c.httpcNoRed.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// 499 es el código propio de Google para "cancelada con éxito".
	if resp.StatusCode == 499 || resp.StatusCode < 400 || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return apiError(resp)
}

func parseChunkResponse(resp *http.Response) (*ChunkResult, error) {
	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated:
		var f DriveFile
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&f); err != nil {
			return nil, fmt.Errorf("subida terminada pero la respuesta es ilegible: %w", err)
		}
		return &ChunkResult{Complete: true, BytesReceived: f.SizeBytes(), File: &f}, nil

	// 308 "Resume Incomplete": Drive aceptó el trozo y espera el siguiente.
	case resp.StatusCode == http.StatusPermanentRedirect:
		received := int64(0)
		if r := resp.Header.Get("Range"); r != "" {
			// Formato: "bytes=0-262143"
			if idx := strings.LastIndex(r, "-"); idx >= 0 {
				if n, err := strconv.ParseInt(r[idx+1:], 10, 64); err == nil {
					received = n + 1
				}
			}
		}
		return &ChunkResult{Complete: false, BytesReceived: received}, nil

	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return nil, ErrUploadExpired

	default:
		return nil, apiError(resp)
	}
}

// UploadSmall sube un archivo pequeño de una sola vez (multipart). Se usa para
// los respaldos de la base y para archivos por debajo de unos pocos MB.
func (c *Client) UploadSmall(ctx context.Context, acc *models.DriveAccount, name, mimeType, parentID string, data []byte) (*DriveFile, error) {
	sessionURL, err := c.StartResumable(ctx, acc, name, mimeType, parentID, int64(len(data)))
	if err != nil {
		return nil, err
	}
	res, err := c.UploadChunk(ctx, sessionURL, 0, int64(len(data)), int64(len(data)), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if !res.Complete {
		return nil, errors.New("la subida no se completó")
	}
	return res.File, nil
}

// fileURL arma la URL de un archivo concreto del API.
func fileURL(fileID string, params url.Values) string {
	u := apiBase + "/files/" + url.PathEscape(fileID)
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	return u
}
