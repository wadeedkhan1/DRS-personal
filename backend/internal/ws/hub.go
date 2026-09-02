// Package ws is the signaling relay. It owns three WebSocket endpoints:
//
//	/ws/agent                    the device's outbound, always-open control socket
//	/ws/session?deviceId=<uuid>  one operator watching one device
//	/ws/presence                 the dashboard's live online/offline feed
//
// Two rules govern everything here.
//
// First, the relay never interprets media or SDP. It forwards signaling frames
// verbatim between the two peers and then gets out of the way; once ICE completes the
// video never touches this process. That is the whole point of the architecture (SDS
// 4) and the reason a modest VPS can carry many sessions.
//
// Second, routing is by *connection identity*, never by an id in the payload. A frame
// is delivered to the peer the hub's own maps say is on the other end of this
// session. A client cannot reach a session it does not own by naming it, which is the
// class of bug the previous implementation had.
package ws

import (
	"context"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"drs/backend/internal/audit"
	"drs/backend/internal/ice"
	"drs/backend/internal/models"
	"drs/backend/internal/presence"
	"drs/backend/pkg/protocol"

	"github.com/gorilla/websocket"
)

const (
	// agentReadLimit caps an inbound agent frame. Signaling frames are small; SDP is
	// the largest of them at a few KB. This is generous enough to never truncate one
	// and small enough that a rogue agent cannot exhaust memory.
	agentReadLimit = 1 << 20 // 1 MiB
	// viewerReadLimit is tighter: a browser only ever sends an answer or a candidate.
	viewerReadLimit = 64 << 10 // 64 KiB

	writeTimeout     = 10 * time.Second
	helloTimeout     = 10 * time.Second
	defaultHeartbeat = 10 * time.Second

	// Fallbacks for the frame rate and width asked of the agent when config does not
	// override them. The agent clamps both again on its side, so these are a request,
	// not a promise.
	//
	// 720p at 24 fps rather than the earlier 900p at 15: every stage of the agent's
	// capture pipeline costs time proportional to pixel count, and this is the point
	// where it holds a smooth rate on ordinary hardware.
	defaultSessionFPS      = 24
	defaultSessionMaxWidth = 1280
)

// agentUpgrader accepts the device's socket. Origin checking is deliberately skipped:
// the agent is a native process, not a browser, so there is no ambient cookie
// authority for a cross-site request to abuse. Authentication is the Hello frame.
var agentUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Hub is the relay. It holds one live socket per device and one per watching operator.
type Hub struct {
	devices  DeviceStore
	sessions SessionStore
	presence presence.PresenceStore
	registry presence.SessionRegistry
	audit    *audit.Logger
	ice      *ice.Provider

	jwtSecret         string
	allowedOrigins    []string
	heartbeatInterval time.Duration
	sessionFPS        int
	sessionMaxWidth   int

	mu      sync.RWMutex
	agents  map[string]*agentConn  // deviceID -> live agent socket
	viewers map[string]*viewerConn // deviceID -> operator currently watching it
}

// Options configures a Hub.
type Options struct {
	Devices           DeviceStore
	Sessions          SessionStore
	Presence          presence.PresenceStore
	Registry          presence.SessionRegistry
	Audit             *audit.Logger
	ICE               *ice.Provider
	JWTSecret         string
	AllowedOrigins    []string
	HeartbeatInterval time.Duration
	SessionFPS        int
	SessionMaxWidth   int
}

// NewHub builds a Hub.
func NewHub(opt Options) *Hub {
	if opt.HeartbeatInterval <= 0 {
		opt.HeartbeatInterval = defaultHeartbeat
	}
	if opt.SessionFPS <= 0 {
		opt.SessionFPS = defaultSessionFPS
	}
	if opt.SessionMaxWidth <= 0 {
		opt.SessionMaxWidth = defaultSessionMaxWidth
	}
	return &Hub{
		devices:           opt.Devices,
		sessions:          opt.Sessions,
		presence:          opt.Presence,
		registry:          opt.Registry,
		audit:             opt.Audit,
		ice:               opt.ICE,
		jwtSecret:         opt.JWTSecret,
		allowedOrigins:    opt.AllowedOrigins,
		heartbeatInterval: opt.HeartbeatInterval,
		sessionFPS:        opt.SessionFPS,
		sessionMaxWidth:   opt.SessionMaxWidth,
		agents:            make(map[string]*agentConn),
		viewers:           make(map[string]*viewerConn),
	}
}

// socket serialises writes to one WebSocket.
//
// This is not a stylistic choice. gorilla panics on a concurrent write, and an
// unrecovered panic in a connection goroutine takes the whole process down. Several
// independent producers legitimately write to the same socket here (the relay, the
// session commands, the error paths), so every one of them goes through send.
type socket struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func (s *socket) send(frame []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return s.conn.WriteMessage(websocket.TextMessage, frame)
}

