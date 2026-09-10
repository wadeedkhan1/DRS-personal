package models

import (
	"encoding/json"
	"time"
)

// Role constants
const (
	RoleSuperAdmin = "super_admin"
	RoleAdmin      = "admin"
)

// Device types
const (
	DeviceTypeWindows = "windows"
	DeviceTypeAndroid = "android"
)

// Device & Session status
const (
	DeviceStatusOnline    = "online"
	DeviceStatusOffline   = "offline"
	DeviceStatusInSession = "in_session"

	SessionModeView    = "view"
	SessionModeControl = "control"

	SessionStatusActive     = "active"
	SessionStatusCompleted  = "completed"
	SessionStatusTerminated = "terminated"
)

type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type User struct {
	ID           string    `json:"id"`
	OrgID        string    `json:"org_id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	MFASecret    string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// DeviceGroup is a team: a set of devices, and a set of admins who can therefore see
// them. The counts are computed per query rather than stored.
type DeviceGroup struct {
	ID          string    `json:"id"`
	OrgID       string    `json:"org_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	DeviceCount int       `json:"device_count"`
	MemberCount int       `json:"member_count"`
}

// GroupMember is one admin on a team. It is a projection of user_group_members joined to
// users, since the membership row on its own says nothing a UI can display.
type GroupMember struct {
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type Device struct {
	ID              string          `json:"id"`
	OrgID           string          `json:"org_id"`
	Name            string          `json:"name"`
	Type            string          `json:"type"` // windows, android
	OSVersion       string          `json:"os_version"`
	IPAddress       string          `json:"ip_address"`
	AssignedAdminID *string         `json:"assigned_admin_id"`
	AssignedAdmin   *string         `json:"assigned_admin_email,omitempty"`
	GroupID         *string         `json:"group_id"`
	GroupName       *string         `json:"group_name,omitempty"`
	AgentSecretHash string          `json:"-"`
	AllowScreen     bool            `json:"allow_screen"`
	AllowTerminal   bool            `json:"allow_terminal"`
	LastSeenAt      time.Time       `json:"last_seen_at"`
	Status          string          `json:"status"` // online, offline, in_session
	Metadata        json.RawMessage `json:"metadata"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type Session struct {
	ID             string     `json:"id"`
	DeviceID       string     `json:"device_id"`
	DeviceName     string     `json:"device_name,omitempty"`
	AdminID        string     `json:"admin_id"`
	AdminEmail     string     `json:"admin_email,omitempty"`
	StartedAt      time.Time  `json:"started_at"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
	Mode           string     `json:"mode"`
	ConnectionType string     `json:"connection_type"`
	Status         string     `json:"status"`
}

type AuditLog struct {
	ID          string          `json:"id"`
	OrgID       *string         `json:"org_id,omitempty"`
	ActorUserID *string         `json:"actor_user_id,omitempty"`
	ActorEmail  string          `json:"actor_email"`
	Action      string          `json:"action"`
	TargetType  string          `json:"target_type"`
	TargetID    string          `json:"target_id"`
	Metadata    json.RawMessage `json:"metadata"`
	IPAddress   string          `json:"ip_address"`
	CreatedAt   time.Time       `json:"created_at"`
}

// Request / Response structures

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

type CreateUserRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type UpdateUserRequest struct {
	Email    *string `json:"email,omitempty"`
	Password *string `json:"password,omitempty"`
	Role     *string `json:"role,omitempty"`
}

type CreateGroupRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// UpdateGroupRequest patches a team. Both fields are pointers so that omitting one
// leaves it alone rather than blanking it, which matters for description: renaming a
// team should not silently erase what it is for.
type UpdateGroupRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type AddGroupMemberRequest struct {
	UserID string `json:"user_id"`
}

type GenerateEnrollmentTokenRequest struct {
	DeviceType string  `json:"device_type"` // windows, android
	GroupID    *string `json:"group_id,omitempty"`
	AdminID    *string `json:"assigned_admin_id,omitempty"`
	// Label is a human name for the link ("Finance rollout"), so a list of outstanding
	// links is something an admin can act on rather than a column of UUIDs.
	Label string `json:"label,omitempty"`
}

// GenerateEnrollmentTokenResponse returns a real expiry timestamp rather than a
// hardcoded "24 hours" string. The old response promised an expiry that nothing
// enforced, so tokens were valid forever.
type GenerateEnrollmentTokenResponse struct {
	// ID identifies the link for revocation. The token itself is a credential and is
	// never accepted back from a client as an identifier.
	ID              string    `json:"id"`
	EnrollmentToken string    `json:"enrollment_token"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// EnrollmentTokenSummary is one row of the invite-link list. It deliberately carries no
// token or hash: the plaintext exists only in the response that created it, and the hash
// is not something a portal has any use for.
type EnrollmentTokenSummary struct {
	ID             string     `json:"id"`
	Label          string     `json:"label"`
	DeviceType     string     `json:"device_type"`
	GroupID        *string    `json:"group_id"`
	GroupName      string     `json:"group_name"`
	CreatedBy      *string    `json:"created_by"`
	CreatedByEmail string     `json:"created_by_email"`
	CreatedAt      time.Time  `json:"created_at"`
	RevokedAt      *time.Time `json:"revoked_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	// DeviceCount is how many devices this link has enrolled — the number that decides
	// whether revoking it is safe.
	DeviceCount int `json:"device_count"`
}

type EnrollDeviceRequest struct {
	EnrollmentToken string          `json:"enrollment_token"`
	Name            string          `json:"name"`
	Type            string          `json:"type"` // windows, android
	OSVersion       string          `json:"os_version"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`

	// MachineID is a stable per-machine identifier the agent generates once and keeps.
	// It is what devices are de-duplicated on when present, because hostnames are not
	// unique across cloned VMs and imaged fleets — and a collision there silently
	// authenticates one machine out of the other's identity. Omitted by agents built
	// before it existed, which fall back to (org, name, type).
	MachineID string `json:"machine_id,omitempty"`

	// Capabilities this device is enrolled with. Pointers so "not sent" is distinct from
	// "sent false": the Android agent omits them and gets the defaults (screen on,
	// terminal off), while the Windows agent always sends both as true.
	AllowScreen   *bool `json:"allow_screen,omitempty"`
	AllowTerminal *bool `json:"allow_terminal,omitempty"`
}

// EnrollDeviceResponse is the agent's identity, handed over exactly once at
// enrollment. There is no STUN/TURN field: ICE servers now arrive with each
// start_session, so both peers always share one freshly-minted list.
type EnrollDeviceResponse struct {
	DeviceID                 string `json:"deviceId"`
	AgentSecret              string `json:"agentSecret"`
	WSURL                    string `json:"wsUrl"`
	HeartbeatIntervalSeconds int    `json:"heartbeatIntervalSeconds"`
}

type AssignDeviceRequest struct {
	AdminID *string `json:"admin_id"`
	GroupID *string `json:"group_id"`
}

type UsageReport struct {
	TotalDevices    int     `json:"total_devices"`
	OnlineDevices   int     `json:"online_devices"`
	TotalSessions   int     `json:"total_sessions"`
	ActiveSessions  int     `json:"active_sessions"`
	AvgDurationMins float64 `json:"avg_duration_minutes"`
	TotalAdmins     int     `json:"total_admins"`
}
