// Package presence tracks which devices are online right now and which are in a
// session. It is deliberately in-memory: SDS 6 defers Redis, which is legitimate for
// a single backend instance.
//
// What keeps that decision cheap to reverse is the PresenceStore and SessionRegistry
// interfaces below. Callers depend on the interfaces, never on MemoryStore, so
// swapping in a Redis-backed implementation later is a wiring change rather than a
// hunt through the codebase. That only holds if it is enforced, hence the compile-time
// assertions at the bottom of this file.
package presence

import (
	"errors"
	"sync"
	"time"

	"drs/backend/internal/models"
)

var (
	// ErrDeviceAlreadyInSession enforces SRS FR-5.3 (one viewer per device).
	ErrDeviceAlreadyInSession = errors.New("device already has an active monitoring session")
	// ErrSessionNotFound is returned when ending a session that is not live.
	ErrSessionNotFound = errors.New("active session not found")
)

// DeviceMeta is what the hub knows about a device the moment it connects. It travels
// with the presence record so that presence events can be filtered by RBAC without a
// database round trip per event per subscriber.
//
// GroupID is carried for the same reason as AssignedAdminID: an Admin may see a device
// because it belongs to a team they are on, and deciding that per event per subscriber
// would otherwise mean a query per event per subscriber.
type DeviceMeta struct {
	OrgID           string
	Type            string
	IPAddress       string
	AssignedAdminID *string
	GroupID         *string
}

// DevicePresence is the live state of one device.
type DevicePresence struct {
	DeviceID        string    `json:"device_id"`
	OrgID           string    `json:"org_id"`
	Type            string    `json:"type"`
	IPAddress       string    `json:"ip_address"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	Status          string    `json:"status"` // online, offline, in_session
	ActiveSessionID string    `json:"active_session_id,omitempty"`
	AssignedAdminID *string   `json:"assigned_admin_id,omitempty"`
	GroupID         *string   `json:"group_id,omitempty"`
}

// ActiveSession is one live monitoring session.
type ActiveSession struct {
	SessionID string    `json:"session_id"`
	DeviceID  string    `json:"device_id"`
	AdminID   string    `json:"admin_id"`
	Mode      string    `json:"mode"`
	StartedAt time.Time `json:"started_at"`
}

// PresenceEvent is broadcast to subscribers on every state change. OrgID,
// AssignedAdminID and GroupID are carried so a subscriber can be filtered by RBAC at the
// point of delivery.
type PresenceEvent struct {
	Type            string    `json:"type"` // device_online, device_offline, session_start, session_end
	DeviceID        string    `json:"device_id"`
	Status          string    `json:"status"`
	SessionID       string    `json:"session_id,omitempty"`
	OrgID           string    `json:"org_id,omitempty"`
	AssignedAdminID *string   `json:"assigned_admin_id,omitempty"`
	GroupID         *string   `json:"group_id,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
}

// PresenceStore tracks device liveness.
type PresenceStore interface {
	SetDeviceOnline(deviceID string, meta DeviceMeta) *DevicePresence
	Touch(deviceID string, ip string)
	// UpdateDeviceAssignment re-points a live device at a new admin/team without waiting
	// for its agent to reconnect. See the implementation for why that matters.
	UpdateDeviceAssignment(deviceID string, adminID, groupID *string)
	SetDeviceOffline(deviceID string)
	IsDeviceOnline(deviceID string) bool
	GetDevicePresence(deviceID string) (*DevicePresence, bool)
	GetAllOnlineDevices() map[string]DevicePresence
	Subscribe(subscriberID string) chan PresenceEvent
	Unsubscribe(subscriberID string, ch chan PresenceEvent)
}

// SessionRegistry tracks live sessions and enforces one per device.
type SessionRegistry interface {
	RegisterSession(sessionID, deviceID, adminID, mode string) (*ActiveSession, error)
	EndSession(sessionID string) (*ActiveSession, bool)
	GetActiveSessionByDevice(deviceID string) (*ActiveSession, bool)
	GetActiveSession(sessionID string) (*ActiveSession, bool)
	GetAllActiveSessions() map[string]ActiveSession
}

// MemoryStore is the single-instance implementation of both interfaces.
type MemoryStore struct {
	mu          sync.RWMutex
	devices     map[string]*DevicePresence
	sessions    map[string]*ActiveSession
	deviceSess  map[string]string // deviceID -> sessionID
	subscribers map[string][]chan PresenceEvent
}

// NewMemoryStore builds an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		devices:     make(map[string]*DevicePresence),
		sessions:    make(map[string]*ActiveSession),
		deviceSess:  make(map[string]string),
		subscribers: make(map[string][]chan PresenceEvent),
	}
}

