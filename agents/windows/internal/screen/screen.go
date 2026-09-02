// Package screen owns screen capture and the WebRTC video session.
//
// At most one session runs at a time. The backend enforces one viewer per device, and
// this mirrors that: a new start replaces whatever was running, and the socket dropping
// stops everything, so no capture goroutine can outlive the connection that authorised
// it.
package screen

import (
	"context"
	"log"
	"sync"

	"drs/agent/windows/internal/protocol"
)

// FrameSender writes one already-encoded envelope up the agent socket. It is expected
// to serialise with every other writer (the conn package guards them all with one mutex).
type FrameSender func(frame []byte) error

// session is one live WebRTC session, as far as the Manager needs to know.
type session interface {
	HandleAnswer(sdp string)
	HandleICECandidate(c protocol.ICECandidate)
	Close()
}

// Manager routes session commands from the socket reader to the live session.
type Manager struct {
	send FrameSender

	mu        sync.Mutex
	cancel    context.CancelFunc
	sessionID string
	live      session
}

// NewManager builds a Manager that emits signaling through send.
func NewManager(send FrameSender) *Manager {
	return &Manager{send: send}
}

// Start begins a session, replacing any that was already running.
func (m *Manager) Start(parent context.Context, cmd protocol.StartSession) {
	m.mu.Lock()
	m.stopLocked()

	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	m.sessionID = cmd.SessionID
	m.mu.Unlock()

	sess, err := newVP8Session(ctx, cmd, m.send)
	if err != nil {
		// There is no JPEG fallback by design: relaying video through the server is
		// what this architecture exists to avoid. So a failure here is reported to the
		// operator rather than silently downgraded to something worse.
		log.Printf("agent: session %s could not start: %v", short(cmd.SessionID), err)
		m.reportError(cmd.SessionID, err.Error())
		cancel()
		return
	}

	m.mu.Lock()
	// Guard against a stop or a replacement having arrived while the peer connection
	// was being built.
	if m.sessionID != cmd.SessionID {
		m.mu.Unlock()
		sess.Close()
		return
	}
	m.live = sess
	m.mu.Unlock()

	log.Printf("agent: session %s streaming (webrtc/vp8)", short(cmd.SessionID))
}

// HandleAnswer applies the browser's SDP answer.
func (m *Manager) HandleAnswer(a protocol.SDP) {
	if s := m.matching(a.SessionID); s != nil {
		s.HandleAnswer(a.SDP)
	}
}

// HandleICECandidate applies a trickled candidate from the browser.
func (m *Manager) HandleICECandidate(c protocol.ICECandidate) {
	if s := m.matching(c.SessionID); s != nil {
		s.HandleICECandidate(c)
	}
}

// matching returns the live session only if the id matches, so signaling left over
// from a replaced session is discarded rather than applied to the current one.
func (m *Manager) matching(sessionID string) session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.live != nil && m.sessionID == sessionID {
		return m.live
	}
	return nil
}

// Stop ends the named session, ignoring a stale stop for one already replaced.
func (m *Manager) Stop(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessionID == sessionID {
		m.stopLocked()
	}
}

// StopAll ends any session. Called when the socket drops so a reconnect starts clean.
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
}

// stopLocked tears down the live session. The caller must hold m.mu.
func (m *Manager) stopLocked() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.live != nil {
		m.live.Close()
		m.live = nil
	}
	m.sessionID = ""
}

// Active reports whether a session is running, for the tray indicator.
func (m *Manager) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.live != nil
}

func (m *Manager) reportError(sessionID, message string) {
	frame, err := protocol.Encode(protocol.TypeSessionError, protocol.SessionErrorMsg{
		SessionID: sessionID,
		Message:   message,
	})
	if err != nil {
		return
	}
	_ = m.send(frame)
}
