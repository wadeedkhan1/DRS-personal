import {
  Envelope,
  ICECandidatePayload,
  ICEServerConfig,
  SDPPayload,
  SessionCapabilitiesPayload,
  SessionErrorPayload,
  SessionReadyPayload,
  TerminalResultPayload,
  bearerSubprotocols,
  decode,
  encode,
  wsURL,
} from './protocol';
import { ApiClient } from './client';

export interface SessionStats {
  fps: number;
  kbps: number;
  width: number;
  height: number;
  rttMs: number | null;
  /** 'host' | 'srflx' | 'relay' — whether the media path is direct or relayed. */
  candidateType: string | null;
}

export interface SessionHandlers {
  onTrack?: (stream: MediaStream) => void;
  onStats?: (stats: SessionStats) => void;
  onError?: (message: string) => void;
  onStateChange?: (state: SessionState) => void;
  /** A terminal command finished on the device and returned its output. */
  onTerminalResult?: (result: TerminalResultPayload) => void;
  /** The device's consented capabilities, sent once when the session opens. */
  onCapabilities?: (caps: SessionCapabilitiesPayload) => void;
}

export type SessionState =
  | 'connecting'   // opening the signaling socket
  | 'negotiating'  // exchanging SDP and ICE
  | 'live'         // media flowing
  | 'failed'
  | 'closed';

/**
 * One monitoring session: the signaling socket plus the WebRTC peer connection.
 *
 * The browser is the ANSWERER. The agent produces the media, so it offers; this side
 * only ever answers and receives. It adds no tracks, creates no data channel, and
 * cannot initiate anything — which is what makes the trust flow one-way.
 *
 * Opening the socket *is* starting the session, and closing it is what ends it: the
 * backend sends the agent a stop_session on disconnect, unconditionally. So there is no
 * separate "end session" message to forget to send.
 */
export class SessionConnection {
  private ws: WebSocket | null = null;
  private pc: RTCPeerConnection | null = null;
  private iceServers: ICEServerConfig[] = [];
  private iceTransportPolicy: RTCIceTransportPolicy = 'all';
  private sessionId: string | null = null;
  private statsTimer: number | null = null;
  private closed = false;

  // Candidates routinely arrive before the offer they belong to. Applying one before
  // setRemoteDescription throws, and silently dropping them is the classic cause of a
  // session that negotiates cleanly and then shows nothing but black.
  private pendingCandidates: RTCIceCandidateInit[] = [];
  private remoteDescriptionSet = false;

  // getStats gives cumulative counters, so a rate needs the previous sample.
  private lastBytes = 0;
  private lastFrames = 0;
  private lastSampleAt = 0;

  constructor(
    private readonly deviceId: string,
    private readonly token: string,
    private readonly handlers: SessionHandlers,
  ) {}

  async start(): Promise<void> {
    this.handlers.onStateChange?.('connecting');

    // Fetched before the socket opens, so the peer connection can be built the instant
    // the agent says it is ready. Both peers get this same list — the agent receives an
    // identical copy in start_session — because negotiating against different candidate
    // universes fails in ways that are very hard to read from either end.
    try {
      const cfg = await ApiClient.getIceConfig();
      this.iceServers = cfg.iceServers;
      this.iceTransportPolicy = cfg.iceTransportPolicy;
    } catch {
      this.iceServers = [];
    }
    if (this.closed) return;

    const url = wsURL('/ws/session', { deviceId: this.deviceId });
    const ws = new WebSocket(url, bearerSubprotocols(this.token));
    this.ws = ws;

    ws.onmessage = (event) => this.onMessage(event);
    ws.onerror = () => {
      // A WebSocket error event carries no detail by design. The close code that
      // follows is where the actual reason is, so this only reports if nothing else has.
      if (!this.closed && !this.sessionId) {
        this.handlers.onError?.('Could not open the monitoring session.');
      }
    };
    ws.onclose = (event) => {
      if (this.closed) return;
      this.handlers.onStateChange?.('closed');
      if (event.code === 1008 /* policy violation */) {
        this.handlers.onError?.('This device is already being viewed by someone else.');
      } else if (!this.sessionId) {
        this.handlers.onError?.('The monitoring session was refused.');
      }
    };
  }

