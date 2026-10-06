// Package drive habla directamente con el API REST de Google Drive v3.
//
// Se implementa a mano en lugar de usar la librería oficial por una razón
// concreta: necesitamos control fino del streaming (Range, chunks resumibles,
// reenvío sin bufferizar) y la librería oficial esconde el cuerpo HTTP.
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
	"sync"
	"time"

	"github.com/docuhub/docuhub/internal/crypto"
	"github.com/docuhub/docuhub/internal/models"
	"github.com/google/uuid"
)

const (
	apiBase     = "https://www.googleapis.com/drive/v3"
	uploadBase  = "https://www.googleapis.com/upload/drive/v3"
	tokenURL    = "https://oauth2.googleapis.com/token"
	authURL     = "https://accounts.google.com/o/oauth2/v2/auth"
	folderMime  = "application/vnd.google-apps.folder"
	userinfoURL = "https://www.googleapis.com/oauth2/v2/userinfo"
)

var (
	ErrNotFound   = errors.New("recurso no encontrado en Drive")
	ErrNoAccount  = errors.New("no hay ninguna cuenta de Google Drive conectada")
	ErrReauthNeed = errors.New("la cuenta de Google necesita volver a autorizarse")
)

// AccountStore es la parte del repositorio que el cliente necesita para
// persistir los access tokens renovados. Se define aquí (y no se importa el
// repo) para no crear un ciclo de dependencias.
type AccountStore interface {
	SaveAccessToken(ctx context.Context, id uuid.UUID, accessTokenEnc string, expiresAt time.Time) error
	MarkAccountError(ctx context.Context, id uuid.UUID, msg string) error
}

type Client struct {
	clientID     string
	clientSecret string
	scope        string
	redirectURI  string

	enc   *crypto.Encryptor
	store AccountStore

	httpc      *http.Client // sigue redirecciones (descargas)
	httpcNoRed *http.Client // no las sigue (chunks: el 308 es significativo)

	mu sync.Mutex // serializa el refresco de tokens por cuenta
}

func New(clientID, clientSecret, scope, redirectURI string, enc *crypto.Encryptor, store AccountStore) *Client {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
		WriteBufferSize:     64 << 10,
		ReadBufferSize:      64 << 10,
	}
	return &Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		scope:        scope,
		redirectURI:  redirectURI,
		enc:          enc,
		store:        store,
		// Sin Timeout global a propósito: una subida de 20 GB es legítima.
		// El control de tiempo lo lleva el context de cada petición.
		httpc: &http.Client{Transport: transport},
		httpcNoRed: &http.Client{
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// ------------------------------------------------------------------ OAuth --

// AuthURL arma la URL de consentimiento. `prompt=consent` fuerza a Google a
// devolver un refresh token incluso si la cuenta ya autorizó antes.
func (c *Client) AuthURL(state string) string {
	q := url.Values{}
	q.Set("client_id", c.clientID)
	q.Set("redirect_uri", c.redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", c.scope+" https://www.googleapis.com/auth/userinfo.email")
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	q.Set("include_granted_scopes", "true")
	q.Set("state", state)
	return authURL + "?" + q.Encode()
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// ExchangeCode canjea el código de la redirección por tokens.
func (c *Client) ExchangeCode(ctx context.Context, code string) (refreshToken, accessToken string, expiry time.Time, err error) {
	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	form.Set("redirect_uri", c.redirectURI)
	form.Set("grant_type", "authorization_code")

	tr, err := c.postToken(ctx, form)
	if err != nil {
		return "", "", time.Time{}, err
	}
	if tr.RefreshToken == "" {
		return "", "", time.Time{}, errors.New("Google no devolvió refresh token: revoca el acceso de la app en la cuenta y vuelve a conectar")
	}
	return tr.RefreshToken, tr.AccessToken, time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second), nil
}

func (c *Client) postToken(ctx context.Context, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var tr tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tr); err != nil {
		return nil, fmt.Errorf("respuesta de token ilegible: %w", err)
	}
	if tr.Error != "" {
		if tr.Error == "invalid_grant" {
			return nil, ErrReauthNeed
		}
		return nil, fmt.Errorf("google oauth: %s (%s)", tr.Error, tr.ErrorDesc)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google oauth devolvió %d", resp.StatusCode)
	}
	return &tr, nil
}

// AccessToken devuelve un token válido, renovándolo si está por vencer.
// Muta la cuenta en memoria para que la siguiente llamada reutilice el token.
func (c *Client) AccessToken(ctx context.Context, acc *models.DriveAccount) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if acc.AccessTokenEnc != "" && acc.AccessExpiresAt != nil &&
		time.Until(*acc.AccessExpiresAt) > 2*time.Minute {
		if tok, err := c.enc.Decrypt(acc.AccessTokenEnc); err == nil {
			return tok, nil
		}
		// Si no se puede descifrar (clave rotada), se renueva abajo.
	}

	refresh, err := c.enc.Decrypt(acc.RefreshTokenEnc)
	if err != nil {
		return "", fmt.Errorf("no se pudo descifrar el refresh token (¿cambió APP_ENCRYPTION_KEY?): %w", err)
	}

	form := url.Values{}
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	form.Set("refresh_token", refresh)
	form.Set("grant_type", "refresh_token")

	tr, err := c.postToken(ctx, form)
	if err != nil {
		if errors.Is(err, ErrReauthNeed) && c.store != nil {
			_ = c.store.MarkAccountError(ctx, acc.ID, "la autorización de Google fue revocada; vuelve a conectar la cuenta")
		}
		return "", err
	}

	exp := time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	encTok, err := c.enc.Encrypt(tr.AccessToken)
	if err != nil {
		return "", err
	}
	acc.AccessTokenEnc = encTok
	acc.AccessExpiresAt = &exp
	if c.store != nil {
		if err := c.store.SaveAccessToken(ctx, acc.ID, encTok, exp); err != nil {
			return "", err
		}
	}
	return tr.AccessToken, nil
}

