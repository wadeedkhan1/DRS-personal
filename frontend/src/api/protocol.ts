// The wire contract, mirroring backend/pkg/protocol/protocol.go.
//
// Every frame is an envelope: {"type": ..., "data": {...}}. Field names are camelCase
// to match the Go structs exactly, which is why they differ from the snake_case REST
// payloads elsewhere in this app.

export const PROTOCOL_VERSION = 1;

export type MsgType =
  | 'welcome'
  | 'error'
  | 'ping'
  | 'start_session'
  | 'stop_session'
  | 'session_ready'
  | 'offer'
  | 'answer'
  | 'ice_candidate'
  | 'session_error'
  | 'session_capabilities'
  | 'presence_update'
  | 'terminal_command'
  | 'terminal_result';

export interface Envelope<T = unknown> {
  type: MsgType;
  data?: T;
}

export interface SDPPayload {
  sessionId: string;
  sdpType: 'offer' | 'answer';
  sdp: string;
}

/**
 * A trickled ICE candidate. The three optional fields are nullable rather than
 * defaulted because the browser distinguishes absent from zero, and an sdpMLineIndex
 * wrongly sent as 0 is accepted and then simply never connects.
 */
export interface ICECandidatePayload {
  sessionId: string;
  candidate: string;
  sdpMid?: string | null;
  sdpMLineIndex?: number | null;
  usernameFragment?: string | null;
}

export interface SessionReadyPayload {
  sessionId: string;
  mode: 'webrtc';
}

export interface SessionErrorPayload {
  sessionId?: string;
  message: string;
}

/**
 * Sent once when the session socket opens, telling the viewer what the device consented
 * to at enrollment. allowScreen gates the live video; allowTerminal gates the terminal.
 */
export interface SessionCapabilitiesPayload {
  sessionId: string;
  allowScreen: boolean;
  allowTerminal: boolean;
}

export interface PresenceUpdatePayload {
  deviceId: string;
  status: 'online' | 'offline' | 'in_session';
  sessionId?: string;
}

export interface ICEServerConfig {
  urls: string[];
  username?: string;
  credential?: string;
}

/**
 * One command an operator sends the device to run (Phase 1 terminal). commandId is
 * generated here and echoed back in the result, so a result can be matched to its command
 * over the shared session socket where results may arrive out of order. shell picks the
 * interpreter: 'powershell' (default) or 'cmd'.
 */
export interface TerminalCommandPayload {
  sessionId: string;
  commandId: string;
  command: string;
  shell?: 'powershell' | 'cmd';
}

/**
 * The outcome of one command. stdout and stderr are separate; exitCode is the process
 * exit status; error is set only when the command could not be run at all (spawn failure
 * or timeout), as distinct from a command that ran and exited non-zero.
 */
export interface TerminalResultPayload {
  sessionId: string;
  commandId: string;
  stdout: string;
  stderr: string;
  exitCode: number;
  error?: string;
}

/** Builds a frame for sending. */
export function encode<T>(type: MsgType, data: T): string {
  return JSON.stringify({ type, data });
}

/** Parses a received frame, returning null rather than throwing on garbage. */
export function decode(raw: string): Envelope | null {
  try {
    const env = JSON.parse(raw);
    if (!env || typeof env.type !== 'string') return null;
    return env as Envelope;
  } catch {
    return null;
  }
}

/**
 * Builds a WebSocket URL on the same origin as the page.
 *
 * Same-origin matters: the portal is served by the same nginx that proxies the
 * backend, so this works in development behind the Vite proxy and in production
 * without any environment-specific configuration.
 */
export function wsURL(path: string, params?: Record<string, string>): string {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const query = params ? '?' + new URLSearchParams(params).toString() : '';
  return `${protocol}//${window.location.host}${path}${query}`;
}

/**
 * The subprotocol values used to authenticate a browser socket.
 *
 * A WebSocket cannot carry an Authorization header, so the JWT rides as the second
 * subprotocol value. The server negotiates back only 'bearer', so the token never
 * appears in a response header — and unlike a query string it stays out of access
 * logs and browser history.
 */
export function bearerSubprotocols(token: string): string[] {
  return ['bearer', token];
}
