package ws

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"drs/backend/internal/auth"
	"drs/backend/internal/models"
	"drs/backend/internal/presence"
	"drs/backend/pkg/protocol"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var errAgentOffline = errors.New("device is offline")

// viewerConn is one operator watching one device.
type viewerConn struct {
	*socket
	hub       *Hub
	userID    string
	email     string
	role      string
	orgID     string
	deviceID  string
	sessionID string
	ip        string // captured at open; used to attribute per-command terminal audit

	// Capabilities the device consented to at enrollment, copied from the device row when
	// the session opens. allowTerminal gates the one operator->device input path.
	allowScreen   bool
	allowTerminal bool

	endOnce sync.Once
}

// browserUpgrader builds the upgrader for browser-originated sockets.
//
// Two differences from the agent's. Origin is checked, because a browser socket does
// carry ambient authority and a page on another origin must not be able to open one.
// And "bearer" is advertised as a supported subprotocol: the browser cannot set an
// Authorization header on a WebSocket, so the JWT rides as the second subprotocol
// value and we negotiate back only the first, keeping the token out of the response.
func (h *Hub) browserUpgrader() websocket.Upgrader {
	return websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		Subprotocols:    []string{"bearer"},
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true // non-browser client; it has no ambient authority to abuse
			}
			for _, allowed := range h.allowedOrigins {
				if allowed == "*" || strings.EqualFold(allowed, origin) {
					return true
				}
			}
			return false
		},
	}
}

