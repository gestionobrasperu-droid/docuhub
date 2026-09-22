package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/docuhub/docuhub/internal/crypto"
	"github.com/docuhub/docuhub/internal/models"
	"github.com/docuhub/docuhub/internal/quota"
	"github.com/docuhub/docuhub/internal/repo"
	"github.com/google/uuid"
)

// ------------------------------------------------------------- usuarios ---

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.repo.ListUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudieron listar los usuarios")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"users":             users,
		"default_quota":     s.cfg.DefaultQuotaBytes,
		"default_bandwidth": s.cfg.DefaultBandwidthBytes,
	})
}

type createUserRequest struct {
	Email          string `json:"email"`
	Name           string `json:"name"`
	Password       string `json:"password"`
	Role           string `json:"role"`
	QuotaBytes     int64  `json:"quota_bytes"`
	BandwidthBytes int64  `json:"bandwidth_bytes"`
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	actor := userFrom(r.Context())
	var req createUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	if !strings.Contains(email, "@") || len(email) < 5 {
		writeErr(w, http.StatusBadRequest, "El correo no es válido")
		return
	}
	if !validRole(req.Role) {
		writeErr(w, http.StatusBadRequest, "El rol debe ser admin, manager, member o guest")
		return
	}
	// Solo un admin puede fabricar otro admin.
	if req.Role == models.RoleAdmin && actor.Role != models.RoleAdmin {
		writeErr(w, http.StatusForbidden, "Solo un administrador puede crear otro administrador")
		return
	}

	// Sin contraseña explícita se genera una y se devuelve una sola vez.
	password := req.Password
	generated := false
	if password == "" {
		var err error
		password, err = crypto.NewToken(9)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "No se pudo generar la contraseña")
			return
		}
		generated = true
	}
	if err := validatePassword(password); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	hash, err := crypto.HashPassword(password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo crear el usuario")
		return
	}

	user, err := s.repo.CreateUser(r.Context(), email, strings.TrimSpace(req.Name), hash,
		req.Role, req.QuotaBytes, req.BandwidthBytes, true)
	if err != nil {
		writeErr(w, http.StatusConflict, "Ya existe un usuario con ese correo")
		return
	}

	s.audit(r, "user.create", "user", user.ID.String(), user.Email, true,
		map[string]any{"rol": user.Role})

	out := map[string]any{"user": user}
	if generated {
		out["temporary_password"] = password
	}
	writeJSON(w, http.StatusCreated, out)
}

