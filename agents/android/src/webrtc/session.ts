/**
 * WebRTC offerer, ported from agents/windows/internal/screen/webrtc.go.
 *
 * This client owns the media, so it offers: send `session_ready` (so the browser can build
 * its RTCPeerConnection before the offer races it), then `offer`; apply the browser's
 * `answer`; trickle ICE both ways. react-native-webrtc negotiates VP8 in SDP and encodes
 * the screen track natively — there is no hand-rolled VP8 here.
 */
import {Platform} from 'react-native';
import {
  MediaStream,
  RTCIceCandidate,
  RTCPeerConnection,
  RTCSessionDescription,
  mediaDevices,
} from 'react-native-webrtc';

import {
  encode,
  ICECandidate,
  MsgType,
  MODE_WEBRTC,
  SDP,
  StartSession,
} from '../protocol';

import {log} from '../log';

type Send = (frame: string) => void;

/** Classify an ICE candidate string as host / srflx / prflx / relay for logs. */
function candType(candidate: string): string {
  const m = /\btyp (\w+)/.exec(candidate);
  return m ? m[1] : '?';
}

/** Acquire the screen MediaStream. Injected so the UI can drive the consent flow. */
export type AcquireStream = () => Promise<MediaStream>;

export const defaultAcquireStream: AcquireStream = () => {
  // getDisplayMedia drives Android's MediaProjection consent dialog and starts
  // react-native-webrtc's own capture foreground service.
  //
  // On Android 14+ (API 34) the consent dialog defaults to "single app" capture, which
  // produces ZERO frames once our app is backgrounded (cap=0f 0x0). createConfigForDefaultDisplay
  // forces full-screen capture of the default display, which is what we want and which
  // actually delivers frames. The config only exists on API 34+, so gate on it.
  const fullScreen = Platform.OS === 'android' && Number(Platform.Version) >= 34;
  log(`acquiring screen capture (fullScreenConfig=${fullScreen})`);
  const constraints = fullScreen ? {android: {createConfigForDefaultDisplay: true}} : {};
  return mediaDevices.getDisplayMedia(constraints) as unknown as Promise<MediaStream>;
};

export class VP8Session {
  private pc: RTCPeerConnection | null = null;
  private stream: MediaStream | null = null;
  private remoteSet = false;
  private pendingCandidates: RTCIceCandidate[] = [];
  private closed = false;
  private localCandidates = 0;
  private remoteCandidates = 0;
  private statsTimer: ReturnType<typeof setInterval> | null = null;

  constructor(
    private readonly cmd: StartSession,
    private readonly send: Send,
    private readonly acquire: AcquireStream = defaultAcquireStream,
    private readonly onStat?: (line: string) => void,
  ) {}

  get sessionId(): string {
    return this.cmd.sessionId;
  }

  /** Acquire capture, build the peer connection, and offer. Rejects on capture failure. */
  async start(): Promise<void> {
    // Grab the stream first: it proves capture works (and consent was granted) before a
    // peer connection is built.
    this.stream = await this.acquire();
    if (this.closed) {
      this.stopStream();
      return;
    }

    const config: any = {
      iceServers: (this.cmd.iceServers ?? []).map(s => ({
        urls: s.urls,
        ...(s.username ? {username: s.username, credential: s.credential} : {}),
      })),
    };
    // Both peers must agree on this. "relay" forces media through TURN.
    if (this.cmd.iceTransportPolicy === 'relay') {
      config.iceTransportPolicy = 'relay';
    }

    log(
      `starting ${this.sessionId} — iceServers=${config.iceServers.length} ` +
        `policy=${config.iceTransportPolicy ?? 'all'} tracks=${this.stream.getTracks().length}`,
    );

    const pc = new RTCPeerConnection(config);
    this.pc = pc;

    this.stream.getTracks().forEach(track => {
      log(
        `capture track kind=${track.kind} enabled=${(track as any).enabled} ` +
          `readyState=${(track as any).readyState}`,
      );
      pc.addTrack(track, this.stream as MediaStream);
    });

    // Trickle local candidates. OMIT optional fields when absent — a defaulted
    // sdpMLineIndex of 0 is silently accepted and then never connects.
    (pc as any).addEventListener('icecandidate', (event: any) => {
      const c = event?.candidate;
      if (!c || !c.candidate) {
        log(`local ICE gathering complete (sent ${this.localCandidates})`);
        return; // end of candidates
      }
      const payload: ICECandidate = {
        sessionId: this.sessionId,
        candidate: c.candidate,
      };
      if (c.sdpMid !== null && c.sdpMid !== undefined) {
        payload.sdpMid = c.sdpMid;
      }
      if (c.sdpMLineIndex !== null && c.sdpMLineIndex !== undefined) {
        payload.sdpMLineIndex = c.sdpMLineIndex;
      }
      this.localCandidates++;
      log(`-> local candidate #${this.localCandidates} [${candType(c.candidate)}]`);
      this.send(encode(MsgType.ICECandidate, payload));
    });

    (pc as any).addEventListener('icegatheringstatechange', () => {
      log(`ice gathering state: ${(pc as any).iceGatheringState}`);
    });

    (pc as any).addEventListener('iceconnectionstatechange', () => {
      const st = (pc as any).iceConnectionState;
      log(`ice connection state: ${st}`);
      if (st === 'connected' || st === 'completed') {
        this.startSenderStats();
      }
      if (st === 'failed') {
        this.reportError(new Error('ICE failed — no connectable candidate pair'));
        this.close();
      }
    });

    (pc as any).addEventListener('connectionstatechange', () => {
      const st = (pc as any).connectionState;
      log(`peer connection state: ${st}`);
      if (st === 'failed' || st === 'closed') {
        this.close();
      }
    });

    // Announced before the offer so the browser is ready for a track rather than racing it.
    this.send(
      encode(MsgType.SessionReady, {sessionId: this.sessionId, mode: MODE_WEBRTC}),
    );

    const offer = await pc.createOffer({});
    if (this.closed) {
      return;
    }
    await pc.setLocalDescription(offer);
    const sdp: SDP = {
      sessionId: this.sessionId,
      sdpType: 'offer',
      sdp: (pc.localDescription as any)?.sdp ?? (offer as any).sdp,
    };
    log(`-> offer sent (sdp ${sdp.sdp?.length ?? 0} bytes)`);
    this.send(encode(MsgType.Offer, sdp));
  }