// SetDeviceOnline records a device as connected, preserving in_session state across a
// reconnect that happens mid-session.
func (s *MemoryStore) SetDeviceOnline(deviceID string, meta DeviceMeta) *DevicePresence {
	s.mu.Lock()

	p, exists := s.devices[deviceID]
	if !exists {
		p = &DevicePresence{DeviceID: deviceID}
		s.devices[deviceID] = p
	}
	p.OrgID = meta.OrgID
	p.Type = meta.Type
	p.IPAddress = meta.IPAddress
	p.AssignedAdminID = meta.AssignedAdminID
	p.GroupID = meta.GroupID
	p.LastSeenAt = time.Now()

	if _, inSess := s.deviceSess[deviceID]; inSess {
		p.Status = models.DeviceStatusInSession
	} else {
		p.Status = models.DeviceStatusOnline
	}

	snapshot := *p
	event := PresenceEvent{
		Type:            "device_online",
		DeviceID:        deviceID,
		Status:          p.Status,
		OrgID:           p.OrgID,
		AssignedAdminID: p.AssignedAdminID,
		GroupID:         p.GroupID,
		Timestamp:       time.Now(),
	}
	subs := s.subscriberSnapshotLocked()
	s.mu.Unlock()

	// Published after releasing the lock, but from *this* goroutine rather than a new
	// one. Publishing from a goroutine (as this package used to) meant two state
	// changes could reach a subscriber in the opposite order to which they happened,
	// so a device could latch as offline after coming back online.
	publish(subs, event)
	return &snapshot
}

// Touch refreshes last-seen on a heartbeat. It emits no event: heartbeats are
// frequent and a subscriber only cares about transitions.
func (s *MemoryStore) Touch(deviceID string, ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.devices[deviceID]; ok {
		p.LastSeenAt = time.Now()
		if ip != "" {
			p.IPAddress = ip
		}
	}
}

// UpdateDeviceAssignment re-points a live device at a new admin and team.
//
// Presence meta is otherwise written only when an agent connects, so before this existed
// reassigning a device that was already online left the presence record pointing at the
// previous admin until that agent happened to reconnect. While groups were a caption that
// was a cosmetic staleness. Now that team membership grants visibility, it is the
// difference between revoking someone's access and believing you had.
//
// A device that is not currently online has no presence record to correct; the next
// SetDeviceOnline reads the fresh row from the database anyway.
func (s *MemoryStore) UpdateDeviceAssignment(deviceID string, adminID, groupID *string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.devices[deviceID]; ok {
		p.AssignedAdminID = adminID
		p.GroupID = groupID
	}
}

// SetDeviceOffline removes a device and tears down any session it was in.
func (s *MemoryStore) SetDeviceOffline(deviceID string) {
	s.mu.Lock()
	if _, exists := s.devices[deviceID]; !exists {
		s.mu.Unlock()
		return
	}
	orgID := s.devices[deviceID].OrgID
	assigned := s.devices[deviceID].AssignedAdminID
	group := s.devices[deviceID].GroupID
	delete(s.devices, deviceID)

	if sessID, inSess := s.deviceSess[deviceID]; inSess {
		delete(s.deviceSess, deviceID)
		delete(s.sessions, sessID)
	}

	event := PresenceEvent{
		Type:            "device_offline",
		DeviceID:        deviceID,
		Status:          models.DeviceStatusOffline,
		OrgID:           orgID,
		AssignedAdminID: assigned,
		GroupID:         group,
		Timestamp:       time.Now(),
	}
	subs := s.subscriberSnapshotLocked()
	s.mu.Unlock()

	publish(subs, event)
}

// IsDeviceOnline reports whether a device is connected (in a session counts).
func (s *MemoryStore) IsDeviceOnline(deviceID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, exists := s.devices[deviceID]
	return exists && (p.Status == models.DeviceStatusOnline || p.Status == models.DeviceStatusInSession)
}

// GetDevicePresence returns a copy of one device's state.
func (s *MemoryStore) GetDevicePresence(deviceID string) (*DevicePresence, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, exists := s.devices[deviceID]
	if !exists {
		return nil, false
	}
	cpy := *p
	return &cpy, true
}

// GetAllOnlineDevices returns a copy of every connected device's state.
func (s *MemoryStore) GetAllOnlineDevices() map[string]DevicePresence {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]DevicePresence, len(s.devices))
	for k, v := range s.devices {
		result[k] = *v
	}
	return result
}

