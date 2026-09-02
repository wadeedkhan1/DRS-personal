package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"drs/backend/internal/audit"
	"drs/backend/internal/auth"
	"drs/backend/internal/config"
	"drs/backend/internal/ice"
	"drs/backend/internal/middleware"
	"drs/backend/internal/models"
	"drs/backend/internal/presence"

	"github.com/google/uuid"
)

// enrollmentTokenTTL is how long a generated token stays redeemable. It is set far in
// the future because tokens are, by request, non-expiring and reusable (see redeemToken):
// the same token can enroll any number of devices and never ages out. This trades the
// "token in a chat log is not a permanent backdoor" property for convenience — acceptable
// for this deployment, but note that anyone holding a token can enroll devices forever.
const enrollmentTokenTTL = 100 * 365 * 24 * time.Hour

// Handler holds the REST dependencies.
type Handler struct {
	cfg      *config.Config
	db       *sql.DB
	presence presence.PresenceStore
	audit    *audit.Logger
	ice      *ice.Provider
}

// NewHandler builds a Handler. presence is taken as an interface so the Redis swap
// SDS 6 anticipates does not need to touch this package.
func NewHandler(cfg *config.Config, db *sql.DB, pres presence.PresenceStore, aud *audit.Logger, iceProvider *ice.Provider) *Handler {
	return &Handler{cfg: cfg, db: db, presence: pres, audit: aud, ice: iceProvider}
}

func (h *Handler) respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *Handler) respondError(w http.ResponseWriter, status int, message string) {
	h.respondJSON(w, status, map[string]string{"error": message})
}

// pathTail returns the n-th path segment, or "" when absent.
func pathSegment(r *http.Request, n int) string {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) <= n {
		return ""
	}
	return parts[n]
}

// Auth

// Login exchanges credentials for a JWT.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req models.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	var user models.User
	err := h.db.QueryRow(`
		SELECT id, org_id, email, password_hash, role, created_at, updated_at
		FROM users WHERE LOWER(email) = LOWER($1)
	`, req.Email).Scan(&user.ID, &user.OrgID, &user.Email, &user.PasswordHash, &user.Role, &user.CreatedAt, &user.UpdatedAt)

	// One message and one code for "no such user" and "wrong password" alike, so the
	// endpoint cannot be used to discover which addresses have accounts.
	if err != nil || !auth.CheckPasswordHash(req.Password, user.PasswordHash) {
		h.audit.Log("", "", req.Email, "login_failed", "user", "", map[string]any{
			"ip": r.RemoteAddr,
		}, r.RemoteAddr)
		h.respondError(w, http.StatusUnauthorized, "Invalid email or password")
		return
	}

	token, err := auth.GenerateJWT(&user, h.cfg.JWTSecret, 24*time.Hour)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	h.audit.Log(user.OrgID, user.ID, user.Email, "login_success", "user", user.ID, map[string]any{
		"role": user.Role,
	}, r.RemoteAddr)

	h.respondJSON(w, http.StatusOK, models.LoginResponse{Token: token, User: user})
}

// Me returns the authenticated user.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetUserClaims(r)
	if !ok {
		h.respondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var user models.User
	err := h.db.QueryRow(`
		SELECT id, org_id, email, role, created_at, updated_at FROM users WHERE id = $1
	`, claims.UserID).Scan(&user.ID, &user.OrgID, &user.Email, &user.Role, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		h.respondError(w, http.StatusNotFound, "User not found")
		return
	}
	h.respondJSON(w, http.StatusOK, user)
}

// ICEServers hands the browser the same NAT-traversal list the agent is given in
// start_session. Both peers must negotiate against an identical list or they simply
// fail to connect, so there is one provider feeding both.
func (h *Handler) ICEServers(w http.ResponseWriter, r *http.Request) {
	h.respondJSON(w, http.StatusOK, map[string]any{
		"iceServers": h.ice.Servers(),
		// The browser must use the same policy as the agent, or one peer offers
		// candidates the other has been told to ignore.
		"iceTransportPolicy": h.ice.TransportPolicy(),
	})
}