  /** Apply the browser's answer and flush candidates that arrived first. */
  async handleAnswer(sdp: string): Promise<void> {
    if (!this.pc || this.closed) {
      return;
    }
    try {
      await this.pc.setRemoteDescription(
        new RTCSessionDescription({type: 'answer', sdp}),
      );
    } catch (e) {
      log(`!! set answer FAILED: ${String(e)}`);
      this.reportError(new Error(`set answer failed: ${String(e)}`));
      return;
    }
    this.remoteSet = true;
    const pending = this.pendingCandidates;
    this.pendingCandidates = [];
    log(`<- answer applied; flushing ${pending.length} buffered candidate(s)`);
    for (const cand of pending) {
      try {
        await this.pc.addIceCandidate(cand);
      } catch (e) {
        log(`buffered candidate rejected: ${String(e)}`);
      }
    }
  }

  /**
   * Apply a candidate, buffering it if the answer has not arrived. Trickle ICE means
   * candidates routinely overtake the answer, and addIceCandidate before
   * setRemoteDescription is an error — dropping them silently is a classic cause of a
   * session that negotiates cleanly and then never produces video.
   */
  async handleICECandidate(c: ICECandidate): Promise<void> {
    if (!this.pc || this.closed || !c.candidate) {
      return;
    }
    const cand = new RTCIceCandidate({
      candidate: c.candidate,
      sdpMid: c.sdpMid ?? undefined,
      sdpMLineIndex: c.sdpMLineIndex ?? undefined,
    });
    if (!this.remoteSet) {
      this.pendingCandidates.push(cand);
      log(`<- remote candidate buffered (${this.pendingCandidates.length} waiting for answer)`);
      return;
    }
    this.remoteCandidates++;
    log(`<- remote candidate #${this.remoteCandidates} applied [${candType(c.candidate)}]`);
    try {
      await this.pc.addIceCandidate(cand);
    } catch (e) {
      log(`remote candidate rejected: ${String(e)}`);
    }
  }

  /** Tear the session down exactly once: stop capture and close the peer connection. */
  close(): void {
    if (this.closed) {
      return;
    }
    this.closed = true;
    if (this.statsTimer) {
      clearInterval(this.statsTimer);
      this.statsTimer = null;
    }
    this.stopStream();
    try {
      this.pc?.close();
    } catch {
      // ignore
    }
    this.pc = null;
  }

  private stopStream(): void {
    this.stream?.getTracks().forEach(t => {
      try {
        t.stop();
      } catch {
        // ignore
      }
    });
    this.stream = null;
  }

  /**
   * Poll outbound-rtp stats so we can see, from the sender side, whether the screen
   * encoder is actually producing and sending frames. framesEncoded stuck at 0 means the
   * capture surface is not feeding the encoder; bytesSent climbing means media is going
   * out and any "no video" is then a receiver/decoder problem.
   */
  private startSenderStats(): void {
    if (this.statsTimer || this.closed) {
      return;
    }
    let last = {frames: 0, bytes: 0};
    this.statsTimer = setInterval(async () => {
      if (!this.pc || this.closed) {
        return;
      }
      let report: any;
      try {
        report = await this.pc.getStats();
      } catch {
        return;
      }
      let srcFrames = 0;
      let srcW = 0;
      let srcH = 0;
      let framesEncoded = 0;
      let bytesSent = 0;
      let packetsSent = 0;
      let quality = '-';
      report.forEach((s: any) => {
        if (s.type === 'media-source' && (s.kind === 'video' || s.mediaType === 'video')) {
          // Frames coming OUT of the screen capturer, before the encoder.
          srcFrames = s.frames ?? srcFrames;
          srcW = s.width ?? srcW;
          srcH = s.height ?? srcH;
        }
        if (s.type === 'outbound-rtp' && (s.kind === 'video' || s.mediaType === 'video')) {
          framesEncoded = s.framesEncoded ?? 0;
          bytesSent = s.bytesSent ?? 0;
          packetsSent = s.packetsSent ?? 0;
          quality = s.qualityLimitationReason ?? '-';
        }
      });
      const line =
        `cap=${srcFrames}f ${srcW}x${srcH} | enc=${framesEncoded} ` +
        `sent=${Math.round(bytesSent / 1024)}KB pkts=${packetsSent} ql=${quality}`;
      log(`outbound-rtp: ${line} (+${framesEncoded - last.frames} enc, +${bytesSent - last.bytes}B)`);
      this.onStat?.(line);
      last = {frames: framesEncoded, bytes: bytesSent};
    }, 2000);
  }

  /** Report a session failure to the operator (relayed to the browser as session_error). */
  private reportError(err: Error): void {
    log(`session error: ${err.message}`);
    this.send(
      encode(MsgType.SessionError, {sessionId: this.sessionId, message: err.message}),
    );
  }
}
