// Package config carga toda la configuración desde variables de entorno.
// No hay archivos de configuración: en Docker las variables vienen del .env,
// y en desarrollo local se exportan en la terminal.
package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env     string // "dev" o "prod"
	Port    string
	BaseURL string // URL pública, debe coincidir con la redirect URI de Google

	DatabaseURL string

	// EncryptionKey cifra los refresh tokens de Google guardados en la base.
	EncryptionKey []byte

	GoogleClientID     string
	GoogleClientSecret string
	DriveScope         string
	DriveID            string // ID de la Unidad Compartida; vacío = "Mi unidad"
	RootFolderName     string

	SessionTTL   time.Duration
	CookieSecure bool
	CookieName   string

	// Valores por defecto para usuarios nuevos.
	DefaultQuotaBytes     int64
	DefaultBandwidthBytes int64

	// Tamaño máximo de un chunk de subida aceptado por el backend.
	MaxChunkBytes int64

	BootstrapAdminEmail    string
	BootstrapAdminPassword string

	TrustProxyHeaders bool
}

func Load() (*Config, error) {
	c := &Config{
		Env:                    env("APP_ENV", "dev"),
		Port:                   env("APP_PORT", "8080"),
		BaseURL:                strings.TrimSuffix(env("APP_BASE_URL", "http://localhost:8080"), "/"),
		DatabaseURL:            env("DATABASE_URL", "postgres://docuhub:docuhub@localhost:5432/docuhub?sslmode=disable"),
		GoogleClientID:         env("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret:     env("GOOGLE_CLIENT_SECRET", ""),
		DriveScope:             env("DRIVE_SCOPE", "https://www.googleapis.com/auth/drive"),
		DriveID:                env("DRIVE_ID", ""),
		RootFolderName:         env("DRIVE_ROOT_FOLDER", "DocuHub"),
		CookieName:             env("SESSION_COOKIE_NAME", "docuhub_session"),
		BootstrapAdminEmail:    env("BOOTSTRAP_ADMIN_EMAIL", "admin@localhost"),
		BootstrapAdminPassword: env("BOOTSTRAP_ADMIN_PASSWORD", ""),
	}

	keyHex := env("APP_ENCRYPTION_KEY", "")
	if keyHex == "" {
		return nil, fmt.Errorf("APP_ENCRYPTION_KEY es obligatoria (32 bytes en hexadecimal, 64 caracteres)")
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("APP_ENCRYPTION_KEY debe ser hexadecimal de 64 caracteres (32 bytes)")
	}
	c.EncryptionKey = key

	c.SessionTTL = envDuration("SESSION_TTL", 12*time.Hour)
	c.DefaultQuotaBytes = envBytes("DEFAULT_QUOTA_BYTES", 10<<30)         // 10 GiB
	c.DefaultBandwidthBytes = envBytes("DEFAULT_BANDWIDTH_BYTES", 50<<30) // 50 GiB/mes
	c.MaxChunkBytes = envBytes("MAX_CHUNK_BYTES", 16<<20)                 // 16 MiB
	c.CookieSecure = envBool("COOKIE_SECURE", strings.HasPrefix(c.BaseURL, "https://"))
	c.TrustProxyHeaders = envBool("TRUST_PROXY_HEADERS", true)

	return c, nil
}

// DriveConfigured indica si hay credenciales de Google cargadas.
func (c *Config) DriveConfigured() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != ""
}

func (c *Config) RedirectURI() string {
	return c.BaseURL + "/api/admin/drive/callback"
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := strings.ToLower(env(key, ""))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := env(key, ""); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// envBytes acepta un número plano o con sufijo: 10GB, 500MB, 2TB.
func envBytes(key string, def int64) int64 {
	raw := strings.ToUpper(strings.ReplaceAll(env(key, ""), " ", ""))
	if raw == "" {
		return def
	}
	mult := int64(1)
	switch {
	case strings.HasSuffix(raw, "TB"):
		mult, raw = 1<<40, strings.TrimSuffix(raw, "TB")
	case strings.HasSuffix(raw, "GB"):
		mult, raw = 1<<30, strings.TrimSuffix(raw, "GB")
	case strings.HasSuffix(raw, "MB"):
		mult, raw = 1<<20, strings.TrimSuffix(raw, "MB")
	case strings.HasSuffix(raw, "KB"):
		mult, raw = 1<<10, strings.TrimSuffix(raw, "KB")
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return def
	}
	return n * mult
}