  private onMessage(event: MessageEvent) {
    const env = decode(String(event.data));
    if (!env) return;

    switch (env.type) {
      case 'session_ready':
        this.onSessionReady(env as Envelope<SessionReadyPayload>);
        break;
      case 'offer':
        void this.onOffer(env as Envelope<SDPPayload>);
        break;
      case 'ice_candidate':
        void this.onRemoteCandidate(env as Envelope<ICECandidatePayload>);
        break;
      case 'session_error': {
        const data = (env as Envelope<SessionErrorPayload>).data;
        this.handlers.onError?.(data?.message || 'The device reported a problem.');
        break;
      }
      case 'error': {
        const data = (env as Envelope<SessionErrorPayload>).data;
        this.handlers.onError?.(data?.message || 'The server reported a problem.');
        break;
      }
      case 'terminal_result': {
        const data = (env as Envelope<TerminalResultPayload>).data;
        if (data) this.handlers.onTerminalResult?.(data);
        break;
      }
      case 'session_capabilities': {
        const data = (env as Envelope<SessionCapabilitiesPayload>).data;
        if (data) this.handlers.onCapabilities?.(data);
        break;
      }
    }
  }

  private onSessionReady(env: Envelope<SessionReadyPayload>) {
    this.sessionId = env.data?.sessionId ?? null;
    this.handlers.onStateChange?.('negotiating');
    this.ensurePeer();
  }

  private ensurePeer(): RTCPeerConnection {
    if (this.pc) return this.pc;

    const pc = new RTCPeerConnection({
      iceServers: this.iceServers,
      // 'relay' when the deployment forces all media through TURN. It must match what
      // the agent was told, or the two peers gather candidates that cannot pair.
      iceTransportPolicy: this.iceTransportPolicy,
    });
    this.pc = pc;

    pc.ontrack = (ev) => {
      const stream = ev.streams[0] ?? new MediaStream([ev.track]);
      console.debug('[DRS] ontrack', {
        kind: ev.track.kind,
        readyState: ev.track.readyState,
        muted: ev.track.muted,
        streams: ev.streams.length,
      });
      this.handlers.onTrack?.(stream);
    };

    pc.onicecandidate = (ev) => {
      if (!ev.candidate || !this.sessionId) return; // null candidate = gathering done
      const c = ev.candidate;
      this.send('ice_candidate', {
        sessionId: this.sessionId,
        candidate: c.candidate,
        sdpMid: c.sdpMid,
        sdpMLineIndex: c.sdpMLineIndex,
        usernameFragment: c.usernameFragment ?? undefined,
      });
    };

    pc.onconnectionstatechange = () => {
      switch (pc.connectionState) {
        case 'connected':
          this.handlers.onStateChange?.('live');
          this.startStatsPolling();
          break;
        case 'failed':
          this.handlers.onStateChange?.('failed');
          // The specific failure worth naming: with no TURN relay configured, a
          // symmetric NAT or corporate firewall leaves no direct path and ICE simply
          // runs out of candidates.
          this.handlers.onError?.(
            'Could not establish a direct connection to this device. ' +
              'This usually means a firewall or NAT is blocking peer-to-peer traffic ' +
              'and a TURN relay is needed.',
          );
          break;
        case 'disconnected':
          this.handlers.onStateChange?.('negotiating');
          break;
      }
    };

    return pc;
  }

  private async onOffer(env: Envelope<SDPPayload>) {
    const data = env.data;
    if (!data?.sdp) return;
    if (data.sessionId) this.sessionId = data.sessionId;

    const pc = this.ensurePeer();
    try {
      await pc.setRemoteDescription({ type: 'offer', sdp: data.sdp });
      this.remoteDescriptionSet = true;

      // Now that a remote description exists, anything buffered can be applied.
      for (const candidate of this.pendingCandidates) {
        await pc.addIceCandidate(candidate).catch(() => {});
      }
      this.pendingCandidates = [];

      const answer = await pc.createAnswer();
      await pc.setLocalDescription(answer);
      this.send('answer', {
        sessionId: this.sessionId,
        sdpType: 'answer',
        sdp: answer.sdp,
      });
    } catch (err) {
      this.handlers.onError?.(`Could not negotiate the video stream: ${String(err)}`);
    }
  }

  private async onRemoteCandidate(env: Envelope<ICECandidatePayload>) {
    const data = env.data;
    if (!data?.candidate) return;

    const init: RTCIceCandidateInit = {
      candidate: data.candidate,
      sdpMid: data.sdpMid ?? null,
      sdpMLineIndex: data.sdpMLineIndex ?? null,
      usernameFragment: data.usernameFragment ?? undefined,
    };

    if (!this.remoteDescriptionSet) {
      this.pendingCandidates.push(init);
      return;
    }
    await this.pc?.addIceCandidate(init).catch(() => {});
  }