func (s *socket) sendEnvelope(t protocol.MsgType, payload any) error {
	frame, err := protocol.Encode(t, payload)
	if err != nil {
		return err
	}
	return s.send(frame)
}

// sendFatal tells a client it will never succeed, so it stops reconnecting.
func (s *socket) sendFatal(message string) {
	_ = s.sendEnvelope(protocol.TypeError, protocol.ErrorMsg{Message: message, Fatal: true})
}

func (s *socket) close(code int, reason string) {
	s.writeMu.Lock()
	_ = s.conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason),
		time.Now().Add(time.Second),
	)
	s.writeMu.Unlock()
	_ = s.conn.Close()
}

type agentConn struct {
	*socket
	device Device
	ip     string
}

// ServeAgent handles the device's outbound control socket.
func (h *Hub) ServeAgent(w http.ResponseWriter, r *http.Request) {
	conn, err := agentUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote the error
	}
	sock := &socket{conn: conn}
	conn.SetReadLimit(agentReadLimit)

	ip := clientIP(r)
	device, ok := h.authenticateAgent(r.Context(), sock)
	if !ok {
		sock.close(websocket.ClosePolicyViolation, "authentication failed")
		return
	}

	ac := &agentConn{socket: sock, device: device, ip: ip}

	// Welcome goes out BEFORE the socket is registered, and that ordering is load-bearing.
	//
	// Registering publishes this connection to every other goroutine, so a viewer
	// teardown racing the registration can reach sendToAgent and deliver a stop_session
	// ahead of the handshake. The agent, still waiting for its first frame, treats
	// anything that is not a welcome as a protocol violation and drops the connection —
	// then reconnects, and races again.
	if err := sock.sendEnvelope(protocol.TypeWelcome, protocol.Welcome{
		HeartbeatIntervalSeconds: int(h.heartbeatInterval.Seconds()),
		ServerTime:               time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		sock.close(websocket.CloseInternalServerErr, "welcome failed")
		return
	}

	h.registerAgent(ac)
	defer h.unregisterAgent(ac)

	log.Printf("[WS] agent connected: device=%s (%s) ip=%s", device.ID, device.Type, ip)
	h.readAgent(ac)
}

// authenticateAgent enforces the Hello handshake: the first frame, within a deadline,
// must be a Hello with a matching protocol version and a valid secret.
func (h *Hub) authenticateAgent(ctx context.Context, sock *socket) (Device, bool) {
	_ = sock.conn.SetReadDeadline(time.Now().Add(helloTimeout))
	_, data, err := sock.conn.ReadMessage()
	if err != nil {
		return Device{}, false
	}

	env, err := protocol.DecodeEnvelope(data)
	if err != nil || env.Type != protocol.TypeHello {
		sock.sendFatal("expected hello frame")
		return Device{}, false
	}

	var hello protocol.Hello
	if err := protocol.DecodeData(env.Data, &hello); err != nil {
		sock.sendFatal("malformed hello")
		return Device{}, false
	}
	if hello.ProtocolVersion != protocol.ProtocolVersion {
		// Fatal on purpose. A version-mismatched agent will never succeed, so letting
		// it retry forever would just be a busy loop against the server.
		sock.sendFatal("unsupported protocol version")
		return Device{}, false
	}

	device, ok, err := h.devices.AuthenticateAgent(ctx, hello.DeviceID, hello.AgentSecret)
	if err != nil {
		log.Printf("[WS ERROR] agent auth lookup failed: %v", err)
		// Transient (database) failure, so NOT fatal: the agent should back off and
		// retry rather than give up permanently on our outage.
		_ = sock.sendEnvelope(protocol.TypeError, protocol.ErrorMsg{Message: "server unavailable"})
		return Device{}, false
	}
	if !ok {
		sock.sendFatal("authentication failed")
		return Device{}, false
	}
	return device, true
}

