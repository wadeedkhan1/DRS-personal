import {
  Envelope,
  PresenceUpdatePayload,
  bearerSubprotocols,
  decode,
  wsURL,
} from './protocol';

export type PresenceListener = (deviceId: string, status: string) => void;

/**
 * The dashboard's live device-status feed (/ws/presence).
 *
 * Separate from a session socket on purpose: a session socket is per-device and only
 * open while someone is watching, so without this the device list would have to poll to
 * show live online/offline state (SRS FR-2.4).
 *
 * The server scopes what it sends by RBAC, so an Admin is never told about devices
 * belonging to another Admin.
 */
export class PresenceSocket {
  private ws: WebSocket | null = null;
  private listeners: PresenceListener[] = [];
  private reconnectTimer: number | null = null;
  private backoffMs = 1000;
  private closed = false;

  constructor(
    private token: string,
    private onConnectionChange?: (connected: boolean) => void,
  ) {}

  connect() {
    if (this.closed) return;

    const ws = new WebSocket(wsURL('/ws/presence'), bearerSubprotocols(this.token));
    this.ws = ws;

    ws.onopen = () => {
      // Reset only after the connection actually succeeds. Resetting on attempt would
      // turn the backoff into a tight loop against a server that accepts and
      // immediately drops.
      this.backoffMs = 1000;
      this.onConnectionChange?.(true);
    };

    ws.onmessage = (event) => {
      const env = decode(String(event.data));
      if (env?.type !== 'presence_update') return;
      const data = (env as Envelope<PresenceUpdatePayload>).data;
      if (!data?.deviceId || !data.status) return;
      this.listeners.forEach((fn) => fn(data.deviceId, data.status));
    };

    ws.onclose = () => {
      this.ws = null;
      this.onConnectionChange?.(false);
      this.scheduleReconnect();
    };
  }

  /**
   * Reconnects with exponential backoff, capped at 30s.
   *
   * The previous implementation retried on a fixed 3s timer, so a backend restart meant
   * every open dashboard hammered it every three seconds while it was trying to come up.
   */
  private scheduleReconnect() {
    if (this.closed || this.reconnectTimer !== null) return;

    const delay = this.backoffMs;
    this.backoffMs = Math.min(this.backoffMs * 2, 30000);
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, delay);
  }

  onPresence(listener: PresenceListener): () => void {
    this.listeners.push(listener);
    return () => {
      this.listeners = this.listeners.filter((l) => l !== listener);
    };
  }

  disconnect() {
    this.closed = true;
    if (this.reconnectTimer !== null) {
      window.clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.ws?.close();
    this.ws = null;
  }
}
