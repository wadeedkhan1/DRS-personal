package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/lib/pq"
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
	d.allow_screen, d.allow_terminal,
	d.last_seen_at, d.status, d.metadata, d.created_at, d.updated_at`

// adminVisibleDevices is the one place the non-super-admin device visibility rule lives
// (SRS FR-1.3, FR-1.4, FR-6.4). It expects the caller's user id bound to $1 of the
// fragment, and mirrors ws.canViewDevice exactly — the REST list and the session socket
// disagreeing about who may see a device is the kind of bug that only shows up as a
// mysterious 404 after someone has already been told they have access.
//
// The team clause is additive: an Admin on no team keeps precisely the access they had
// before teams conferred any, so an empty membership table locks nobody out.
const adminVisibleDevices = `(
	d.assigned_admin_id = %[1]s
	OR d.group_id IN (SELECT group_id FROM user_group_members WHERE user_id = %[1]s)
)`

// visibleDevicesClause renders adminVisibleDevices against a concrete placeholder, so a
// caller with a different number of preceding arguments cannot silently bind the wrong
// one.
func visibleDevicesClause(placeholder string) string {
	return fmt.Sprintf(adminVisibleDevices, placeholder)
}

func scanDevice(rows interface{ Scan(...any) error }) (models.Device, error) {
	var d models.Device
	var metadataBytes []byte
	err := rows.Scan(
		&d.ID, &d.OrgID, &d.Name, &d.Type, &d.OSVersion, &d.IPAddress,
		&d.AssignedAdminID, &d.AssignedAdmin,
		&d.GroupID, &d.GroupName,
		&d.AllowScreen, &d.AllowTerminal,
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

	// An Admin sees their assignments and their teams' devices; a Super Admin sees the
	// whole org. Both are scoped to the caller's organization, so the org_id the schema
	// already carries is actually load-bearing rather than decorative.
	if claims.Role != models.RoleSuperAdmin {
		query += ` AND ` + visibleDevicesClause("$2")
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

	// The visibility rule lives in the WHERE clause rather than in a check after the
	// scan, so "not permitted" and "not found" are the same code path and cannot drift
	// apart into an enumeration oracle.
	query := `
		SELECT ` + deviceColumns + `
		FROM devices d
		LEFT JOIN users u ON d.assigned_admin_id = u.id
		LEFT JOIN device_groups g ON d.group_id = g.id
		WHERE d.id = $1 AND d.org_id = $2`
	args := []any{deviceID, claims.OrgID}
	if claims.Role != models.RoleSuperAdmin {
		query += ` AND ` + visibleDevicesClause("$3")
		args = append(args, claims.UserID)
	}

	d, err := scanDevice(h.db.QueryRow(query, args...))
	if err != nil {
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

	// An admin's invite link always assigns the devices it enrolls to that admin, so
	// everyone who uses their link lands in their panel. A super admin may target any
	// admin (or leave it unassigned) via the request field.
	if claims.Role != models.RoleSuperAdmin {
		req.AdminID = &claims.UserID
	}

	// The team is where the real authority is: every admin on it can view every device
	// the link enrolls. Without this check, opening the route to Admins would let any of
	// them mint a link into any team in the org — an escalation, not a convenience.
	// canSeeGroup is the same rule ListGroups and ListGroupMembers already apply, so a
	// team the caller cannot see and one that does not exist give the same answer.
	if req.GroupID != nil && *req.GroupID != "" {
		if !h.canSeeGroup(claims, *req.GroupID) {
			h.respondError(w, http.StatusForbidden, "You are not a member of that team")
			return
		}
	} else {
		req.GroupID = nil
	}

	// 12 random bytes -> 96 bits, formatted for someone to retype once.
	raw, err := auth.GenerateSecret(12)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}
	token := "DRS-" + strings.ToUpper(raw)
	expiresAt := time.Now().Add(enrollmentTokenTTL)

	label := strings.TrimSpace(req.Label)
	if len(label) > 120 {
		label = label[:120]
	}

	var tokenID string
	// Only the hash is stored: the token is a credential that grants enrollment, so
	// read access to the database should not be enough to use one.
	err = h.db.QueryRow(`
		INSERT INTO enrollment_tokens
			(org_id, token_hash, device_type, assigned_admin_id, group_id, created_by, expires_at, label)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''))
		RETURNING id
	`, claims.OrgID, auth.HashSecret(token), req.DeviceType,
		req.AdminID, req.GroupID, claims.UserID, expiresAt, label).Scan(&tokenID)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to create enrollment token")
		return
	}

	h.audit.Log(claims.OrgID, claims.UserID, claims.Email, "create_enrollment_token", "enrollment_token", tokenID, map[string]any{
		"type":       req.DeviceType,
		"expires_at": expiresAt,
		"group_id":   req.GroupID,
		"label":      label,
	}, r.RemoteAddr)

	h.respondJSON(w, http.StatusOK, models.GenerateEnrollmentTokenResponse{
		ID:              tokenID,
		EnrollmentToken: token,
		ExpiresAt:       expiresAt,
	})
}

// ListEnrollmentTokens shows the invite links that can still enroll devices.
//
// This exists because the download endpoint turned a token into an executable: an admin
// needs to be able to see what is outstanding in order to decide what to revoke. Scoped
// the way ListGroups is — an Admin sees only links they created, a Super Admin sees the
// org's. Revoked links stay listed rather than disappearing, because "this link was
// killed on Tuesday" is the useful answer.
func (h *Handler) ListEnrollmentTokens(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)

	query := `
		SELECT t.id, COALESCE(t.label, ''), t.device_type,
		       t.group_id, COALESCE(g.name, ''),
		       t.created_by, COALESCE(u.email, ''), t.created_at,
		       t.revoked_at, t.expires_at,
		       (SELECT COUNT(*) FROM devices d WHERE d.enrolled_via_token = t.id)
		FROM enrollment_tokens t
		LEFT JOIN device_groups g ON g.id = t.group_id
		LEFT JOIN users u ON u.id = t.created_by
		WHERE t.org_id = $1`
	args := []any{claims.OrgID}
	if claims.Role != models.RoleSuperAdmin {
		query += ` AND t.created_by = $2`
		args = append(args, claims.UserID)
	}
	query += ` ORDER BY t.created_at DESC LIMIT 200`

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to list enrollment tokens")
		return
	}
	defer rows.Close()

	tokens := []models.EnrollmentTokenSummary{}
	for rows.Next() {
		var t models.EnrollmentTokenSummary
		if err := rows.Scan(&t.ID, &t.Label, &t.DeviceType, &t.GroupID, &t.GroupName,
			&t.CreatedBy, &t.CreatedByEmail, &t.CreatedAt, &t.RevokedAt, &t.ExpiresAt,
			&t.DeviceCount); err == nil {
			tokens = append(tokens, t)
		}
	}
	h.respondJSON(w, http.StatusOK, tokens)
}

// RevokeEnrollmentToken turns an invite link off.
//
// It stops the link enrolling anything new. It deliberately does NOT touch devices the
// link already enrolled: those hold their own agent secrets, and removing them is a
// separate decision (delete the device) that an admin should make on purpose rather than
// as a side effect of tidying up a link.
func (h *Handler) RevokeEnrollmentToken(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)

	tokenID := pathSegment(r, 3)
	if tokenID == "" {
		h.respondError(w, http.StatusBadRequest, "Invalid path")
		return
	}
	if _, err := uuid.Parse(tokenID); err != nil {
		h.respondError(w, http.StatusNotFound, "Enrollment token not found")
		return
	}

	// An Admin may only revoke their own links. Scoping the UPDATE itself rather than
	// checking first means a link belonging to someone else reports "not found" instead
	// of confirming it exists.
	query := `UPDATE enrollment_tokens
	          SET revoked_at = NOW(), revoked_by = $2
	          WHERE id = $1 AND org_id = $3 AND revoked_at IS NULL`
	args := []any{tokenID, claims.UserID, claims.OrgID}
	if claims.Role != models.RoleSuperAdmin {
		query += ` AND created_by = $4`
		args = append(args, claims.UserID)
	}

	res, err := h.db.Exec(query, args...)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to revoke enrollment token")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Already revoked, someone else's, or nonexistent — all the same answer.
		h.respondError(w, http.StatusNotFound, "Enrollment token not found")
		return
	}

	h.audit.Log(claims.OrgID, claims.UserID, claims.Email, "revoke_enrollment_token",
		"enrollment_token", tokenID, nil, r.RemoteAddr)
	w.WriteHeader(http.StatusNoContent)
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
		WSURL:                    h.agentWSURL(r),
		HeartbeatIntervalSeconds: int(h.cfg.HeartbeatInterval.Seconds()),
	})
}

// redeemToken validates a token and creates the device in one transaction.
//
// Tokens are reusable by request: the same token can enroll many devices, so redemption
// does NOT mark it used. Each redemption mints a fresh, unique agent secret and its own
// device row, so distinct devices never share an identity.
//
// Reusable is not the same as unstoppable. Revocation and expiry ARE enforced here, and
// this is the only place they are — the revoke endpoint would be decorative without this
// clause. That matters more since the download endpoint began baking tokens into agent
// binaries: the installer is a credential in executable form, and this is its off switch.
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
		  AND revoked_at IS NULL
		  AND expires_at > NOW()
	`, auth.HashSecret(req.EnrollmentToken)).Scan(&tokenID, &orgID, &assignedAdminID, &groupID)
	if err != nil {
		return "", "", "", errors.New("token not redeemable")
	}

	secret, err := auth.GenerateSecret(32)
	if err != nil {
		return "", "", "", err
	}

	metadata := json.RawMessage("{}")
	if len(req.Metadata) > 0 {
		metadata = req.Metadata
	}

	// Capabilities. Screen defaults on and terminal off, which covers an agent that sends
	// neither (the Android one, and any older Windows build); the Windows agent sends both
	// as true. The server keeps deciding — an agent asking for a capability is a request.
	allowScreen := true
	allowTerminal := false
	if req.AllowScreen != nil {
		allowScreen = *req.AllowScreen
	}
	if req.AllowTerminal != nil {
		allowTerminal = *req.AllowTerminal
	}

	// Which existing device, if any, is this machine?
	//
	// Prefer the machine id: it is generated once per machine and kept, so it survives a
	// rename and — critically — distinguishes two machines that share a hostname. Cloned
	// VMs and imaged corporate fleets routinely do, and matching them on name meant the
	// second one to enroll took over the first one's row and rotated its secret, leaving
	// the first agent reconnecting forever with a credential the server had discarded.
	//
	// Fall back to (org, name, type) when the agent sends no machine id, so agents built
	// before this existed keep re-enrolling to their own row instead of duplicating it.
	machineID := strings.TrimSpace(req.MachineID)
	if machineID != "" {
		err = tx.QueryRow(`
			SELECT id FROM devices WHERE org_id = $1 AND machine_id = $2
		`, orgID, machineID).Scan(&deviceID)

		// Not found by machine id. That is either a genuinely new machine, or a device
		// that enrolled before machine ids existed and is now reporting one for the
		// first time — every device already in the fleet, on its next re-enrollment.
		//
		// Adopt by name ONLY when the existing row has claimed no machine id. Without
		// that condition an upgraded agent would create a second row and leave its own
		// original lingering offline forever; with it, a clone whose twin has already
		// claimed a machine id still gets its own row, which is the takeover this
		// column exists to prevent.
		if err == sql.ErrNoRows {
			err = tx.QueryRow(`
				SELECT id FROM devices
				WHERE org_id = $1 AND name = $2 AND type = $3 AND machine_id IS NULL
			`, orgID, req.Name, req.Type).Scan(&deviceID)
		}
	} else {
		err = tx.QueryRow(`
			SELECT id FROM devices WHERE org_id = $1 AND name = $2 AND type = $3
		`, orgID, req.Name, req.Type).Scan(&deviceID)
	}

	switch {
	case err == sql.ErrNoRows:
		deviceID = uuid.New().String()
		if _, err = tx.Exec(`
			INSERT INTO devices
				(id, org_id, name, type, os_version, agent_secret_hash,
				 assigned_admin_id, group_id, allow_screen, allow_terminal,
				 status, metadata, last_seen_at, machine_id, enrolled_via_token)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'offline', $11, NOW(),
			        NULLIF($12, ''), $13)
		`, deviceID, orgID, req.Name, req.Type, req.OSVersion, auth.HashSecret(secret),
			assignedAdminID, groupID, allowScreen, allowTerminal, []byte(metadata),
			machineID, tokenID); err != nil {
			return "", "", "", err
		}
	case err != nil:
		return "", "", "", err
	default:
		// Existing machine re-enrolling: rotate its secret and re-apply the token's
		// assignment and the freshly consented capabilities. The name is refreshed too,
		// so a renamed machine matched by machine id shows its current hostname.
		if _, err = tx.Exec(`
			UPDATE devices
			SET agent_secret_hash = $2, name = $3, os_version = $4, assigned_admin_id = $5,
			    group_id = $6, allow_screen = $7, allow_terminal = $8,
			    machine_id = COALESCE(NULLIF($9, ''), machine_id),
			    enrolled_via_token = $10,
			    status = 'offline', last_seen_at = NOW(), updated_at = NOW()
			WHERE id = $1
		`, deviceID, auth.HashSecret(secret), req.Name, req.OSVersion, assignedAdminID,
			groupID, allowScreen, allowTerminal, machineID, tokenID); err != nil {
			return "", "", "", err
		}
	}

	// Deliberately do NOT mark the token used: it is reusable so it can enroll more
	// devices later.

	if err = tx.Commit(); err != nil {
		return "", "", "", err
	}
	return deviceID, orgID, secret, nil
}

