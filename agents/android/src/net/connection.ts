/**
 * The outbound WebSocket control channel, ported from
 * agents/windows/internal/conn/conn.go.
 *
 * The device dials out and keeps the socket open, so commands flow down it with no inbound
 * port forwarding. It reconnects with backoff, so a dropped network, a server restart or a
 * phone waking from sleep all recover without anyone intervening. A revoked secret
 * (error{fatal:true}) stops the loop for good rather than hammering the server.
 */
import {
  decodeEnvelope,
  encode,
  ErrorMsg,
  Heartbeat,
  ICECandidate,
  MsgType,
  PROTOCOL_VERSION,
  SDP,
  StartSession,
  StopSession,
  SysInfo,
  Welcome,
} from '../protocol';
import {NativeEventEmitter, NativeModules} from 'react-native';
import {Identity} from '../config/storage';
import {buildSysInfo} from '../telemetry';
import {AcquireStream, VP8Session} from '../webrtc/session';
import {log} from '../log';

const AGENT_VERSION = '1.0.0';
const MIN_BACKOFF_MS = 1000;
const MAX_BACKOFF_MS = 30000;
const HELLO_ACK_TIMEOUT_MS = 10000;

export type ConnStatus =
  | 'connecting'
  | 'online'
  | 'in_session'
  | 'reconnecting'
  | 'fatal'
  | 'stopped';

export interface ConnectionCallbacks {
  onStatus: (status: ConnStatus, message?: string) => void;
  /** Acquire the screen stream (drives the consent UI). Passed through to the session. */
  acquire: AcquireStream;
  /** Per-second capture/encoder stats line for on-screen diagnostics (optional). */
  onStat?: (line: string) => void;
}

export class Connection {
  private ws: WebSocket | null = null;
  private welcomed = false;
  private stopped = false;
  private backoff = MIN_BACKOFF_MS;
  private helloTimer: ReturnType<typeof setTimeout> | null = null;
  private beatTimer: ReturnType<typeof setInterval> | null = null;
  private tickSub: {remove: () => void} | null = null;
  private nativeBeat = false;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private sysInfo: SysInfo | null = null;
  private session: VP8Session | null = null;

  constructor(
    private readonly id: Identity,
    private readonly cb: ConnectionCallbacks,
  ) {}

  start(): void {
    this.stopped = false;
    this.connect();
  }

  /** Stop for good: no reconnect, tear down any session and socket. */
  stop(): void {
    this.stopped = true;
    this.clearTimers();
    this.teardownSession();
    this.closeSocket();
    this.cb.onStatus('stopped');
  }

  private connect(): void {
    if (this.stopped) {
      return;
    }
    this.welcomed = false;
    this.cb.onStatus('connecting');
    log(`connecting to ${this.id.wsUrl}`);

    let ws: WebSocket;
    try {
      ws = new WebSocket(this.id.wsUrl);
    } catch (e) {
      log(`dial failed: ${String(e)}`);
      this.scheduleReconnect();
      return;
    }
    this.ws = ws;

    ws.onopen = () => {
      this.sendRaw(
        encode(MsgType.Hello, {
          protocolVersion: PROTOCOL_VERSION,
          deviceId: this.id.deviceId,
          agentSecret: this.id.agentSecret,
          agentVersion: AGENT_VERSION,
        }),
      );
      // Must get a welcome within the deadline, or treat it as a dead connection.
      this.helloTimer = setTimeout(() => {
        if (!this.welcomed) {
          this.closeSocket();
        }
      }, HELLO_ACK_TIMEOUT_MS);
    };

    ws.onmessage = ev => this.onMessage(String(ev.data));
    ws.onerror = () => {
      // onclose fires after; reconnect is handled there.
    };
    ws.onclose = () => {
      log('socket closed');
      this.clearHelloTimer();
      this.stopBeat();
      this.teardownSession();
      this.ws = null;
      if (!this.stopped) {
        this.scheduleReconnect();
      }
    };
  }

  private async onMessage(raw: string): Promise<void> {
    let type: string;
    let data: any;
    try {
      const env = decodeEnvelope(raw);
      type = env.type;
      data = env.data ?? {};
    } catch {
      return;
    }

    if (!this.welcomed) {
      // First frame decides: welcome (proceed) or error (maybe fatal).
      if (type === MsgType.Welcome) {
        await this.onWelcome(data as Welcome);
        return;
      }
      if (type === MsgType.Error) {
        this.onError(data as ErrorMsg);
        return;
      }
      // Anything else before welcome is a bad connection; drop and retry.
      this.closeSocket();
      return;
    }

    switch (type) {
      case MsgType.StartSession:
        this.onStartSession(data as StartSession);
        break;
      case MsgType.StopSession:
        this.onStopSession(data as StopSession);
        break;
      case MsgType.Answer:
        this.session?.handleAnswer((data as SDP).sdp);
        break;
      case MsgType.ICECandidate:
        this.session?.handleICECandidate(data as ICECandidate);
        break;
      case MsgType.Ping:
        // Liveness only; the heartbeat loop is the answer.
        break;
      case MsgType.Error:
        this.onError(data as ErrorMsg);
        break;
      default:
        break;
    }
  }