// bearerFromSubprotocol pulls the JWT out of "Sec-WebSocket-Protocol: bearer, <token>".
func bearerFromSubprotocol(r *http.Request) string {
	raw := r.Header.Get("Sec-WebSocket-Protocol")
	parts := strings.Split(raw, ",")
	if len(parts) < 2 || !strings.EqualFold(strings.TrimSpace(parts[0]), "bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// authenticateBrowser validates the JWT before the upgrade, so a rejection is a real
// HTTP status the client can read rather than an immediately-closed socket.
func (h *Hub) authenticateBrowser(w http.ResponseWriter, r *http.Request) (*auth.JWTClaims, bool) {
	token := bearerFromSubprotocol(r)
	if token == "" {
		http.Error(w, "missing bearer subprotocol", http.StatusUnauthorized)
		return nil, false
	}
	claims, err := auth.ValidateJWT(token, h.jwtSecret)
	if err != nil {
		http.Error(w, "invalid or expired token", http.StatusUnauthorized)
		return nil, false
	}
	if claims.Role != models.RoleAdmin && claims.Role != models.RoleSuperAdmin {
		http.Error(w, "admin access required", http.StatusForbidden)
		return nil, false
	}
	return claims, true
}

// clampQuery reads an optional positive integer query parameter and clamps it into
// [min, max], falling back to def when it is absent, unparseable or non-positive.
//
// Absent means "use the deployment default", so every caller that predates the monitoring
// wall keeps behaving exactly as before. Out-of-range is clamped rather than rejected: a
// tile asking for 1px wide is a client bug, not an attack, and failing the whole session
// over it would be worse than quietly giving it the smallest sane size.
func clampQuery(r *http.Request, name string, min, max, def int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// canViewDevice is the RBAC rule from SRS FR-1.3, FR-1.4 and FR-6.4: a Super Admin sees
// every device; an Admin sees a device assigned to them, or one that sits in a team they
// belong to.
//
// The team half is additive, not a replacement. An Admin who is on no team keeps exactly
// the access they had before teams meant anything, so an empty user_group_members table
// cannot lock anybody out.
func canViewDevice(claims *auth.JWTClaims, device Device, viewerGroups map[string]bool) bool {
	if claims.Role == models.RoleSuperAdmin {
		return true
	}
	if device.AssignedAdminID != nil && *device.AssignedAdminID == claims.UserID {
		return true
	}
	return device.GroupID != nil && viewerGroups[*device.GroupID]
}

// ServeSession handles one operator watching one device.
func (h *Hub) ServeSession(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.authenticateBrowser(w, r)
	if !ok {
		return
	}

	deviceID := r.URL.Query().Get("deviceId")
	if _, err := uuid.Parse(deviceID); err != nil {
		http.Error(w, "deviceId query parameter is required", http.StatusBadRequest)
		return
	}

	device, found, err := h.devices.Lookup(r.Context(), deviceID)
	if err != nil {
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	// Membership is read live on every session open rather than trusted from the JWT.
	// Putting team ids in the token would mean a 24h window in which removing someone
	// from a team did not actually remove their access, and there is no revocation list.
	viewerGroups, err := h.devices.GroupsForUser(r.Context(), claims.UserID)
	if err != nil {
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	// A device the operator may not see and a device that does not exist get the same
	// answer, so the endpoint cannot be used to enumerate other admins' devices.
	if !found || !canViewDevice(claims, device, viewerGroups) {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	// A device that consented to neither screen nor terminal exposes nothing, so there is
	// no session to open. Refuse before upgrading rather than opening a socket that can do
	// nothing.
	if !device.AllowScreen && !device.AllowTerminal {
		http.Error(w, "device is not sharing its screen or terminal", http.StatusForbidden)
		return
	}
	if !h.agentOnline(deviceID) {
		http.Error(w, "device is offline", http.StatusConflict)
		return
	}

	// The viewer may ask this session to be cheaper than the deployment default. The
	// monitoring wall runs a dozen tiles at once and asks each for a few frames a second
	// at thumbnail width; the single-device viewer asks for nothing and gets the default.
	fps := clampQuery(r, "fps", minSessionFPS, maxSessionFPS, h.sessionFPS)
	maxWidth := clampQuery(r, "maxWidth", minSessionMaxWidth, maxSessionMaxWidth, h.sessionMaxWidth)

	upgrader := h.browserUpgrader()
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(viewerReadLimit)
	sock := &socket{conn: conn}

	sessionID := uuid.New().String()
	vc := &viewerConn{
		socket:    sock,
		hub:       h,
		userID:    claims.UserID,
		email:     claims.Email,
		role:      claims.Role,
		orgID:     claims.OrgID,
		deviceID:  deviceID,
		sessionID: sessionID,
		ip:        clientIP(r),

		allowScreen:   device.AllowScreen,
		allowTerminal: device.AllowTerminal,
	}

	// Claim the device before anything else. This is where SRS FR-5.3 is enforced, and
	// doing it first means a rejected second viewer never disturbs the running session.
	if _, err := h.registry.RegisterSession(sessionID, deviceID, claims.UserID, models.SessionModeView); err != nil {
		_ = sock.sendEnvelope(protocol.TypeSessionError, protocol.SessionErrorMsg{
			Message: "device is already being viewed",
		})
		sock.close(websocket.ClosePolicyViolation, "device busy")
		return
	}

	h.registerViewer(vc)

	// Every exit path from here closes the session out: normal disconnect, browser
	// crash, network drop, panic. Nothing is left to the client remembering to say
	// goodbye, which is what made sessions leak before.
	defer vc.endSession(models.SessionStatusCompleted)

	if err := h.sessions.Open(context.Background(), sessionID, deviceID, claims.UserID, models.SessionModeView); err != nil {
		log.Printf("[WS WARN] could not persist session %s: %v", sessionID, err)
	}
	if h.audit != nil {
		h.audit.Log(claims.OrgID, claims.UserID, claims.Email, "session_start", "device", deviceID, map[string]any{
			"session_id":  sessionID,
			"device_name": device.Name,
			"mode":        models.SessionModeView,
		}, clientIP(r))
	}

	// Tell the browser what this device consented to, so it renders the screen area only
	// when screen sharing is on and the terminal only when terminal access is on.
	_ = sock.sendEnvelope(protocol.TypeSessionCapabilities, protocol.SessionCapabilities{
		SessionID:     sessionID,
		AllowScreen:   vc.allowScreen,
		AllowTerminal: vc.allowTerminal,
	})

	// Tell the agent to start producing, but only if the device shares its screen. A
	// terminal-only device opens the session socket (so commands can flow) without any
	// capture ever starting. The ICE list is attached here and fetched by the browser
	// from GET /api/session/ice, so both peers negotiate against an identical view of the
	// network.
	if vc.allowScreen {
		start, err := protocol.Encode(protocol.TypeStartSession, protocol.StartSession{
			SessionID:          sessionID,
			Mode:               protocol.ModeWebRTC,
			FPS:                fps,
			MaxWidth:           maxWidth,
			ICEServers:         h.ice.Servers(),
			ICETransportPolicy: h.ice.TransportPolicy(),
			Operator:           claims.Email,
		})
		if err == nil {
			if err := h.sendToAgent(deviceID, start); err != nil {
				_ = sock.sendEnvelope(protocol.TypeSessionError, protocol.SessionErrorMsg{
					SessionID: sessionID,
					Message:   "device went offline",
				})
				return
			}
		}
	}

	log.Printf("[WS] session %s opened on device %s by %s", short(sessionID), deviceID, claims.Email)
	h.readViewer(vc)
}

// readViewer is the operator's inbound loop. The allowlist is the security boundary:
// a browser may only answer an offer, trickle candidates, and — since the terminal
// feature — send a command to run. It cannot, for instance, send an offer of its own,
// or a start_session, or anything addressed elsewhere.
func (h *Hub) readViewer(vc *viewerConn) {
	defer recoverConn("viewer " + vc.userID)

	for {
		// Generous but finite: a viewer that has stopped reacting entirely should not
		// hold the device's one session slot open forever.
		_ = vc.conn.SetReadDeadline(time.Now().Add(10 * time.Minute))
		_, data, err := vc.conn.ReadMessage()
		if err != nil {
			return
		}
		env, err := protocol.DecodeEnvelope(data)
		if err != nil {
			continue
		}

		switch env.Type {
		case protocol.TypeAnswer, protocol.TypeICECandidate:
			// Forwarded verbatim to the device *this socket* is bound to. The payload's
			// own sessionId is never consulted for routing, so naming another session
			// achieves nothing.
			if err := h.sendToAgent(vc.deviceID, data); err != nil {
				return
			}
		case protocol.TypeTerminalCommand:
			// The one relayed frame the backend parses rather than passing blind: every
			// command an operator runs on a device is written to the audit trail before
			// it is forwarded, since this is the first operator->device input path and it
			// carries real power. Authorization is not re-checked here — the operator
			// already passed canViewDevice when this socket opened.
			h.relayTerminalCommand(vc, env.Data, data)
		default:
			// Ignored. Notably this drops an offer from the browser: the agent is the
			// offerer, and accepting a browser offer would invert the negotiation.
		}
	}
}

// relayTerminalCommand audits one terminal command, then forwards it to the device. The
// audit entry is written even if forwarding then fails, so an attempt against an
// offline device is still recorded.
func (h *Hub) relayTerminalCommand(vc *viewerConn, payload []byte, frame []byte) {
	var cmd protocol.TerminalCommand
	if err := protocol.DecodeData(payload, &cmd); err != nil {
		return // unparseable command: nothing to audit or run
	}
	// Consent gate: a device that did not opt into terminal access never receives a
	// command, whoever the operator is. The attempt is still audited below so a refused
	// command is on the record, then answered with an error instead of being relayed.
	if !vc.allowTerminal {
		if h.audit != nil {
			h.audit.Log(vc.orgID, vc.userID, vc.email, "terminal_command_denied", "device", vc.deviceID, map[string]any{
				"session_id": vc.sessionID,
				"command":    cmd.Command,
				"shell":      cmd.Shell,
			}, vc.ip)
		}
		_ = vc.sendEnvelope(protocol.TypeTerminalResult, protocol.TerminalResult{
			SessionID: vc.sessionID,
			CommandID: cmd.CommandID,
			ExitCode:  -1,
			Error:     "terminal access is not enabled on this device",
		})
		return
	}
	if h.audit != nil {
		h.audit.Log(vc.orgID, vc.userID, vc.email, "terminal_command", "device", vc.deviceID, map[string]any{
			"session_id": vc.sessionID,
			"command":    cmd.Command,
			"shell":      cmd.Shell,
		}, vc.ip)
	}
	if err := h.sendToAgent(vc.deviceID, frame); err != nil {
		_ = vc.sendEnvelope(protocol.TypeSessionError, protocol.SessionErrorMsg{
			SessionID: vc.sessionID,
			Message:   "device went offline",
		})
	}
}

func (h *Hub) registerViewer(vc *viewerConn) {
	h.mu.Lock()
	previous := h.viewers[vc.deviceID]
	h.viewers[vc.deviceID] = vc
	h.mu.Unlock()

	if previous != nil && previous != vc {
		previous.close(websocket.ClosePolicyViolation, "replaced by newer viewer")
	}
}

// endSession is the single teardown path for a session, idempotent because the viewer
// disconnecting and the agent dropping can both reach it.
func (vc *viewerConn) endSession(status string) {
	vc.endOnce.Do(func() {
		h := vc.hub

		h.mu.Lock()
		if current, ok := h.viewers[vc.deviceID]; ok && current == vc {
			delete(h.viewers, vc.deviceID)
		}
		h.mu.Unlock()

		// Stop the capture. Best-effort: if the agent is what went away, this fails
		// harmlessly and the rest of the teardown still runs.
		if frame, err := protocol.Encode(protocol.TypeStopSession, protocol.StopSession{
			SessionID: vc.sessionID,
		}); err == nil {
			_ = h.sendToAgent(vc.deviceID, frame)
		}

		h.registry.EndSession(vc.sessionID)
		if err := h.sessions.Close(context.Background(), vc.sessionID, status); err != nil {
			log.Printf("[WS WARN] could not close session %s: %v", vc.sessionID, err)
		}
		if h.audit != nil {
			h.audit.Log(vc.orgID, vc.userID, vc.email, "session_end", "device", vc.deviceID, map[string]any{
				"session_id": vc.sessionID,
				"status":     status,
			}, "")
		}

		vc.close(websocket.CloseNormalClosure, "")
		log.Printf("[WS] session %s closed (%s)", short(vc.sessionID), status)
	})
}

// ServePresence streams live device status to a dashboard.
//
// This exists because the session socket is per-device and only open while watching:
// without it the device list could not show live online/offline state (SRS FR-2.4)
// without polling.
func (h *Hub) ServePresence(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.authenticateBrowser(w, r)
	if !ok {
		return
	}

	// The viewer's team memberships are read once, here, and held for the life of the
	// socket. Presence carries the assignment fields on every event precisely so that
	// filtering costs no database round trip per event per subscriber, and re-querying
	// membership per event would give that back.
	//
	// The cost is bounded and deliberate: dropping an Admin from a team does not cut
	// their presence feed until they reconnect, so for the rest of that socket's life
	// they can still see whether a device they no longer have access to is online. Every
	// path that exposes actual data — the REST list, and opening /ws/session — reads
	// membership live, so nothing can be viewed or run on that basis.
	viewerGroups, err := h.devices.GroupsForUser(r.Context(), claims.UserID)
	if err != nil {
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}

	upgrader := h.browserUpgrader()
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(viewerReadLimit)
	sock := &socket{conn: conn}
	defer sock.close(websocket.CloseNormalClosure, "")

	events := h.presence.Subscribe(claims.UserID)
	defer h.presence.Unsubscribe(claims.UserID, events)

	// Send the current state immediately, so a dashboard that connects mid-flight is
	// correct without waiting for the next transition.
	for deviceID, p := range h.presence.GetAllOnlineDevices() {
		if !visibleToAdmin(claims, p.OrgID, p.AssignedAdminID, p.GroupID, viewerGroups) {
			continue
		}
		_ = sock.sendEnvelope(protocol.TypePresenceUpdate, protocol.PresenceUpdate{
			DeviceID: deviceID,
			Status:   p.Status,
		})
	}

	// A reader goroutine turns a closed browser tab into a closed channel. Without it
	// a disconnected dashboard would sit in the subscriber list until the next event
	// failed to write.
	done := make(chan struct{})
	go func() {
		defer recoverConn("presence reader " + claims.UserID)
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-done:
			return
		case event, open := <-events:
			if !open {
				return
			}
			if !visibleToAdmin(claims, event.OrgID, event.AssignedAdminID, event.GroupID, viewerGroups) {
				continue
			}
			if err := sock.sendEnvelope(protocol.TypePresenceUpdate, protocol.PresenceUpdate{
				DeviceID:  event.DeviceID,
				Status:    event.Status,
				SessionID: event.SessionID,
			}); err != nil {
				return
			}
		}
	}
}

// visibleToAdmin applies the same RBAC rule as canViewDevice to a presence event, so
// an Admin is not told about devices belonging to another Admin or another team.
//
// viewerGroups is the set the presence socket read once at upgrade time, not a fresh
// lookup. See ServePresence for why, and for what that costs.
func visibleToAdmin(claims *auth.JWTClaims, orgID string, assignedAdminID, groupID *string, viewerGroups map[string]bool) bool {
	if claims.Role == models.RoleSuperAdmin {
		return orgID == "" || orgID == claims.OrgID
	}
	if assignedAdminID != nil && *assignedAdminID == claims.UserID {
		return true
	}
	return groupID != nil && viewerGroups[*groupID]
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// Keep the presence type assertions honest about what the hub needs.
var (
	_ presence.PresenceStore   = (*presence.MemoryStore)(nil)
	_ presence.SessionRegistry = (*presence.MemoryStore)(nil)
)