// publicBaseURL is the origin this deployment is reachable at, as best the backend can
// tell from the request it is answering.
//
// Two things are derived from it — the WebSocket URL handed to an enrolling agent, and
// the server address baked into a downloaded agent — and they must agree, so there is one
// function rather than two derivations that can drift apart.
//
// PUBLIC_BASE_URL overrides it. That escape hatch exists because a wrong answer here is
// unusually expensive: it is written into an agent binary that then cannot reach the
// server, on a machine nobody is looking at.
func (h *Handler) publicBaseURL(r *http.Request) string {
	if h.cfg != nil && h.cfg.PublicBaseURL != "" {
		return h.cfg.PublicBaseURL
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	// X-Forwarded-Host first: behind a proxy that rewrites Host (nginx's proxy_set_header,
	// or a dev proxy with changeOrigin), r.Host is the upstream's address, not the one the
	// browser — or the agent — can reach.
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		// A comma-separated chain means several proxies; the first entry is the original.
		if i := strings.IndexByte(fwd, ','); i >= 0 {
			fwd = fwd[:i]
		}
		host = strings.TrimSpace(fwd)
	}
	return scheme + "://" + host
}

// agentWSURL builds the URL the agent should dial, from the request it arrived on, so
// a deployment behind any hostname works without extra configuration.
func (h *Handler) agentWSURL(r *http.Request) string {
	base := h.publicBaseURL(r)
	switch {
	case strings.HasPrefix(base, "https://"):
		return "wss://" + strings.TrimPrefix(base, "https://") + "/ws/agent"
	case strings.HasPrefix(base, "http://"):
		return "ws://" + strings.TrimPrefix(base, "http://") + "/ws/agent"
	default:
		return "ws://" + base + "/ws/agent"
	}
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

	// The admin and group are validated against the caller's org before being written.
	// The foreign keys prove the rows exist but not that they are this tenant's, and
	// org_id is the only thing separating tenants.
	if req.AdminID != nil {
		var one int
		if err := h.db.QueryRow(`SELECT 1 FROM users WHERE id = $1 AND org_id = $2`,
			*req.AdminID, claims.OrgID).Scan(&one); err != nil {
			h.respondError(w, http.StatusBadRequest, "Unknown admin")
			return
		}
	}
	if req.GroupID != nil {
		var one int
		if err := h.db.QueryRow(`SELECT 1 FROM device_groups WHERE id = $1 AND org_id = $2`,
			*req.GroupID, claims.OrgID).Scan(&one); err != nil {
			h.respondError(w, http.StatusBadRequest, "Unknown team")
			return
		}
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

	// Push the new assignment into presence. Without this, reassigning a device that is
	// currently online leaves the presence record pointing at the previous admin and team
	// until that agent reconnects — cosmetic staleness while a group was a caption, but a
	// failure to revoke now that team membership grants visibility.
	h.presence.UpdateDeviceAssignment(deviceID, req.AdminID, req.GroupID)

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

// ListGroups returns the teams visible to the caller.
//
// An Admin sees only the teams they belong to. Until membership existed this handler
// returned every team in the org to both roles, which was harmless while a team was a
// caption but is an enumeration of the org's structure now that a team name is also the
// name of an access grant.
func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)

	// Counted with a correlated subquery rather than a LEFT JOIN + GROUP BY, because the
	// member count needs a second aggregate over an unrelated table and joining both at
	// once multiplies the rows.
	query := `
		SELECT g.id, g.org_id, g.name, g.description, g.created_at,
		       (SELECT COUNT(*) FROM devices d WHERE d.group_id = g.id)            AS dev_count,
		       (SELECT COUNT(*) FROM user_group_members m WHERE m.group_id = g.id) AS mem_count
		FROM device_groups g
		WHERE g.org_id = $1`
	args := []any{claims.OrgID}
	if claims.Role != models.RoleSuperAdmin {
		query += ` AND g.id IN (SELECT group_id FROM user_group_members WHERE user_id = $2)`
		args = append(args, claims.UserID)
	}
	query += ` ORDER BY g.name ASC`

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to query groups")
		return
	}
	defer rows.Close()

	groups := make([]models.DeviceGroup, 0)
	for rows.Next() {
		var g models.DeviceGroup
		if err := rows.Scan(&g.ID, &g.OrgID, &g.Name, &g.Description, &g.CreatedAt,
			&g.DeviceCount, &g.MemberCount); err == nil {
			groups = append(groups, g)
		}
	}
	h.respondJSON(w, http.StatusOK, groups)
}