// --------------------------------------------------------- peticiones API --

func (c *Client) do(ctx context.Context, acc *models.DriveAccount, method, urlStr string, body io.Reader, contentType string) (*http.Response, error) {
	token, err := c.AccessToken(ctx, acc)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.httpc.Do(req)
}

// doJSON ejecuta y decodifica, reintentando ante límites de cuota y errores 5xx.
func (c *Client) doJSON(ctx context.Context, acc *models.DriveAccount, method, urlStr string, payload any, out any) error {
	var raw []byte
	if payload != nil {
		var err error
		raw, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}

	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		var body io.Reader
		ct := ""
		if raw != nil {
			body = bytes.NewReader(raw)
			ct = "application/json"
		}
		resp, err := c.do(ctx, acc, method, urlStr, body, ct)
		if err != nil {
			return err
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = fmt.Errorf("drive devolvió %d", resp.StatusCode)
			if !sleepBackoff(ctx, attempt) {
				return ctx.Err()
			}
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			return ErrNotFound
		}
		if resp.StatusCode >= 400 {
			return apiError(resp)
		}
		if out == nil {
			return nil
		}
		return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
	}
	return lastErr
}

func sleepBackoff(ctx context.Context, attempt int) bool {
	d := time.Duration(1<<uint(attempt)) * time.Second
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func apiError(resp *http.Response) error {
	var e struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return fmt.Errorf("drive %d: %s", e.Error.Code, e.Error.Message)
	}
	return fmt.Errorf("drive %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

// ------------------------------------------------------------ operaciones --

type StorageQuota struct {
	Limit        int64  `json:"limit"`
	Usage        int64  `json:"usage"`
	UsageInDrive int64  `json:"usage_in_drive"`
	UserEmail    string `json:"user_email"`
	UserName     string `json:"user_name"`
	Unlimited    bool   `json:"unlimited"`
}

// About consulta espacio total y usado de la cuenta.
func (c *Client) About(ctx context.Context, acc *models.DriveAccount) (*StorageQuota, error) {
	var out struct {
		StorageQuota struct {
			Limit        string `json:"limit"`
			Usage        string `json:"usage"`
			UsageInDrive string `json:"usageInDrive"`
		} `json:"storageQuota"`
		User struct {
			EmailAddress string `json:"emailAddress"`
			DisplayName  string `json:"displayName"`
		} `json:"user"`
	}
	u := apiBase + "/about?fields=storageQuota,user"
	if err := c.doJSON(ctx, acc, http.MethodGet, u, nil, &out); err != nil {
		return nil, err
	}
	q := &StorageQuota{
		UserEmail: out.User.EmailAddress,
		UserName:  out.User.DisplayName,
	}
	q.Limit = parseInt64(out.StorageQuota.Limit)
	q.Usage = parseInt64(out.StorageQuota.Usage)
	q.UsageInDrive = parseInt64(out.StorageQuota.UsageInDrive)
	// Las cuentas de Workspace con almacenamiento ilimitado omiten "limit".
	q.Unlimited = out.StorageQuota.Limit == ""
	return q, nil
}

// UserEmail obtiene el correo de la cuenta recién autorizada.
func (c *Client) UserEmail(ctx context.Context, accessToken string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userinfoURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.httpc.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", "", apiError(resp)
	}
	var out struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", "", err
	}
	return out.Email, out.Name, nil
}