// readAgent is the agent's inbound loop. Everything the agent may send is either
// liveness or a signaling frame destined for the operator watching it.
func (h *Hub) readAgent(ac *agentConn) {
	defer recoverConn("agent " + ac.device.ID)

	// A device that loses power never sends a close frame, so liveness is inferred
	// from the heartbeat: miss three and the socket is considered dead. Without this
	// a powered-off machine reads as "online" until TCP eventually gives up.
	deadline := 3 * h.heartbeatInterval

	for {
		_ = ac.conn.SetReadDeadline(time.Now().Add(deadline))
		_, data, err := ac.conn.ReadMessage()
		if err != nil {
			return
		}
		env, err := protocol.DecodeEnvelope(data)
		if err != nil {
			continue
		}

		switch env.Type {
		case protocol.TypeHeartbeat:
			var hb protocol.Heartbeat
			if err := protocol.DecodeData(env.Data, &hb); err != nil {
				continue
			}
			h.presence.Touch(ac.device.ID, ac.ip)
			if err := h.devices.RecordHeartbeat(context.Background(), ac.device.ID, ac.ip, hb); err != nil {
				log.Printf("[WS WARN] heartbeat persist failed for %s: %v", ac.device.ID, err)
			}

		case protocol.TypeOffer, protocol.TypeICECandidate,
			protocol.TypeSessionReady, protocol.TypeSessionError,
			protocol.TypeTerminalResult:
			// Relayed verbatim. The destination comes from the hub's viewer map keyed
			// by *this* agent's device id, so an agent can only ever reach the
			// operator watching it. terminal_result carries command output back to the
			// one operator whose command produced it.
			h.forwardToViewer(ac.device.ID, env.Type, data)

		default:
			// Anything else, including a viewer-only message type, is ignored rather
			// than trusted.
		}
	}
}

func (h *Hub) registerAgent(ac *agentConn) {
	h.mu.Lock()
	previous := h.agents[ac.device.ID]
	h.agents[ac.device.ID] = ac
	h.mu.Unlock()

	// Evict explicitly. Overwriting the map entry alone would leave the stale
	// connection's deferred cleanup to delete the *new* entry moments later, making a
	// live device read as offline.
	if previous != nil && previous != ac {
		previous.close(websocket.ClosePolicyViolation, "replaced by newer connection")
	}

	h.presence.SetDeviceOnline(ac.device.ID, presence.DeviceMeta{
		OrgID:           ac.device.OrgID,
		Type:            ac.device.Type,
		IPAddress:       ac.ip,
		AssignedAdminID: ac.device.AssignedAdminID,
	})
	if err := h.devices.SetStatus(context.Background(), ac.device.ID, models.DeviceStatusOnline, ac.ip); err != nil {
		log.Printf("[WS WARN] could not mark %s online: %v", ac.device.ID, err)
	}
}

func (h *Hub) unregisterAgent(ac *agentConn) {
	h.mu.Lock()
	if current, ok := h.agents[ac.device.ID]; ok && current == ac {
		delete(h.agents, ac.device.ID)
	}
	viewer := h.viewers[ac.device.ID]
	h.mu.Unlock()

	// The device is gone, so any session on it is over. Tell the operator rather than
	// leaving them watching a frozen last frame.
	if viewer != nil {
		_ = viewer.sendEnvelope(protocol.TypeSessionError, protocol.SessionErrorMsg{
			SessionID: viewer.sessionID,
			Message:   "device disconnected",
		})
		viewer.endSession("terminated")
	}

	h.presence.SetDeviceOffline(ac.device.ID)
	if err := h.devices.SetStatus(context.Background(), ac.device.ID, models.DeviceStatusOffline, ""); err != nil {
		log.Printf("[WS WARN] could not mark %s offline: %v", ac.device.ID, err)
	}
	ac.close(websocket.CloseNormalClosure, "")
	log.Printf("[WS] agent disconnected: device=%s", ac.device.ID)
}

// forwardToViewer relays an agent frame to whoever is watching that device.
func (h *Hub) forwardToViewer(deviceID string, t protocol.MsgType, frame []byte) {
	h.mu.RLock()
	viewer := h.viewers[deviceID]
	h.mu.RUnlock()
	if viewer == nil {
		return
	}
	if err := viewer.send(frame); err != nil {
		log.Printf("[WS WARN] forward %s to viewer of %s failed: %v", t, deviceID, err)
	}
}

// sendToAgent relays a viewer frame to the device being watched.
func (h *Hub) sendToAgent(deviceID string, frame []byte) error {
	h.mu.RLock()
	agent := h.agents[deviceID]
	h.mu.RUnlock()
	if agent == nil {
		return errAgentOffline
	}
	return agent.send(frame)
}

// agentOnline reports whether a device has a live socket.
func (h *Hub) agentOnline(deviceID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.agents[deviceID] != nil
}

// clientIP prefers the address nginx forwarded. r.RemoteAddr behind a reverse proxy
// is the proxy itself, which would show every device as living at the same address.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Left-most entry is the original client; the rest are proxies.
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xr := r.Header.Get("X-Real-IP"); xr != "" {
		return strings.TrimSpace(xr)
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i > 0 {
		return host[:i]
	}
	return host
}

// recoverConn keeps one malformed connection from taking down the server. Every
// socket goroutine is wrapped: the old implementation had none, so a single
// concurrent-write panic was fatal to every other session too.
func recoverConn(what string) {
	if rec := recover(); rec != nil {
		log.Printf("[WS PANIC] %s: %v\n%s", what, rec, debug.Stack())
	}
}
