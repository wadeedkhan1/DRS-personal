package ws

import (
	"context"
	"database/sql"
	"encoding/json"

	"drs/backend/internal/auth"
	"drs/backend/pkg/protocol"

	"github.com/google/uuid"
)

// Device is the slice of a device record the socket layer actually needs. Keeping it
// narrow is what lets the hub be tested against a fake store instead of a live
// Postgres, which is why the RBAC rules below are testable at all.
type Device struct {
	ID              string
	OrgID           string
	Name            string
	Type            string
	AssignedAdminID *string
	AllowScreen     bool
	AllowTerminal   bool
}

// DeviceStore is the device persistence the hub depends on.
type DeviceStore interface {
	// AuthenticateAgent verifies a device id and secret. It returns ok=false for an
	// unknown device, an unenrolled device and a wrong secret alike: telling those
	// apart would let an attacker enumerate valid device ids.
	AuthenticateAgent(ctx context.Context, deviceID, secret string) (Device, bool, error)
	Lookup(ctx context.Context, deviceID string) (Device, bool, error)
	SetStatus(ctx context.Context, deviceID, status, ip string) error
	RecordHeartbeat(ctx context.Context, deviceID, ip string, hb protocol.Heartbeat) error
}

// SessionStore persists the session history behind SRS FR-9.1 reporting.
type SessionStore interface {
	Open(ctx context.Context, sessionID, deviceID, adminID, mode string) error
	Close(ctx context.Context, sessionID, status string) error
}

// SQLStore implements DeviceStore and SessionStore against Postgres.
type SQLStore struct{ db *sql.DB }

// NewSQLStore wraps a database handle.
func NewSQLStore(db *sql.DB) *SQLStore { return &SQLStore{db: db} }

// AuthenticateAgent looks a device up and constant-time compares its secret.
func (s *SQLStore) AuthenticateAgent(ctx context.Context, deviceID, secret string) (Device, bool, error) {
	// Validate the id shape before it reaches Postgres: a malformed uuid is a type
	// error there, not a "no rows", and would surface as a 500-ish log line on every
	// probe rather than a quiet auth failure.
	if _, err := uuid.Parse(deviceID); err != nil {
		return Device{}, false, nil
	}

	var dev Device
	var hash sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, org_id, name, type, assigned_admin_id, agent_secret_hash
		FROM devices WHERE id = $1
	`, deviceID).Scan(&dev.ID, &dev.OrgID, &dev.Name, &dev.Type, &dev.AssignedAdminID, &hash)

	if err == sql.ErrNoRows {
		return Device{}, false, nil
	}
	if err != nil {
		return Device{}, false, err
	}
	if !hash.Valid || hash.String == "" {
		return Device{}, false, nil // enrolled but never completed
	}
	if !auth.SecretMatches(hash.String, secret) {
		return Device{}, false, nil
	}
	return dev, true, nil
}

// Lookup fetches a device without authenticating it, for the viewer's RBAC check.
func (s *SQLStore) Lookup(ctx context.Context, deviceID string) (Device, bool, error) {
	if _, err := uuid.Parse(deviceID); err != nil {
		return Device{}, false, nil
	}
	var dev Device
	err := s.db.QueryRowContext(ctx, `
		SELECT id, org_id, name, type, assigned_admin_id, allow_screen, allow_terminal
		FROM devices WHERE id = $1
	`, deviceID).Scan(&dev.ID, &dev.OrgID, &dev.Name, &dev.Type, &dev.AssignedAdminID,
		&dev.AllowScreen, &dev.AllowTerminal)
	if err == sql.ErrNoRows {
		return Device{}, false, nil
	}
	if err != nil {
		return Device{}, false, err
	}
	return dev, true, nil
}

// SetStatus records online/offline transitions durably, so the dashboard is right
// after a backend restart even before any agent reconnects.
func (s *SQLStore) SetStatus(ctx context.Context, deviceID, status, ip string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE devices
		SET status = $2,
		    last_seen_at = NOW(),
		    ip_address = COALESCE(NULLIF($3, ''), ip_address),
		    updated_at = NOW()
		WHERE id = $1
	`, deviceID, status, ip)
	return err
}

// RecordHeartbeat folds the agent's telemetry into the device row. CPU and RAM live
// in metadata rather than columns of their own because they are diagnostic, not
// queried, and adding columns for them would outrun the migration this phase needs.
func (s *SQLStore) RecordHeartbeat(ctx context.Context, deviceID, ip string, hb protocol.Heartbeat) error {
	meta, err := json.Marshal(map[string]any{
		"cpuPercent": hb.CPUPercent,
		"ramPercent": hb.RAMPercent,
		"hostname":   hb.SysInfo.Hostname,
		"platform":   hb.SysInfo.Platform,
	})
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE devices
		SET last_seen_at = NOW(),
		    ip_address  = COALESCE(NULLIF($2, ''), ip_address),
		    os_version  = COALESCE(NULLIF($3, ''), os_version),
		    metadata    = COALESCE(metadata, '{}'::jsonb) || $4::jsonb,
		    updated_at  = NOW()
		WHERE id = $1
	`, deviceID, ip, hb.SysInfo.OS, string(meta))
	return err
}

// Open writes the session row at session start.
func (s *SQLStore) Open(ctx context.Context, sessionID, deviceID, adminID, mode string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, device_id, admin_id, started_at, mode, connection_type, status)
		VALUES ($1, $2, $3, NOW(), $4, 'p2p', 'active')
	`, sessionID, deviceID, adminID, mode)
	return err
}

// Close stamps a session as finished. The WHERE clause on ended_at makes this
// idempotent, which matters because the viewer disconnecting and the agent dropping
// both legitimately try to close the same session.
func (s *SQLStore) Close(ctx context.Context, sessionID, status string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET ended_at = NOW(), status = $2
		WHERE id = $1 AND ended_at IS NULL
	`, sessionID, status)
	return err
}

var (
	_ DeviceStore  = (*SQLStore)(nil)
	_ SessionStore = (*SQLStore)(nil)
)