// isUniqueViolation reports whether an error is Postgres 23505. Team names are unique
// per org, and a clash is the caller's mistake (409) rather than a server fault (500).
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

// CreateGroup adds a team/department grouping (SRS FR-1.7).
func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)

	var req models.CreateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		h.respondError(w, http.StatusBadRequest, "Invalid group name")
		return
	}

	var g models.DeviceGroup
	err := h.db.QueryRow(`
		INSERT INTO device_groups (org_id, name, description)
		VALUES ($1, $2, $3)
		RETURNING id, org_id, name, description, created_at
	`, claims.OrgID, req.Name, req.Description).Scan(
		&g.ID, &g.OrgID, &g.Name, &g.Description, &g.CreatedAt,
	)
	if isUniqueViolation(err) {
		h.respondError(w, http.StatusConflict, "A team with that name already exists")
		return
	}
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to create group")
		return
	}

	// Creating a team is a permission change now that membership grants visibility, so
	// it belongs in the trail alongside assign_device (SRS FR-1.8).
	h.audit.Log(claims.OrgID, claims.UserID, claims.Email, "create_group", "group", g.ID, map[string]any{
		"name": g.Name,
	}, r.RemoteAddr)
	h.respondJSON(w, http.StatusCreated, g)
}

// UpdateGroup renames a team or edits its description.
func (h *Handler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)
	groupID := pathSegment(r, 2)
	if groupID == "" {
		h.respondError(w, http.StatusBadRequest, "Missing group id")
		return
	}

	var req models.UpdateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			h.respondError(w, http.StatusBadRequest, "Invalid group name")
			return
		}
		req.Name = &trimmed
	}
	if req.Name == nil && req.Description == nil {
		h.respondError(w, http.StatusBadRequest, "Nothing to update")
		return
	}

	// COALESCE leaves an omitted field at its current value, which is what makes a rename
	// safe to send without also having to restate the description.
	var g models.DeviceGroup
	err := h.db.QueryRow(`
		UPDATE device_groups
		SET name        = COALESCE($1, name),
		    description = COALESCE($2, description)
		WHERE id = $3 AND org_id = $4
		RETURNING id, org_id, name, description, created_at
	`, req.Name, req.Description, groupID, claims.OrgID).Scan(
		&g.ID, &g.OrgID, &g.Name, &g.Description, &g.CreatedAt,
	)
	if isUniqueViolation(err) {
		h.respondError(w, http.StatusConflict, "A team with that name already exists")
		return
	}
	if err == sql.ErrNoRows {
		h.respondError(w, http.StatusNotFound, "Team not found")
		return
	}
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to update group")
		return
	}

	h.audit.Log(claims.OrgID, claims.UserID, claims.Email, "update_group", "group", g.ID, map[string]any{
		"name": g.Name,
	}, r.RemoteAddr)
	h.respondJSON(w, http.StatusOK, g)
}

