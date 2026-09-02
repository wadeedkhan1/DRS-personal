// Package protocol defines the agent<->backend<->browser wire contract once, so the
// three implementations can never drift apart on the details that matter (field
// names, message names, who speaks first).
//
// Every frame on the wire is an Envelope: {"type": "...", "data": {...}}. The type
// selects which struct below Data unmarshals into. JSON field names are camelCase
// because the browser is a first-class peer here, not an afterthought.
//
// The shape of a session, in one place so it is not scattered across three clients:
//
//  1. The agent dials out to /ws/agent and sends Hello. It gets Welcome (authenticated)
//     or an ErrorMsg with Fatal set (rejected for good, stop retrying).
//  2. An operator opens /ws/session?deviceId=... The backend mints a session id and
//     pushes StartSession down the agent's socket, carrying the ICE servers that both
//     peers must share.
//  3. The AGENT is the WebRTC offerer. It owns the media, so it offers: SessionReady
//     first (so the browser can build its RTCPeerConnection before the offer races
//     it), then Offer. The browser answers. ICE trickles both ways.
//  4. The backend relays offer/answer/ice_candidate verbatim. It never parses SDP.
//     Where the media itself goes depends on ICETransportPolicy: normally straight
//     between the peers, or through the TURN relay when a direct path does not exist
//     (or when relay is forced). Either way it does not pass through this process.
//
// Deliberately absent: there is no video-over-WebSocket frame type. Media that has to
// traverse the server goes through TURN instead, which keeps DTLS-SRTP encryption,
// congestion control and packet-loss recovery — none of which a hand-rolled relay over
// the signaling socket would have. There is also no input/remote-control message,
// because this phase is view-only.
package protocol

import (
	"encoding/json"
	"errors"
)

// ProtocolVersion is bumped on any breaking change to the messages below. The backend
// rejects a mismatched agent at Hello rather than failing mysteriously later.
const ProtocolVersion = 1

// MsgType is the discriminator in an Envelope.
type MsgType string

const (
	// Agent -> backend.
	TypeHello        MsgType = "hello"
	TypeHeartbeat    MsgType = "heartbeat"
	TypeSessionError MsgType = "session_error"

	// Backend -> agent.
	TypeWelcome      MsgType = "welcome"
	TypeError        MsgType = "error"
	TypePing         MsgType = "ping"
	TypeStartSession MsgType = "start_session"
	TypeStopSession  MsgType = "stop_session"

	// WebRTC signaling. The agent is the offerer (it produces the media), so
	// TypeSessionReady and TypeOffer flow agent->browser, TypeAnswer flows
	// browser->agent, and TypeICECandidate trickles both ways.
	TypeSessionReady MsgType = "session_ready"
	TypeOffer        MsgType = "offer"
	TypeAnswer       MsgType = "answer"
	TypeICECandidate MsgType = "ice_candidate"

	// Backend -> browser only. The agent never sees this one; it lives here so the
	// browser has a single envelope vocabulary rather than two.
	TypePresenceUpdate MsgType = "presence_update"
)

// SessionMode names the media transport. Only WebRTC exists; the constant stays
// explicit so a future mode is an added value rather than a changed contract.
type SessionMode string

// ModeWebRTC is the only supported media transport.
const ModeWebRTC SessionMode = "webrtc"

// Envelope wraps every frame. Data stays raw so the relay can forward a message
// without knowing, or caring, what is inside it.
type Envelope struct {
	Type MsgType         `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Hello is the agent's first frame and its only authentication. AgentSecret is the
// full-entropy secret issued at enrollment, sent in the clear over TLS; the backend
// stores only its hash.
type Hello struct {
	ProtocolVersion int    `json:"protocolVersion"`
	DeviceID        string `json:"deviceId"`
	AgentSecret     string `json:"agentSecret"`
	AgentVersion    string `json:"agentVersion"`
}

// Welcome acknowledges a successful Hello and tells the agent how often to beat. The
// interval is server-chosen so the read deadline and the beat rate can never disagree.
type Welcome struct {
	HeartbeatIntervalSeconds int    `json:"heartbeatIntervalSeconds"`
	ServerTime               string `json:"serverTime"`
}

// SysInfo is the device metadata refreshed on every heartbeat (SRS FR-2.3).
type SysInfo struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`       // "Windows 11 (10.0.22631)"
	Platform string `json:"platform"` // "windows" | "darwin" | "linux"
}

