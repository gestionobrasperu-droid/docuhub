// Package models define el dominio. Las etiquetas JSON son el contrato con el
// frontend; los nombres de campo siguen el esquema de la base.
package models

import (
	"time"

	"github.com/google/uuid"
)

// Roles globales, de mayor a menor autoridad.
const (
	RoleAdmin   = "admin"
	RoleManager = "manager"
	RoleMember  = "member"
	RoleGuest   = "guest"
)

// Niveles de permiso sobre un recurso concreto.
const (
	LevelViewer     = "viewer"     // ve que el archivo existe y sus metadatos
	LevelDownloader = "downloader" // además puede descargarlo
	LevelEditor     = "editor"     // además sube, renombra y crea subcarpetas
	LevelManager    = "manager"    // además borra y otorga permisos
)

// LevelRank ordena los niveles para poder compararlos.
var LevelRank = map[string]int{
	LevelViewer:     1,
	LevelDownloader: 2,
	LevelEditor:     3,
	LevelManager:    4,
}

type User struct {
	ID             uuid.UUID  `json:"id"`
	Email          string     `json:"email"`
	Name           string     `json:"name"`
	PasswordHash   string     `json:"-"`
	Role           string     `json:"role"`
	Status         string     `json:"status"`
	QuotaBytes     int64      `json:"quota_bytes"`
	BandwidthBytes int64      `json:"bandwidth_bytes"`
	UsedBytes      int64      `json:"used_bytes"`
	MustChangePw   bool       `json:"must_change_password"`
	CreatedAt      time.Time  `json:"created_at"`
	LastLoginAt    *time.Time `json:"last_login_at,omitempty"`
}

// IsAtLeast compara el rol global contra un mínimo exigido.
func (u *User) IsAtLeast(role string) bool {
	rank := map[string]int{RoleGuest: 1, RoleMember: 2, RoleManager: 3, RoleAdmin: 4}
	return rank[u.Role] >= rank[role]
}

type Session struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type DriveAccount struct {
	ID              uuid.UUID  `json:"id"`
	Email           string     `json:"email"`
	DisplayName     string     `json:"display_name"`
	RefreshTokenEnc string     `json:"-"`
	AccessTokenEnc  string     `json:"-"`
	AccessExpiresAt *time.Time `json:"-"`
	DriveID         string     `json:"drive_id"`
	RootFolderID    string     `json:"root_folder_id"`
	QuotaTotalBytes int64      `json:"quota_total_bytes"`
	QuotaUsedBytes  int64      `json:"quota_used_bytes"`
	QuotaCheckedAt  *time.Time `json:"quota_checked_at,omitempty"`
	IsPrimary       bool       `json:"is_primary"`
	Status          string     `json:"status"`
	LastError       string     `json:"last_error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type Folder struct {
	ID             uuid.UUID  `json:"id"`
	ParentID       *uuid.UUID `json:"parent_id,omitempty"`
	Name           string     `json:"name"`
	Path           string     `json:"path"`
	DriveFolderID  string     `json:"-"`
	DriveAccountID *uuid.UUID `json:"-"`
	OwnerID        *uuid.UUID `json:"owner_id,omitempty"`
	IsRoot         bool       `json:"is_root"`
	Restricted     bool       `json:"restricted"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type File struct {
	ID             uuid.UUID  `json:"id"`
	FolderID       uuid.UUID  `json:"folder_id"`
	Name           string     `json:"name"`
	MimeType       string     `json:"mime_type"`
	SizeBytes      int64      `json:"size_bytes"`
	MD5            string     `json:"md5,omitempty"`
	DriveFileID    string     `json:"-"`
	DriveAccountID *uuid.UUID `json:"-"`
	OwnerID        *uuid.UUID `json:"owner_id,omitempty"`
	OwnerEmail     string     `json:"owner_email,omitempty"`
	Version        int        `json:"version"`
	DownloadCount  int64      `json:"download_count"`
	Status         string     `json:"status"`
	Tags           []string   `json:"tags"`
	Description    string     `json:"description"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type Permission struct {
	ID           uuid.UUID  `json:"id"`
	SubjectType  string     `json:"subject_type"`
	SubjectID    uuid.UUID  `json:"subject_id"`
	SubjectLabel string     `json:"subject_label,omitempty"`
	ResourceType string     `json:"resource_type"`
	ResourceID   uuid.UUID  `json:"resource_id"`
	Level        string     `json:"level"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

type ShareLink struct {
	ID            uuid.UUID  `json:"id"`
	Token         string     `json:"token,omitempty"` // solo se devuelve al crearlo
	URL           string     `json:"url,omitempty"`
	FileID        *uuid.UUID `json:"file_id,omitempty"`
	FolderID      *uuid.UUID `json:"folder_id,omitempty"`
	CreatedBy     *uuid.UUID `json:"created_by,omitempty"`
	HasPassword   bool       `json:"has_password"`
	MaxDownloads  int        `json:"max_downloads"`
	DownloadCount int        `json:"download_count"`
	AllowUpload   bool       `json:"allow_upload"`
	Note          string     `json:"note"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
}

type Upload struct {
	ID             uuid.UUID  `json:"id"`
	UserID         uuid.UUID  `json:"user_id"`
	FolderID       uuid.UUID  `json:"folder_id"`
	FileID         *uuid.UUID `json:"file_id,omitempty"`
	DriveAccountID *uuid.UUID `json:"-"`
	FileName       string     `json:"file_name"`
	MimeType       string     `json:"mime_type"`
	SizeBytes      int64      `json:"size_bytes"`
	BytesReceived  int64      `json:"bytes_received"`
	SessionURLEnc  string     `json:"-"`
	Status         string     `json:"status"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
}

type AuditEntry struct {
	ID           int64     `json:"id"`
	ActorEmail   string    `json:"actor_email"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	ResourceName string    `json:"resource_name"`
	IP           string    `json:"ip"`
	UserAgent    string    `json:"user_agent"`
	Success      bool      `json:"success"`
	Metadata     any       `json:"metadata"`
	CreatedAt    time.Time `json:"created_at"`
}