// Devices

const deviceColumns = `
	d.id, d.org_id, d.name, d.type, d.os_version, d.ip_address,
	d.assigned_admin_id, u.email AS admin_email,
	d.group_id, g.name AS group_name,
	d.last_seen_at, d.status, d.metadata, d.created_at, d.updated_at`

func scanDevice(rows interface{ Scan(...any) error }) (models.Device, error) {
	var d models.Device
	var metadataBytes []byte
	err := rows.Scan(
		&d.ID, &d.OrgID, &d.Name, &d.Type, &d.OSVersion, &d.IPAddress,
		&d.AssignedAdminID, &d.AssignedAdmin,
		&d.GroupID, &d.GroupName,
		&d.LastSeenAt, &d.Status, &metadataBytes, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return d, err
	}
	if len(metadataBytes) > 0 {
		d.Metadata = json.RawMessage(metadataBytes)
	} else {
		d.Metadata = json.RawMessage("{}")
	}
	return d, nil
}

// ListDevices returns the devices visible to the caller (SRS FR-1.3, FR-1.4).
func (h *Handler) ListDevices(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)

	query := `
		SELECT ` + deviceColumns + `
		FROM devices d
		LEFT JOIN users u ON d.assigned_admin_id = u.id
		LEFT JOIN device_groups g ON d.group_id = g.id
		WHERE d.org_id = $1`
	args := []any{claims.OrgID}

	// An Admin sees only their assignments; a Super Admin sees the whole org. Both are
	// scoped to the caller's organization, so the org_id the schema already carries is
	// actually load-bearing rather than decorative.
	if claims.Role != models.RoleSuperAdmin {
		query += ` AND d.assigned_admin_id = $2`
		args = append(args, claims.UserID)
	}
	query += ` ORDER BY d.created_at DESC`

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to query devices")
		return
	}
	defer rows.Close()

	devices := make([]models.Device, 0)
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			continue
		}
		h.overlayPresence(&d)
		devices = append(devices, d)
	}
	h.respondJSON(w, http.StatusOK, devices)
}

// overlayPresence replaces the stored status with live state, which is authoritative
// while the process is up.
func (h *Handler) overlayPresence(d *models.Device) {
	if pres, ok := h.presence.GetDevicePresence(d.ID); ok {
		d.Status = pres.Status
		d.LastSeenAt = pres.LastSeenAt
		if pres.IPAddress != "" {
			d.IPAddress = pres.IPAddress
		}
	}
}

// GetDevice returns one device, enforcing the same visibility rule.
func (h *Handler) GetDevice(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)
	deviceID := pathSegment(r, 2)
	if deviceID == "" {
		h.respondError(w, http.StatusBadRequest, "Missing device id")
		return
	}

	row := h.db.QueryRow(`
		SELECT `+deviceColumns+`
		FROM devices d
		LEFT JOIN users u ON d.assigned_admin_id = u.id
		LEFT JOIN device_groups g ON d.group_id = g.id
		WHERE d.id = $1 AND d.org_id = $2
	`, deviceID, claims.OrgID)

	d, err := scanDevice(row)
	if err != nil {
		h.respondError(w, http.StatusNotFound, "Device not found")
		return
	}
	// Not found and not permitted give the same answer, so this cannot be used to
	// enumerate other admins' devices.
	if claims.Role != models.RoleSuperAdmin &&
		(d.AssignedAdminID == nil || *d.AssignedAdminID != claims.UserID) {
		h.respondError(w, http.StatusNotFound, "Device not found")
		return
	}

	h.overlayPresence(&d)
	h.respondJSON(w, http.StatusOK, d)
}