// Heartbeat is liveness plus cheap telemetry. Its absence past the read deadline is
// how the backend notices a device that lost power instead of disconnecting politely.
type Heartbeat struct {
	CPUPercent int     `json:"cpuPercent"`
	RAMPercent int     `json:"ramPercent"`
	SysInfo    SysInfo `json:"sysInfo"`
}

// ErrorMsg reports a problem. Fatal distinguishes "your credentials are void, stop
// reconnecting" from "something went wrong, retrying is reasonable". Without that
// distinction an agent whose secret was revoked would hammer the server forever.
type ErrorMsg struct {
	Message string `json:"message"`
	Fatal   bool   `json:"fatal"`
}

// StartSession commands the agent to begin capturing. It is a server command, not a
// browser request: the agent only ever captures when the backend, which did the RBAC
// check and wrote the audit entry, tells it to.
type StartSession struct {
	SessionID  string      `json:"sessionId"`
	Mode       SessionMode `json:"mode,omitempty"`
	FPS        int         `json:"fps"`
	MaxWidth   int         `json:"maxWidth"`
	ICEServers []ICEServer `json:"iceServers,omitempty"`
	Operator   string      `json:"operator,omitempty"`

	// ICETransportPolicy is "all" (default) or "relay". "relay" discards host and
	// server-reflexive candidates, so media can only travel via TURN — every session
	// then goes through the server whether or not a direct path exists.
	ICETransportPolicy string `json:"iceTransportPolicy,omitempty"`
}

// ICEServer mirrors the browser's RTCIceServer so the same list can be handed to both
// peers unchanged.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// SDP carries an offer or an answer. SDPType is "offer" or "answer".
type SDP struct {
	SessionID string `json:"sessionId"`
	SDPType   string `json:"sdpType"`
	SDP       string `json:"sdp"`
}

// ICECandidate is one trickled candidate. The three optional fields are pointers
// because the browser distinguishes absent from zero, and a wrongly-defaulted
// sdpMLineIndex of 0 is silently accepted and then simply never connects.
type ICECandidate struct {
	SessionID        string  `json:"sessionId"`
	Candidate        string  `json:"candidate"`
	SDPMid           *string `json:"sdpMid,omitempty"`
	SDPMLineIndex    *uint16 `json:"sdpMLineIndex,omitempty"`
	UsernameFragment *string `json:"usernameFragment,omitempty"`
}

// SessionReady tells the browser a track is coming, so it can build its
// RTCPeerConnection before the offer arrives rather than racing it.
type SessionReady struct {
	SessionID string      `json:"sessionId"`
	Mode      SessionMode `json:"mode"`
}

// StopSession ends capture. The backend sends it on viewer disconnect, unconditionally.
type StopSession struct {
	SessionID string `json:"sessionId"`
}

// PresenceUpdate tells a dashboard that one device changed state. It is scoped by
// RBAC at send time, so an Admin is never told about another Admin's devices.
type PresenceUpdate struct {
	DeviceID  string `json:"deviceId"`
	Status    string `json:"status"` // online | offline | in_session
	SessionID string `json:"sessionId,omitempty"`
}

// SessionErrorMsg explains to the operator why a stream stopped, or never started.
type SessionErrorMsg struct {
	SessionID string `json:"sessionId"`
	Message   string `json:"message"`
}

// Encode marshals a payload into an Envelope frame ready to write.
func Encode(t MsgType, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Envelope{Type: t, Data: raw})
}

// EncodeRaw builds a frame from an already-marshalled payload. This is what lets the
// relay forward a message verbatim without round-tripping it through a struct, which
// keeps the backend honest about not interpreting SDP.
func EncodeRaw(t MsgType, raw json.RawMessage) ([]byte, error) {
	return json.Marshal(Envelope{Type: t, Data: raw})
}

// DecodeEnvelope parses a frame far enough to route it.
func DecodeEnvelope(data []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Envelope{}, err
	}
	if env.Type == "" {
		return Envelope{}, errors.New("protocol: envelope has no type")
	}
	return env, nil
}

// DecodeData unmarshals an envelope payload, treating an empty payload as a no-op so
// a data-less message such as ping is not an error.
func DecodeData(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}