// RegisterSession claims a device for one session, enforcing SRS FR-5.3.
func (s *MemoryStore) RegisterSession(sessionID, deviceID, adminID, mode string) (*ActiveSession, error) {
	s.mu.Lock()

	if _, inSess := s.deviceSess[deviceID]; inSess {
		s.mu.Unlock()
		return nil, ErrDeviceAlreadyInSession
	}

	sess := &ActiveSession{
		SessionID: sessionID,
		DeviceID:  deviceID,
		AdminID:   adminID,
		Mode:      mode,
		StartedAt: time.Now(),
	}
	s.sessions[sessionID] = sess
	s.deviceSess[deviceID] = sessionID

	event := PresenceEvent{
		Type:      "session_start",
		DeviceID:  deviceID,
		Status:    models.DeviceStatusInSession,
		SessionID: sessionID,
		Timestamp: time.Now(),
	}
	if dev, ok := s.devices[deviceID]; ok {
		dev.Status = models.DeviceStatusInSession
		dev.ActiveSessionID = sessionID
		event.OrgID = dev.OrgID
		event.AssignedAdminID = dev.AssignedAdminID
		event.GroupID = dev.GroupID
	}
	snapshot := *sess
	subs := s.subscriberSnapshotLocked()
	s.mu.Unlock()

	publish(subs, event)
	return &snapshot, nil
}

// EndSession releases a device. It is safe to call for an unknown session, which
// matters because both the viewer disconnecting and the agent dropping can race to
// end the same one.
func (s *MemoryStore) EndSession(sessionID string) (*ActiveSession, bool) {
	s.mu.Lock()

	sess, exists := s.sessions[sessionID]
	if !exists {
		s.mu.Unlock()
		return nil, false
	}
	delete(s.sessions, sessionID)
	delete(s.deviceSess, sess.DeviceID)

	event := PresenceEvent{
		Type:      "session_end",
		DeviceID:  sess.DeviceID,
		Status:    models.DeviceStatusOnline,
		SessionID: sessionID,
		Timestamp: time.Now(),
	}
	if dev, ok := s.devices[sess.DeviceID]; ok {
		dev.Status = models.DeviceStatusOnline
		dev.ActiveSessionID = ""
		event.OrgID = dev.OrgID
		event.AssignedAdminID = dev.AssignedAdminID
		event.GroupID = dev.GroupID
	}
	snapshot := *sess
	subs := s.subscriberSnapshotLocked()
	s.mu.Unlock()

	publish(subs, event)
	return &snapshot, true
}

// GetActiveSessionByDevice finds the live session on a device, if any.
func (s *MemoryStore) GetActiveSessionByDevice(deviceID string) (*ActiveSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sessID, exists := s.deviceSess[deviceID]
	if !exists {
		return nil, false
	}
	sess, ok := s.sessions[sessID]
	if !ok {
		return nil, false
	}
	cpy := *sess
	return &cpy, true
}

// GetActiveSession looks a live session up by id.
func (s *MemoryStore) GetActiveSession(sessionID string) (*ActiveSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, exists := s.sessions[sessionID]
	if !exists {
		return nil, false
	}
	cpy := *sess
	return &cpy, true
}

// GetAllActiveSessions returns a copy of every live session.
func (s *MemoryStore) GetAllActiveSessions() map[string]ActiveSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]ActiveSession, len(s.sessions))
	for k, v := range s.sessions {
		result[k] = *v
	}
	return result
}

// Subscribe opens a buffered event channel for a subscriber.
func (s *MemoryStore) Subscribe(subscriberID string) chan PresenceEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan PresenceEvent, 16)
	s.subscribers[subscriberID] = append(s.subscribers[subscriberID], ch)
	return ch
}

// Unsubscribe removes and closes a subscriber channel.
func (s *MemoryStore) Unsubscribe(subscriberID string, ch chan PresenceEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subs := s.subscribers[subscriberID]
	for i, sub := range subs {
		if sub == ch {
			s.subscribers[subscriberID] = append(subs[:i], subs[i+1:]...)
			close(ch)
			break
		}
	}
	if len(s.subscribers[subscriberID]) == 0 {
		delete(s.subscribers, subscriberID)
	}
}

// subscriberSnapshotLocked copies the current channel set. The caller must hold the
// lock; publishing happens after it is released, against this snapshot.
func (s *MemoryStore) subscriberSnapshotLocked() []chan PresenceEvent {
	total := 0
	for _, chans := range s.subscribers {
		total += len(chans)
	}
	if total == 0 {
		return nil
	}
	out := make([]chan PresenceEvent, 0, total)
	for _, chans := range s.subscribers {
		out = append(out, chans...)
	}
	return out
}

// publish does a non-blocking send to each subscriber. A subscriber too slow to keep
// up loses events rather than stalling the hub; presence is refreshed on reconnect
// and by GET /api/devices, so a dropped event is recoverable while a blocked hub is not.
func publish(subs []chan PresenceEvent, event PresenceEvent) {
	for _, ch := range subs {
		select {
		case ch <- event:
		default:
		}
	}
}

// Enforce the seam SDS 6 depends on: if MemoryStore ever stops satisfying these,
// this file stops compiling rather than the Redis migration getting quietly harder.
var (
	_ PresenceStore   = (*MemoryStore)(nil)
	_ SessionRegistry = (*MemoryStore)(nil)
)