type updateUserRequest struct {
	Name           *string `json:"name"`
	Role           *string `json:"role"`
	Status         *string `json:"status"`
	QuotaBytes     *int64  `json:"quota_bytes"`
	BandwidthBytes *int64  `json:"bandwidth_bytes"`
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	actor := userFrom(r.Context())
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	var req updateUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Role != nil {
		if !validRole(*req.Role) {
			writeErr(w, http.StatusBadRequest, "Rol inválido")
			return
		}
		if *req.Role == models.RoleAdmin && actor.Role != models.RoleAdmin {
			writeErr(w, http.StatusForbidden, "Solo un administrador puede otorgar el rol de administrador")
			return
		}
	}
	if req.Status != nil && *req.Status != "active" && *req.Status != "suspended" {
		writeErr(w, http.StatusBadRequest, "El estado debe ser active o suspended")
		return
	}
	// Un administrador no puede suspenderse ni degradarse a sí mismo: evita
	// quedarse sin ninguna cuenta capaz de administrar.
	if id == actor.ID && (req.Status != nil || req.Role != nil) {
		writeErr(w, http.StatusBadRequest, "No puedes cambiar tu propio rol ni tu estado")
		return
	}

	user, err := s.repo.UpdateUser(r.Context(), id, req.Name, req.Role, req.Status,
		req.QuotaBytes, req.BandwidthBytes)
	if err != nil {
		writeErr(w, http.StatusNotFound, "El usuario no existe")
		return
	}
	if req.Status != nil && *req.Status == "suspended" {
		if err := s.repo.RevokeUserSessions(r.Context(), id); err != nil {
			writeErr(w, http.StatusInternalServerError, "Se suspendió pero no se pudieron cerrar sus sesiones")
			return
		}
	}

	s.audit(r, "user.update", "user", user.ID.String(), user.Email, true, map[string]any{
		"rol": user.Role, "estado": user.Status, "cuota": user.QuotaBytes,
	})
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	user, err := s.repo.UserByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "El usuario no existe")
		return
	}

	password, err := crypto.NewToken(9)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo generar la contraseña")
		return
	}
	hash, err := crypto.HashPassword(password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo generar la contraseña")
		return
	}
	if err := s.repo.SetPassword(r.Context(), id, hash, true); err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo cambiar la contraseña")
		return
	}
	if err := s.repo.RevokeUserSessions(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudieron cerrar sus sesiones")
		return
	}

	s.audit(r, "user.reset_password", "user", id.String(), user.Email, true, nil)
	writeJSON(w, http.StatusOK, map[string]any{"temporary_password": password})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	actor := userFrom(r.Context())
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	if id == actor.ID {
		writeErr(w, http.StatusBadRequest, "No puedes eliminar tu propia cuenta")
		return
	}
	user, err := s.repo.UserByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "El usuario no existe")
		return
	}
	if err := s.repo.DeleteUser(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo eliminar el usuario")
		return
	}

	s.audit(r, "user.delete", "user", id.String(), user.Email, true, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func validRole(role string) bool {
	switch role {
	case models.RoleAdmin, models.RoleManager, models.RoleMember, models.RoleGuest:
		return true
	}
	return false
}

// ------------------------------------------------ estadísticas y bitácora --

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	totals, err := s.repo.Totals(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudieron calcular las estadísticas")
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	traffic, err := s.repo.TrafficByDay(r.Context(), days)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo calcular el tráfico")
		return
	}
	usage, err := s.repo.UsageByUser(r.Context(), quota.MonthStart())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo calcular el consumo por usuario")
		return
	}
	top, err := s.repo.TopFiles(r.Context(), 10)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudieron calcular los archivos más usados")
		return
	}
	accounts, err := s.repo.ListDriveAccounts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo consultar Google Drive")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"totals":         totals,
		"traffic":        traffic,
		"usage_by_user":  usage,
		"top_files":      top,
		"drive_accounts": accounts,
	})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := repo.AuditFilter{Action: strings.TrimSpace(q.Get("action"))}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	f.Offset, _ = strconv.Atoi(q.Get("offset"))

	if v := q.Get("actor_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			f.ActorID = &id
		}
	}
	if v := q.Get("days"); v != "" {
		if days, err := strconv.Atoi(v); err == nil && days > 0 {
			since := time.Now().AddDate(0, 0, -days)
			f.Since = &since
		}
	}

	entries, err := s.repo.ListAudit(r.Context(), f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo leer la bitácora")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// ---------------------------------------------------------- Google Drive --

func (s *Server) handleDriveStatus(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.repo.ListDriveAccounts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudieron listar las cuentas")
		return
	}
	root, rootErr := s.repo.RootFolder(r.Context())

	out := map[string]any{
		"accounts":   accounts,
		"configured": s.cfg.DriveConfigured(),
		"redirect_uri": s.cfg.RedirectURI(),
	}
	if rootErr == nil {
		out["root_folder"] = root
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDriveConnect devuelve la URL de consentimiento de Google. El frontend
// redirige el navegador allí; no se abre desde fetch por política de Google.
func (s *Server) handleDriveConnect(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.DriveConfigured() {
		writeErr(w, http.StatusPreconditionFailed,
			"Faltan GOOGLE_CLIENT_ID y GOOGLE_CLIENT_SECRET en la configuración del servidor")
		return
	}
	state, err := s.oauthStateNew()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo iniciar la conexión")
		return
	}
	s.audit(r, "drive.connect_start", "drive", "", "", true, nil)
	writeJSON(w, http.StatusOK, map[string]any{"auth_url": s.drive.AuthURL(state)})
}

// handleDriveCallback recibe la vuelta de Google, guarda los tokens cifrados
// y prepara la carpeta raíz de la plataforma dentro del Drive.
func (s *Server) handleDriveCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if errMsg := q.Get("error"); errMsg != "" {
		http.Redirect(w, r, "/#/admin?drive_error="+errMsg, http.StatusFound)
		return
	}
	if !s.oauthStateConsume(q.Get("state")) {
		http.Redirect(w, r, "/#/admin?drive_error=estado_invalido", http.StatusFound)
		return
	}

	refresh, access, expiry, err := s.drive.ExchangeCode(r.Context(), q.Get("code"))
	if err != nil {
		http.Redirect(w, r, "/#/admin?drive_error="+urlSafe(err.Error()), http.StatusFound)
		return
	}

	email, name, err := s.drive.UserEmail(r.Context(), access)
	if err != nil || email == "" {
		email = "cuenta-google"
	}

	refreshEnc, err := s.enc.Encrypt(refresh)
	if err != nil {
		http.Redirect(w, r, "/#/admin?drive_error=cifrado", http.StatusFound)
		return
	}
	accessEnc, err := s.enc.Encrypt(access)
	if err != nil {
		http.Redirect(w, r, "/#/admin?drive_error=cifrado", http.StatusFound)
		return
	}

	acc, err := s.repo.UpsertDriveAccount(r.Context(), email, name, refreshEnc, accessEnc, expiry, s.cfg.DriveID)
	if err != nil {
		http.Redirect(w, r, "/#/admin?drive_error=guardado", http.StatusFound)
		return
	}

	// Carpeta raíz dentro de Drive (idempotente: si ya existe, la reutiliza).
	rootDrive, err := s.drive.EnsureFolder(r.Context(), acc, s.cfg.RootFolderName, "")
	if err != nil {
		_ = s.repo.MarkAccountError(r.Context(), acc.ID, err.Error())
		http.Redirect(w, r, "/#/admin?drive_error="+urlSafe(err.Error()), http.StatusFound)
		return
	}
	if err := s.repo.SetAccountRootFolder(r.Context(), acc.ID, rootDrive.ID); err != nil {
		http.Redirect(w, r, "/#/admin?drive_error=carpeta_raiz", http.StatusFound)
		return
	}

	// Carpeta raíz local: se crea la primera vez y se reengancha después.
	if root, err := s.repo.RootFolder(r.Context()); err == nil {
		_ = s.repo.AttachFolderToDrive(r.Context(), root.ID, rootDrive.ID, acc.ID)
	} else {
		if _, err := s.repo.CreateFolder(r.Context(), nil, s.cfg.RootFolderName, "/",
			rootDrive.ID, &acc.ID, nil, true); err != nil {
			http.Redirect(w, r, "/#/admin?drive_error=raiz_local", http.StatusFound)
			return
		}
	}

	if qta, err := s.drive.About(r.Context(), acc); err == nil {
		_ = s.repo.SetAccountQuota(r.Context(), acc.ID, qta.Limit, qta.Usage)
	}

	s.audit(r, "drive.connect", "drive", acc.ID.String(), email, true, nil)
	http.Redirect(w, r, "/#/admin?drive=conectado", http.StatusFound)
}