  /**
   * Polls real transport statistics.
   *
   * Everything reported here is measured. The previous viewer displayed a hardcoded
   * 25ms latency, a floor of 1 FPS while stalled, and an "RTT" computed by subtracting
   * the agent's clock from the browser's — two unsynchronised machines, so the number
   * was meaningless.
   */
  private startStatsPolling() {
    if (this.statsTimer !== null) return;

    this.statsTimer = window.setInterval(async () => {
      if (!this.pc) return;
      let report: RTCStatsReport;
      try {
        report = await this.pc.getStats();
      } catch {
        return;
      }

      const stats: SessionStats = {
        fps: 0, kbps: 0, width: 0, height: 0, rttMs: null, candidateType: null,
      };
      const now = performance.now();
      const elapsed = this.lastSampleAt ? (now - this.lastSampleAt) / 1000 : 0;
      let selectedLocalId: string | null = null;

      // Raw counters, logged so a stream that connects but shows nothing can be
      // diagnosed from the browser side. The distinction that matters:
      // packetsReceived climbing with framesDecoded stuck at 0 means the media arrives
      // and the decoder rejects it, which is a completely different bug from no media
      // arriving at all.
      report.forEach((s: any) => {
        if (s.type === 'inbound-rtp') {
          console.debug('[DRS] inbound-rtp', {
            kind: s.kind ?? s.mediaType,
            packetsReceived: s.packetsReceived,
            bytesReceived: s.bytesReceived,
            framesReceived: s.framesReceived,
            framesDecoded: s.framesDecoded,
            keyFramesDecoded: s.keyFramesDecoded,
            framesDropped: s.framesDropped,
            packetsLost: s.packetsLost,
            pliCount: s.pliCount,
            decoder: s.decoderImplementation,
            codec: s.codecId,
          });
        }
        if (s.type === 'candidate-pair' && s.state === 'succeeded') {
          console.debug('[DRS] candidate-pair', {
            nominated: s.nominated,
            bytesReceived: s.bytesReceived,
            currentRoundTripTime: s.currentRoundTripTime,
          });
        }
        if (s.type === 'codec') {
          console.debug('[DRS] codec', { mimeType: s.mimeType, payloadType: s.payloadType, clockRate: s.clockRate });
        }
      });

      report.forEach((s: any) => {
        if (s.type === 'inbound-rtp' && (s.kind === 'video' || s.mediaType === 'video')) {
          if (elapsed > 0) {
            stats.kbps = Math.max(0, ((s.bytesReceived - this.lastBytes) * 8) / elapsed / 1000);
            stats.fps = Math.max(0, (s.framesDecoded - this.lastFrames) / elapsed);
          }
          this.lastBytes = s.bytesReceived ?? 0;
          this.lastFrames = s.framesDecoded ?? 0;
          if (s.frameWidth) stats.width = s.frameWidth;
          if (s.frameHeight) stats.height = s.frameHeight;
        }
        if (s.type === 'candidate-pair' && s.state === 'succeeded' && (s.nominated || s.selected)) {
          if (typeof s.currentRoundTripTime === 'number') {
            stats.rttMs = Math.round(s.currentRoundTripTime * 1000);
          }
          // Remember which local candidate actually won, so the transport shown to the
          // operator is the one in use. Reading candidateType off whichever
          // local-candidate happened to come last reported an arbitrary one of the
          // several that were gathered -- which matters, because this is the signal
          // that tells you whether media is really going through the relay or not.
          selectedLocalId = s.localCandidateId ?? null;
        }
      });

      if (selectedLocalId) {
        report.forEach((s: any) => {
          if (s.type === 'local-candidate' && s.id === selectedLocalId && s.candidateType) {
            stats.candidateType = s.candidateType;
          }
        });
      }

      this.lastSampleAt = now;
      this.handlers.onStats?.({
        ...stats,
        fps: Math.round(stats.fps),
        kbps: Math.round(stats.kbps),
      });
    }, 1000);
  }

  private send(type: 'answer' | 'ice_candidate' | 'terminal_command', data: unknown) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(encode(type, data as any));
    }
  }

  /**
   * Sends a command to run on the device. commandId is generated by the caller so it can
   * match the result that comes back; shell selects the interpreter (default PowerShell).
   * Returns false if the socket is not open, so the caller can tell the operator rather
   * than silently dropping the command.
   */
  sendCommand(command: string, commandId: string, shell: 'powershell' | 'cmd' = 'powershell'): boolean {
    if (this.ws?.readyState !== WebSocket.OPEN) return false;
    this.send('terminal_command', {
      sessionId: this.sessionId,
      commandId,
      command,
      shell,
    });
    return true;
  }

  /**
   * Ends the session.
   *
   * Closing the socket is the whole teardown: the backend responds by telling the agent
   * to stop capturing, so the device is immediately available again. Nothing depends on
   * this method being reached — a crashed tab has the same effect.
   */
  close() {
    this.closed = true;
    if (this.statsTimer !== null) {
      window.clearInterval(this.statsTimer);
      this.statsTimer = null;
    }
    this.pc?.getReceivers().forEach((r) => r.track?.stop());
    this.pc?.close();
    this.pc = null;
    this.ws?.close();
    this.ws = null;
  }
}
