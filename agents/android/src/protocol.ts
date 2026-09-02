/**
 * Wire contract, ported from backend/pkg/protocol/protocol.go.
 *
 * Every frame on the socket is an Envelope: {"type": "...", "data": {...}}, sent as a
 * WebSocket TEXT frame. Field names are camelCase (the browser is a first-class peer).
 *
 * Session shape (this client is the WebRTC OFFERER):
 *   1. Agent dials /ws/agent and sends `hello`; gets `welcome` (ok) or `error{fatal}` (stop).
 *   2. Operator opens a session -> backend pushes `start_session` (with the ICE servers).
 *   3. Agent sends `session_ready`, then `offer`. Browser replies `answer`. ICE trickles both ways.
 *   4. Backend relays offer/answer/ice_candidate verbatim; it never parses SDP.
 */

export const PROTOCOL_VERSION = 1;

export const MsgType = {
  // Agent -> backend.
  Hello: 'hello',
  Heartbeat: 'heartbeat',
  SessionError: 'session_error',

  // Backend -> agent.
  Welcome: 'welcome',
  Error: 'error',
  Ping: 'ping',
  StartSession: 'start_session',
  StopSession: 'stop_session',

  // WebRTC signaling.
  SessionReady: 'session_ready',
  Offer: 'offer',
  Answer: 'answer',
  ICECandidate: 'ice_candidate',
} as const;

export type MsgTypeValue = (typeof MsgType)[keyof typeof MsgType];

export const MODE_WEBRTC = 'webrtc';

export interface Envelope {
  type: string;
  data?: unknown;
}

export interface Hello {
  protocolVersion: number;
  deviceId: string;
  agentSecret: string;
  agentVersion: string;
}

export interface Welcome {
  heartbeatIntervalSeconds: number;
  serverTime: string;
}

export interface SysInfo {
  hostname: string;
  os: string; // e.g. "Android 14"
  platform: string; // "android"
}

export interface Heartbeat {
  cpuPercent: number;
  ramPercent: number;
  sysInfo: SysInfo;
}

export interface ErrorMsg {
  message: string;
  fatal: boolean;
}

export interface ICEServer {
  urls: string[];
  username?: string;
  credential?: string;
}

export interface StartSession {
  sessionId: string;
  mode?: string;
  fps: number;
  maxWidth: number;
  iceServers?: ICEServer[];
  operator?: string;
  iceTransportPolicy?: 'all' | 'relay';
}

export interface SDP {
  sessionId: string;
  sdpType: 'offer' | 'answer';
  sdp: string;
}

/**
 * One trickled ICE candidate. The three optional fields are optional on purpose: the
 * browser distinguishes absent from zero, and a wrongly-defaulted sdpMLineIndex of 0 is
 * silently accepted and then simply never connects. OMIT them when absent — never send 0.
 */
export interface ICECandidate {
  sessionId: string;
  candidate: string;
  sdpMid?: string | null;
  sdpMLineIndex?: number | null;
  usernameFragment?: string | null;
}

export interface SessionReady {
  sessionId: string;
  mode: string;
}

export interface StopSession {
  sessionId: string;
}

export interface SessionErrorMsg {
  sessionId: string;
  message: string;
}

/** Marshal a payload into an Envelope frame string ready to write. */
export function encode(type: MsgTypeValue, data: unknown): string {
  return JSON.stringify({type, data});
}

/** Parse a frame far enough to route it. Throws on malformed / typeless frames. */
export function decodeEnvelope(raw: string): Envelope {
  const env = JSON.parse(raw) as Envelope;
  if (!env || typeof env.type !== 'string' || env.type === '') {
    throw new Error('protocol: envelope has no type');
  }
  return env;
}