func (s *Server) handleDriveSetPrimary(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.repo.SetPrimaryAccount(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo cambiar la cuenta principal")
		return
	}
	s.audit(r, "drive.set_primary", "drive", id.String(), "", true, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDriveRefreshQuota(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	acc, err := s.repo.AccountByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "La cuenta no existe")
		return
	}
	q, err := s.drive.About(r.Context(), acc)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Google no respondió: "+err.Error())
		return
	}
	if err := s.repo.SetAccountQuota(r.Context(), acc.ID, q.Limit, q.Usage); err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo guardar la cuota")
		return
	}
	writeJSON(w, http.StatusOK, q)
}

func (s *Server) handleDriveDisconnect(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	acc, err := s.repo.AccountByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "La cuenta no existe")
		return
	}
	if err := s.repo.DeleteDriveAccount(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo desconectar la cuenta")
		return
	}
	// Los archivos siguen en Drive; solo se corta el enlace con la plataforma.
	s.audit(r, "drive.disconnect", "drive", id.String(), acc.Email, true, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ------------------------------------------------------------- permisos ---

func (s *Server) handleListPermissions(w http.ResponseWriter, r *http.Request) {
	resType := r.URL.Query().Get("resource_type")
	resID, err := uuid.Parse(r.URL.Query().Get("resource_id"))
	if err != nil || (resType != "folder" && resType != "file") {
		writeErr(w, http.StatusBadRequest, "Indica resource_type (folder o file) y resource_id")
		return
	}
	if !s.canManageResource(w, r, resType, resID) {
		return
	}

	perms, err := s.repo.ListPermissions(r.Context(), resType, resID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudieron listar los permisos")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"permissions": perms})
}

type grantRequest struct {
	SubjectEmail string     `json:"subject_email"`
	SubjectID    *uuid.UUID `json:"subject_id"`
	SubjectType  string     `json:"subject_type"`
	ResourceType string     `json:"resource_type"`
	ResourceID   uuid.UUID  `json:"resource_id"`
	Level        string     `json:"level"`
	ExpiresAt    *string    `json:"expires_at"`
}

func (s *Server) handleGrantPermission(w http.ResponseWriter, r *http.Request) {
	actor := userFrom(r.Context())
	var req grantRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ResourceType != "folder" && req.ResourceType != "file" {
		writeErr(w, http.StatusBadRequest, "resource_type debe ser folder o file")
		return
	}
	if _, ok := models.LevelRank[req.Level]; !ok {
		writeErr(w, http.StatusBadRequest, "El nivel debe ser viewer, downloader, editor o manager")
		return
	}
	if !s.canManageResource(w, r, req.ResourceType, req.ResourceID) {
		return
	}

	subjectType := req.SubjectType
	if subjectType == "" {
		subjectType = "user"
	}
	subjectID := uuid.Nil
	label := ""
	switch {
	case req.SubjectID != nil:
		subjectID = *req.SubjectID
	case subjectType == "user" && req.SubjectEmail != "":
		target, err := s.repo.UserByEmail(r.Context(), req.SubjectEmail)
		if err != nil {
			writeErr(w, http.StatusNotFound, "No hay ningún usuario con ese correo")
			return
		}
		subjectID = target.ID
		label = target.Email
	default:
		writeErr(w, http.StatusBadRequest, "Indica el correo del usuario o el id del grupo")
		return
	}

	perm, err := s.repo.GrantPermission(r.Context(), subjectType, subjectID,
		req.ResourceType, req.ResourceID, req.Level, &actor.ID, req.ExpiresAt)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo otorgar el permiso")
		return
	}
	perm.SubjectLabel = label

	s.audit(r, "permission.grant", req.ResourceType, req.ResourceID.String(), label, true,
		map[string]any{"nivel": req.Level, "sujeto": subjectID})
	writeJSON(w, http.StatusCreated, perm)
}

func (s *Server) handleRevokePermission(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	perm, err := s.repo.PermissionByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "El permiso no existe")
		return
	}
	if !s.canManageResource(w, r, perm.ResourceType, perm.ResourceID) {
		return
	}
	if err := s.repo.RevokePermission(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "No se pudo revocar el permiso")
		return
	}

	s.audit(r, "permission.revoke", perm.ResourceType, perm.ResourceID.String(), "", true,
		map[string]any{"nivel": perm.Level})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// canManageResource exige nivel de responsable sobre el recurso afectado.
func (s *Server) canManageResource(w http.ResponseWriter, r *http.Request, resType string, resID uuid.UUID) bool {
	user := userFrom(r.Context())

	var level string
	var err error
	if resType == "folder" {
		_, _, level, err = s.folderAccess(r.Context(), user, resID)
	} else {
		_, _, level, err = s.fileAccess(r.Context(), user, resID)
	}
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "El recurso no existe")
		} else {
			writeErr(w, http.StatusInternalServerError, "No se pudo verificar el recurso")
		}
		return false
	}
	if !atLeast(level, models.LevelManager) {
		writeErr(w, http.StatusForbidden, "Necesitas ser responsable de este recurso para gestionar sus permisos")
		return false
	}
	return true
}

// urlSafe recorta y limpia un mensaje para usarlo en el fragmento de una URL.
func urlSafe(msg string) string {
	msg = strings.ReplaceAll(msg, " ", "_")
	if len(msg) > 120 {
		msg = msg[:120]
	}
	return strings.Map(func(r rune) rune {
		if r < 32 || r == '#' || r == '&' || r == '?' || r == '/' {
			return '_'
		}
		return r
	}, msg)
}