// DeleteGroupOrMember routes the two DELETEs that share the /api/groups/ prefix.
//
// The mux takes one handler per method per prefix, and these two are the same method on
// nested paths, so the split happens here rather than in the routing table.
func (h *Handler) DeleteGroupOrMember(w http.ResponseWriter, r *http.Request) {
	if pathSegment(r, 3) == "members" {
		h.RemoveGroupMember(w, r)
		return
	}
	h.DeleteGroup(w, r)
}

// DeleteGroup removes a team.
//
// Devices survive: devices.group_id is ON DELETE SET NULL, so they become ungrouped
// rather than disappearing with the team. Memberships do not — they cascade, which is
// the point, since a team that no longer exists must not keep granting access.
func (h *Handler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)
	groupID := pathSegment(r, 2)
	if groupID == "" {
		h.respondError(w, http.StatusBadRequest, "Missing group id")
		return
	}

	// The members have to be read before the delete: ON DELETE SET NULL means that once
	// the team is gone there is nothing left to identify which devices used to be in it.
	type member struct {
		id      string
		adminID *string
	}
	var members []member
	if rows, err := h.db.Query(
		`SELECT id, assigned_admin_id FROM devices WHERE group_id = $1`, groupID,
	); err == nil {
		for rows.Next() {
			var m member
			if err := rows.Scan(&m.id, &m.adminID); err == nil {
				members = append(members, m)
			}
		}
		rows.Close()
	}

	res, err := h.db.Exec(`DELETE FROM device_groups WHERE id = $1 AND org_id = $2`, groupID, claims.OrgID)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to delete group")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.respondError(w, http.StatusNotFound, "Team not found")
		return
	}

	// Every device that just lost its team also lost the visibility that team conferred.
	// Presence meta is written only when an agent connects, so a device that is online
	// right now would otherwise keep feeding status to the departed team's admins until
	// its agent happened to reconnect.
	for _, m := range members {
		h.presence.UpdateDeviceAssignment(m.id, m.adminID, nil)
	}

	h.audit.Log(claims.OrgID, claims.UserID, claims.Email, "delete_group", "group", groupID, map[string]any{
		"devices_ungrouped": len(members),
	}, r.RemoteAddr)
	h.respondJSON(w, http.StatusOK, map[string]string{"message": "Team deleted successfully"})
}