type DriveFile struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	MimeType    string   `json:"mimeType"`
	Size        string   `json:"size"`
	MD5Checksum string   `json:"md5Checksum"`
	Parents     []string `json:"parents"`
	Trashed     bool     `json:"trashed"`
}

func (f *DriveFile) SizeBytes() int64 { return parseInt64(f.Size) }

// CreateFolder crea una carpeta dentro de parentID (o en la raíz si va vacío).
func (c *Client) CreateFolder(ctx context.Context, acc *models.DriveAccount, name, parentID string) (*DriveFile, error) {
	payload := map[string]any{
		"name":     name,
		"mimeType": folderMime,
	}
	if parentID != "" {
		payload["parents"] = []string{parentID}
	} else if acc.DriveID != "" {
		payload["parents"] = []string{acc.DriveID}
	}
	var out DriveFile
	u := apiBase + "/files?supportsAllDrives=true&fields=id,name,mimeType,parents"
	if err := c.doJSON(ctx, acc, http.MethodPost, u, payload, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FindFolder busca una carpeta por nombre exacto dentro de un padre.
func (c *Client) FindFolder(ctx context.Context, acc *models.DriveAccount, name, parentID string) (*DriveFile, error) {
	q := fmt.Sprintf("name = '%s' and mimeType = '%s' and trashed = false",
		escapeQuery(name), folderMime)
	if parentID != "" {
		q += fmt.Sprintf(" and '%s' in parents", escapeQuery(parentID))
	}

	params := url.Values{}
	params.Set("q", q)
	params.Set("fields", "files(id,name,mimeType,parents)")
	params.Set("pageSize", "10")
	params.Set("supportsAllDrives", "true")
	params.Set("includeItemsFromAllDrives", "true")
	if acc.DriveID != "" {
		params.Set("corpora", "drive")
		params.Set("driveId", acc.DriveID)
	}

	var out struct {
		Files []DriveFile `json:"files"`
	}
	if err := c.doJSON(ctx, acc, http.MethodGet, apiBase+"/files?"+params.Encode(), nil, &out); err != nil {
		return nil, err
	}
	if len(out.Files) == 0 {
		return nil, ErrNotFound
	}
	return &out.Files[0], nil
}

// EnsureFolder devuelve la carpeta si existe y la crea si no. Idempotente.
func (c *Client) EnsureFolder(ctx context.Context, acc *models.DriveAccount, name, parentID string) (*DriveFile, error) {
	f, err := c.FindFolder(ctx, acc, name, parentID)
	if err == nil {
		return f, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return c.CreateFolder(ctx, acc, name, parentID)
}

// GetFile lee los metadatos de un archivo.
func (c *Client) GetFile(ctx context.Context, acc *models.DriveAccount, fileID string) (*DriveFile, error) {
	u := fmt.Sprintf("%s/files/%s?supportsAllDrives=true&fields=id,name,mimeType,size,md5Checksum,parents,trashed",
		apiBase, url.PathEscape(fileID))
	var out DriveFile
	if err := c.doJSON(ctx, acc, http.MethodGet, u, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Rename cambia el nombre en Drive para que coincida con el de la plataforma.
func (c *Client) Rename(ctx context.Context, acc *models.DriveAccount, fileID, newName string) error {
	u := fmt.Sprintf("%s/files/%s?supportsAllDrives=true", apiBase, url.PathEscape(fileID))
	return c.doJSON(ctx, acc, http.MethodPatch, u, map[string]any{"name": newName}, nil)
}

// Trash manda el archivo a la papelera de Drive (reversible durante 30 días).
// Se prefiere sobre el borrado definitivo: si alguien se equivoca, se recupera.
func (c *Client) Trash(ctx context.Context, acc *models.DriveAccount, fileID string) error {
	u := fmt.Sprintf("%s/files/%s?supportsAllDrives=true", apiBase, url.PathEscape(fileID))
	return c.doJSON(ctx, acc, http.MethodPatch, u, map[string]any{"trashed": true}, nil)
}

// Delete borra sin pasar por la papelera. Solo lo usa el purgado programado.
func (c *Client) Delete(ctx context.Context, acc *models.DriveAccount, fileID string) error {
	u := fmt.Sprintf("%s/files/%s?supportsAllDrives=true", apiBase, url.PathEscape(fileID))
	resp, err := c.do(ctx, acc, http.MethodDelete, u, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode >= 400 {
		return apiError(resp)
	}
	return nil
}

func parseInt64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// escapeQuery protege el lenguaje de consulta de Drive: las comillas simples
// y las barras invertidas se escapan con barra invertida.
func escapeQuery(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `'`, `\'`)
}