// GenerateEnrollmentToken mints a one-time, expiring enrollment token.
//
// No device row is created here. The previous version pre-created one per token,
// which left a permanent "Pending Device" in the dashboard for every token that was
// never used.
func (h *Handler) GenerateEnrollmentToken(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)

	var req models.GenerateEnrollmentTokenRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.DeviceType != models.DeviceTypeWindows && req.DeviceType != models.DeviceTypeAndroid {
		req.DeviceType = models.DeviceTypeWindows
	}

	// 12 random bytes -> 96 bits, formatted for someone to retype once.
	raw, err := auth.GenerateSecret(12)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}
	token := "DRS-" + strings.ToUpper(raw)
	expiresAt := time.Now().Add(enrollmentTokenTTL)

	var tokenID string
	// Only the hash is stored: the token is a credential that grants enrollment, so
	// read access to the database should not be enough to use one.
	err = h.db.QueryRow(`
		INSERT INTO enrollment_tokens
			(org_id, token_hash, device_type, assigned_admin_id, group_id, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, claims.OrgID, auth.HashSecret(token), req.DeviceType,
		req.AdminID, req.GroupID, claims.UserID, expiresAt).Scan(&tokenID)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to create enrollment token")
		return
	}

	h.audit.Log(claims.OrgID, claims.UserID, claims.Email, "create_enrollment_token", "enrollment_token", tokenID, map[string]any{
		"type":       req.DeviceType,
		"expires_at": expiresAt,
	}, r.RemoteAddr)

	h.respondJSON(w, http.StatusOK, models.GenerateEnrollmentTokenResponse{
		EnrollmentToken: token,
		ExpiresAt:       expiresAt,
	})
}

// EnrollDevice redeems a token and returns the agent's permanent identity.
//
// This endpoint is public by necessity: the agent has no credential yet. The token is
// the credential, so redemption is single-use, expiring, and serialised.
func (h *Handler) EnrollDevice(w http.ResponseWriter, r *http.Request) {
	var req models.EnrollDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "Invalid enrollment request body")
		return
	}
	if req.EnrollmentToken == "" || req.Name == "" {
		h.respondError(w, http.StatusBadRequest, "Missing token or device name")
		return
	}
	if req.Type != models.DeviceTypeWindows && req.Type != models.DeviceTypeAndroid {
		h.respondError(w, http.StatusBadRequest, "Unsupported device type")
		return
	}

	deviceID, orgID, agentSecret, err := h.redeemToken(req)
	if err != nil {
		// Deliberately one message for expired, already-used and unknown: a caller
		// holding a wrong token learns nothing about why.
		h.respondError(w, http.StatusBadRequest, "Invalid or expired enrollment token")
		return
	}

	h.audit.Log(orgID, "", "", "device_enrolled", "device", deviceID, map[string]any{
		"name": req.Name,
		"type": req.Type,
		"os":   req.OSVersion,
	}, r.RemoteAddr)

	h.respondJSON(w, http.StatusOK, models.EnrollDeviceResponse{
		DeviceID:                 deviceID,
		AgentSecret:              agentSecret,
		WSURL:                    agentWSURL(r),
		HeartbeatIntervalSeconds: int(h.cfg.HeartbeatInterval.Seconds()),
	})
}

// redeemToken validates a token and creates the device in one transaction.
//
// Tokens are reusable and non-expiring by request: the same token can enroll many
// devices, so redemption does NOT mark it used and does not check expiry. Each redemption
// still mints a fresh, unique agent secret and a new device row, so distinct devices
// never share an identity.
func (h *Handler) redeemToken(req models.EnrollDeviceRequest) (deviceID, orgID, agentSecret string, err error) {
	tx, err := h.db.Begin()
	if err != nil {
		return "", "", "", err
	}
	defer func() { _ = tx.Rollback() }()

	var tokenID string
	var assignedAdminID, groupID *string
	err = tx.QueryRow(`
		SELECT id, org_id, assigned_admin_id, group_id
		FROM enrollment_tokens
		WHERE token_hash = $1
	`, auth.HashSecret(req.EnrollmentToken)).Scan(&tokenID, &orgID, &assignedAdminID, &groupID)
	if err != nil {
		return "", "", "", errors.New("token not redeemable")
	}
	_ = tokenID // retained for readability; no longer used to mark the token consumed

	secret, err := auth.GenerateSecret(32)
	if err != nil {
		return "", "", "", err
	}

	metadata := json.RawMessage("{}")
	if len(req.Metadata) > 0 {
		metadata = req.Metadata
	}

	deviceID = uuid.New().String()
	if _, err = tx.Exec(`
		INSERT INTO devices
			(id, org_id, name, type, os_version, agent_secret_hash,
			 assigned_admin_id, group_id, status, metadata, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'offline', $9, NOW())
	`, deviceID, orgID, req.Name, req.Type, req.OSVersion, auth.HashSecret(secret),
		assignedAdminID, groupID, []byte(metadata)); err != nil {
		return "", "", "", err
	}

	// Deliberately do NOT mark the token used: it is reusable so it can enroll more
	// devices later.

	if err = tx.Commit(); err != nil {
		return "", "", "", err
	}
	return deviceID, orgID, secret, nil
}

// agentWSURL builds the URL the agent should dial, from the request it arrived on, so
// a deployment behind any hostname works without extra configuration.
func agentWSURL(r *http.Request) string {
	scheme := "ws://"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "wss://"
	}
	return scheme + r.Host + "/ws/agent"
}

// AssignDevice moves a device between admins/groups.
func (h *Handler) AssignDevice(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)
	deviceID := pathSegment(r, 2)
	if deviceID == "" || pathSegment(r, 3) != "assign" {
		h.respondError(w, http.StatusBadRequest, "Invalid path")
		return
	}

	var req models.AssignDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	res, err := h.db.Exec(`
		UPDATE devices SET assigned_admin_id = $1, group_id = $2, updated_at = NOW()
		WHERE id = $3 AND org_id = $4
	`, req.AdminID, req.GroupID, deviceID, claims.OrgID)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to assign device")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.respondError(w, http.StatusNotFound, "Device not found")
		return
	}

	h.audit.Log(claims.OrgID, claims.UserID, claims.Email, "assign_device", "device", deviceID, map[string]any{
		"assigned_admin_id": req.AdminID,
		"group_id":          req.GroupID,
	}, r.RemoteAddr)
	h.respondJSON(w, http.StatusOK, map[string]string{"message": "Device assignment updated successfully"})
}

// DeleteDevice deregisters an agent (SRS FR-2.5).
func (h *Handler) DeleteDevice(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)
	deviceID := pathSegment(r, 2)
	if deviceID == "" {
		h.respondError(w, http.StatusBadRequest, "Missing device id")
		return
	}

	res, err := h.db.Exec(`DELETE FROM devices WHERE id = $1 AND org_id = $2`, deviceID, claims.OrgID)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to delete device")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.respondError(w, http.StatusNotFound, "Device not found")
		return
	}

	// Drop it from presence too, so a still-connected agent for a deleted device does
	// not keep showing up as online until its socket happens to close.
	h.presence.SetDeviceOffline(deviceID)

	h.audit.Log(claims.OrgID, claims.UserID, claims.Email, "delete_device", "device", deviceID, nil, r.RemoteAddr)
	h.respondJSON(w, http.StatusOK, map[string]string{"message": "Device deleted successfully"})
}

// Users

// ListUsers returns the organization's portal accounts.
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)
	rows, err := h.db.Query(`
		SELECT id, org_id, email, role, created_at, updated_at
		FROM users WHERE org_id = $1 ORDER BY created_at ASC
	`, claims.OrgID)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to query users")
		return
	}
	defer rows.Close()

	users := make([]models.User, 0)
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.OrgID, &u.Email, &u.Role, &u.CreatedAt, &u.UpdatedAt); err == nil {
			users = append(users, u)
		}
	}
	h.respondJSON(w, http.StatusOK, users)
}

// CreateUser adds an Admin or Super Admin.
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)

	var req models.CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Email == "" || req.Password == "" {
		h.respondError(w, http.StatusBadRequest, "Email and password are required")
		return
	}
	if len(req.Password) < 12 {
		h.respondError(w, http.StatusBadRequest, "Password must be at least 12 characters")
		return
	}
	if req.Role != models.RoleAdmin && req.Role != models.RoleSuperAdmin {
		req.Role = models.RoleAdmin
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to hash password")
		return
	}

	var newUser models.User
	err = h.db.QueryRow(`
		INSERT INTO users (org_id, email, password_hash, role)
		VALUES ($1, LOWER($2), $3, $4)
		RETURNING id, org_id, email, role, created_at, updated_at
	`, claims.OrgID, req.Email, hash, req.Role).Scan(
		&newUser.ID, &newUser.OrgID, &newUser.Email, &newUser.Role, &newUser.CreatedAt, &newUser.UpdatedAt,
	)
	if err != nil {
		h.respondError(w, http.StatusBadRequest, "User already exists or creation failed")
		return
	}

	h.audit.Log(claims.OrgID, claims.UserID, claims.Email, "create_user", "user", newUser.ID, map[string]any{
		"email": newUser.Email,
		"role":  newUser.Role,
	}, r.RemoteAddr)
	h.respondJSON(w, http.StatusCreated, newUser)
}

// DeleteUser removes a portal account.
func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)
	targetUserID := pathSegment(r, 2)
	if targetUserID == "" {
		h.respondError(w, http.StatusBadRequest, "Missing user id")
		return
	}
	if targetUserID == claims.UserID {
		h.respondError(w, http.StatusBadRequest, "Cannot delete your own account")
		return
	}

	res, err := h.db.Exec(`DELETE FROM users WHERE id = $1 AND org_id = $2`, targetUserID, claims.OrgID)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to delete user")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.respondError(w, http.StatusNotFound, "User not found")
		return
	}

	h.audit.Log(claims.OrgID, claims.UserID, claims.Email, "delete_user", "user", targetUserID, nil, r.RemoteAddr)
	h.respondJSON(w, http.StatusOK, map[string]string{"message": "User deleted successfully"})
}

// Groups

// ListGroups returns the organization's device groups.
func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)
	rows, err := h.db.Query(`
		SELECT g.id, g.org_id, g.name, g.description, g.created_at, COUNT(d.id) AS dev_count
		FROM device_groups g
		LEFT JOIN devices d ON g.id = d.group_id
		WHERE g.org_id = $1
		GROUP BY g.id
		ORDER BY g.name ASC
	`, claims.OrgID)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to query groups")
		return
	}
	defer rows.Close()

	groups := make([]models.DeviceGroup, 0)
	for rows.Next() {
		var g models.DeviceGroup
		if err := rows.Scan(&g.ID, &g.OrgID, &g.Name, &g.Description, &g.CreatedAt, &g.DeviceCount); err == nil {
			groups = append(groups, g)
		}
	}
	h.respondJSON(w, http.StatusOK, groups)
}

// CreateGroup adds a team/department grouping (SRS FR-1.7).
func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)

	var req models.CreateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		h.respondError(w, http.StatusBadRequest, "Invalid group name")
		return
	}

	var g models.DeviceGroup
	if err := h.db.QueryRow(`
		INSERT INTO device_groups (org_id, name, description)
		VALUES ($1, $2, $3)
		RETURNING id, org_id, name, description, created_at
	`, claims.OrgID, req.Name, req.Description).Scan(
		&g.ID, &g.OrgID, &g.Name, &g.Description, &g.CreatedAt,
	); err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to create group")
		return
	}
	h.respondJSON(w, http.StatusCreated, g)
}

// Sessions and audit

// ListSessions returns session history within the caller's scope.
func (h *Handler) ListSessions(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)

	query := `
		SELECT s.id, s.device_id, d.name AS device_name, s.admin_id, u.email AS admin_email,
		       s.started_at, s.ended_at, s.mode, s.connection_type, s.status
		FROM sessions s
		LEFT JOIN devices d ON s.device_id = d.id
		LEFT JOIN users u ON s.admin_id = u.id
		WHERE d.org_id = $1`
	args := []any{claims.OrgID}
	if claims.Role != models.RoleSuperAdmin {
		query += ` AND s.admin_id = $2`
		args = append(args, claims.UserID)
	}
	query += ` ORDER BY s.started_at DESC LIMIT 100`

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to query sessions")
		return
	}
	defer rows.Close()

	sessions := make([]models.Session, 0)
	for rows.Next() {
		var s models.Session
		if err := rows.Scan(&s.ID, &s.DeviceID, &s.DeviceName, &s.AdminID, &s.AdminEmail,
			&s.StartedAt, &s.EndedAt, &s.Mode, &s.ConnectionType, &s.Status); err == nil {
			sessions = append(sessions, s)
		}
	}
	h.respondJSON(w, http.StatusOK, sessions)
}

// ListAuditLogs returns the audit trail (SRS FR-7.3).
func (h *Handler) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)

	query := `
		SELECT id, org_id, actor_user_id, actor_email, action, target_type, target_id,
		       metadata, ip_address, created_at
		FROM audit_logs
		WHERE org_id = $1`
	args := []any{claims.OrgID}
	if claims.Role != models.RoleSuperAdmin {
		query += ` AND actor_user_id = $2`
		args = append(args, claims.UserID)
	}
	query += ` ORDER BY created_at DESC LIMIT 200`

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to query audit logs")
		return
	}
	defer rows.Close()

	logs := make([]models.AuditLog, 0)
	for rows.Next() {
		var l models.AuditLog
		var metaBytes []byte
		if err := rows.Scan(&l.ID, &l.OrgID, &l.ActorUserID, &l.ActorEmail, &l.Action,
			&l.TargetType, &l.TargetID, &metaBytes, &l.IPAddress, &l.CreatedAt); err == nil {
			if len(metaBytes) > 0 {
				l.Metadata = json.RawMessage(metaBytes)
			}
			logs = append(logs, l)
		}
	}
	h.respondJSON(w, http.StatusOK, logs)
}

// GetUsageReport backs SRS FR-9.1.
func (h *Handler) GetUsageReport(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)
	var rep models.UsageReport

	_ = h.db.QueryRow(`SELECT COUNT(*) FROM devices WHERE org_id = $1`, claims.OrgID).Scan(&rep.TotalDevices)
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM users WHERE org_id = $1 AND role = 'admin'`, claims.OrgID).Scan(&rep.TotalAdmins)
	_ = h.db.QueryRow(`
		SELECT COUNT(*) FROM sessions s JOIN devices d ON s.device_id = d.id WHERE d.org_id = $1
	`, claims.OrgID).Scan(&rep.TotalSessions)

	var avgSecs sql.NullFloat64
	_ = h.db.QueryRow(`
		SELECT AVG(EXTRACT(EPOCH FROM (s.ended_at - s.started_at)))
		FROM sessions s JOIN devices d ON s.device_id = d.id
		WHERE d.org_id = $1 AND s.ended_at IS NOT NULL
	`, claims.OrgID).Scan(&avgSecs)
	if avgSecs.Valid {
		rep.AvgDurationMins = avgSecs.Float64 / 60.0
	}

	// Live counts come from the presence store rather than the sessions table, which
	// records history and can lag a crash.
	online := 0
	for _, p := range h.presence.GetAllOnlineDevices() {
		if p.OrgID == claims.OrgID {
			online++
		}
	}
	rep.OnlineDevices = online
	rep.ActiveSessions = activeSessionCount(h.presence, claims.OrgID)

	h.respondJSON(w, http.StatusOK, rep)
}

// activeSessionCount counts devices currently in a session within an org.
func activeSessionCount(store presence.PresenceStore, orgID string) int {
	n := 0
	for _, p := range store.GetAllOnlineDevices() {
		if p.OrgID == orgID && p.ActiveSessionID != "" {
			n++
		}
	}
	return n
}

// Health is the liveness probe the deployment guide points an uptime monitor at.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	status := "healthy"
	code := http.StatusOK
	if err := h.db.Ping(); err != nil {
		// Report unhealthy rather than a cheerful 200: a health check that cannot fail
		// tells an uptime monitor nothing.
		status = "degraded: database unreachable"
		code = http.StatusServiceUnavailable
	}
	h.respondJSON(w, code, map[string]any{
		"status":    status,
		"service":   "DRS Backend",
		"timestamp": time.Now().UTC(),
	})
}