// ListGroupMembers returns the admins on a team.
func (h *Handler) ListGroupMembers(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)
	groupID := pathSegment(r, 2)
	if groupID == "" || pathSegment(r, 3) != "members" {
		h.respondError(w, http.StatusBadRequest, "Invalid path")
		return
	}
	if !h.canSeeGroup(claims, groupID) {
		h.respondError(w, http.StatusNotFound, "Team not found")
		return
	}

	rows, err := h.db.Query(`
		SELECT m.user_id, u.email, u.role, m.created_at
		FROM user_group_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.group_id = $1
		ORDER BY u.email ASC
	`, groupID)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to query members")
		return
	}
	defer rows.Close()

	members := make([]models.GroupMember, 0)
	for rows.Next() {
		var m models.GroupMember
		if err := rows.Scan(&m.UserID, &m.Email, &m.Role, &m.CreatedAt); err == nil {
			members = append(members, m)
		}
	}
	h.respondJSON(w, http.StatusOK, members)
}

// canSeeGroup mirrors the scoping in ListGroups: a Super Admin sees any team in the org,
// an Admin only teams they are on. A team in another org and a team the caller may not
// see give the same answer as a team that does not exist.
func (h *Handler) canSeeGroup(claims *auth.JWTClaims, groupID string) bool {
	query := `SELECT 1 FROM device_groups g WHERE g.id = $1 AND g.org_id = $2`
	args := []any{groupID, claims.OrgID}
	if claims.Role != models.RoleSuperAdmin {
		query += ` AND g.id IN (SELECT group_id FROM user_group_members WHERE user_id = $3)`
		args = append(args, claims.UserID)
	}
	var one int
	return h.db.QueryRow(query, args...).Scan(&one) == nil
}