  private async onWelcome(wel: Welcome): Promise<void> {
    this.welcomed = true;
    this.clearHelloTimer();
    this.backoff = MIN_BACKOFF_MS; // a good connection resets the backoff
    if (wel.heartbeatIntervalSeconds > 0) {
      this.id.heartbeatIntervalSeconds = wel.heartbeatIntervalSeconds;
    }
    if (!this.sysInfo) {
      this.sysInfo = await buildSysInfo();
    }
    log(`welcome received; online, heartbeat every ${this.id.heartbeatIntervalSeconds}s`);
    this.cb.onStatus('online');
    this.beat();
    this.startBeat();
  }

  private onError(em: ErrorMsg): void {
    const msg = em.message || 'server rejected the connection';
    log(`server error${em.fatal ? ' (fatal)' : ''}: ${msg}`);
    if (em.fatal) {
      // Revoked/deleted device or protocol mismatch: stop retrying for good.
      this.stopped = true;
      this.clearTimers();
      this.teardownSession();
      this.closeSocket();
      this.cb.onStatus('fatal', msg);
      return;
    }
    console.warn('agent: server error:', msg);
  }

  private onStartSession(cmd: StartSession): void {
    if (!cmd.sessionId) {
      return;
    }
    // One session at a time; a new start replaces any running one.
    this.teardownSession();
    log(`start_session ${cmd.sessionId} from ${cmd.operator ?? 'operator'} (fps=${cmd.fps} maxW=${cmd.maxWidth})`);
    const session = new VP8Session(cmd, f => this.sendRaw(f), this.cb.acquire, this.cb.onStat);
    this.session = session;
    this.cb.onStatus('in_session', cmd.operator);
    session.start().catch(err => {
      const message = err?.message ?? String(err);
      log(`capture/start failed: ${message}`);
      this.sendRaw(
        encode(MsgType.SessionError, {sessionId: cmd.sessionId, message}),
      );
      if (this.session === session) {
        session.close();
        this.session = null;
        if (this.welcomed) {
          this.cb.onStatus('online');
        }
      }
    });
  }

  private onStopSession(cmd: StopSession): void {
    log(`stop_session ${cmd.sessionId}`);
    this.teardownSession();
    if (this.welcomed) {
      this.cb.onStatus('online');
    }
  }

  private beat(): void {
    const hb: Heartbeat = {
      cpuPercent: 0,
      ramPercent: 0,
      sysInfo: this.sysInfo ?? {hostname: 'Android Device', os: 'Android', platform: 'android'},
    };
    this.sendRaw(encode(MsgType.Heartbeat, hb));
  }

  private startBeat(): void {
    this.stopBeat();
    // Beat at half the server interval for margin against a missed tick. The server
    // drops the agent after 3x the interval with no frame.
    const intervalSec = Math.max(3, Math.floor(this.id.heartbeatIntervalSeconds / 2));
    const mod: any = NativeModules.DRSScreenCapture;
    if (mod?.startHeartbeat) {
      // Native ticks keep firing when the app is backgrounded / the screen is off,
      // where JS setInterval is suspended. The keep-alive service holds a wake lock.
      try {
        const emitter = new NativeEventEmitter(mod);
        this.tickSub = emitter.addListener('DRSHeartbeatTick', () => this.beat());
        mod.startHeartbeat(intervalSec * 1000);
        this.nativeBeat = true;
        log(`heartbeat: native ticks every ${intervalSec}s`);
        return;
      } catch (e) {
        log(`native heartbeat unavailable, using JS timer: ${String(e)}`);
      }
    }
    this.beatTimer = setInterval(() => this.beat(), intervalSec * 1000);
  }

  private scheduleReconnect(): void {
    if (this.stopped || this.reconnectTimer) {
      return;
    }
    this.cb.onStatus('reconnecting');
    log(`reconnecting in ${this.backoff}ms`);
    const delay = this.backoff;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, delay);
    this.backoff = Math.min(this.backoff * 2, MAX_BACKOFF_MS);
  }

  private sendRaw(frame: string): void {
    try {
      if (this.ws && this.ws.readyState === 1 /* OPEN */) {
        this.ws.send(frame);
      }
    } catch (e) {
      console.warn('agent: send failed', e);
    }
  }

  private teardownSession(): void {
    if (this.session) {
      this.session.close();
      this.session = null;
    }
  }

  private closeSocket(): void {
    if (this.ws) {
      try {
        this.ws.close();
      } catch {
        // ignore
      }
      this.ws = null;
    }
  }

  private clearHelloTimer(): void {
    if (this.helloTimer) {
      clearTimeout(this.helloTimer);
      this.helloTimer = null;
    }
  }

  private stopBeat(): void {
    if (this.beatTimer) {
      clearInterval(this.beatTimer);
      this.beatTimer = null;
    }
    if (this.tickSub) {
      this.tickSub.remove();
      this.tickSub = null;
    }
    if (this.nativeBeat) {
      try {
        (NativeModules.DRSScreenCapture as any)?.stopHeartbeat?.();
      } catch {
        // ignore
      }
      this.nativeBeat = false;
    }
  }

  private clearTimers(): void {
    this.clearHelloTimer();
    this.stopBeat();
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }
}