// AddGroupMember puts an admin on a team, which grants them every device in it.
func (h *Handler) AddGroupMember(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)
	groupID := pathSegment(r, 2)
	if groupID == "" || pathSegment(r, 3) != "members" {
		h.respondError(w, http.StatusBadRequest, "Invalid path")
		return
	}

	var req models.AddGroupMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		h.respondError(w, http.StatusBadRequest, "Invalid user id")
		return
	}
	if _, err := uuid.Parse(req.UserID); err != nil {
		h.respondError(w, http.StatusBadRequest, "Invalid user id")
		return
	}

	// Both ids are confirmed to be in the caller's org before the insert. The foreign
	// keys guarantee the rows exist but say nothing about which organization they belong
	// to, and org_id is the only thing separating tenants.
	var email string
	if err := h.db.QueryRow(`
		SELECT u.email FROM users u, device_groups g
		WHERE u.id = $1 AND g.id = $2 AND u.org_id = $3 AND g.org_id = $3
	`, req.UserID, groupID, claims.OrgID).Scan(&email); err != nil {
		h.respondError(w, http.StatusNotFound, "Team or user not found")
		return
	}

	// Adding someone who is already on the team is not an error; the caller's intent is
	// already satisfied.
	if _, err := h.db.Exec(`
		INSERT INTO user_group_members (user_id, group_id, added_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, group_id) DO NOTHING
	`, req.UserID, groupID, claims.UserID); err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to add member")
		return
	}

	h.audit.Log(claims.OrgID, claims.UserID, claims.Email, "add_group_member", "group", groupID, map[string]any{
		"user_id": req.UserID,
		"email":   email,
	}, r.RemoteAddr)
	h.respondJSON(w, http.StatusOK, map[string]string{"message": "Member added successfully"})
}

// RemoveGroupMember takes an admin off a team, revoking the visibility it granted.
func (h *Handler) RemoveGroupMember(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetUserClaims(r)
	groupID := pathSegment(r, 2)
	userID := pathSegment(r, 4)
	if groupID == "" || pathSegment(r, 3) != "members" || userID == "" {
		h.respondError(w, http.StatusBadRequest, "Invalid path")
		return
	}

	res, err := h.db.Exec(`
		DELETE FROM user_group_members m
		USING device_groups g
		WHERE m.group_id = g.id AND m.group_id = $1 AND m.user_id = $2 AND g.org_id = $3
	`, groupID, userID, claims.OrgID)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to remove member")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.respondError(w, http.StatusNotFound, "Membership not found")
		return
	}

	h.audit.Log(claims.OrgID, claims.UserID, claims.Email, "remove_group_member", "group", groupID, map[string]any{
		"user_id": userID,
	}, r.RemoteAddr)
	h.respondJSON(w, http.StatusOK, map[string]string{"message": "Member removed successfully"})
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
	// An Admin sees their own sessions plus any session on a device they can see, so a
	// team lead can review what happened on their team's machines rather than only what
	// they personally did.
	if claims.Role != models.RoleSuperAdmin {
		query += ` AND (s.admin_id = $2 OR ` + visibleDevicesClause("$2") + `)`
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
