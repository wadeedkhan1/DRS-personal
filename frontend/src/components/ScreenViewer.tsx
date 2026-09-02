import React, { useState, useEffect, useRef, useCallback } from 'react';
import { Device } from '../types';
import { useAuth } from '../context/AuthContext';
import { SessionConnection, SessionState, SessionStats } from '../api/session';
import {
  Maximize2, Minimize2, Camera, PowerOff, Activity, ShieldAlert,
  Wifi, Clock, Loader2, AlertTriangle, RefreshCw, Link2, TerminalSquare, CornerDownLeft,
} from 'lucide-react';

interface ScreenViewerProps {
  device: Device;
  onClose: () => void;
}

/** One command the operator ran, plus its result once it comes back. */
interface TerminalEntry {
  commandId: string;
  command: string;
  shell: 'powershell' | 'cmd';
  status: 'running' | 'done';
  stdout?: string;
  stderr?: string;
  exitCode?: number;
  error?: string;
}

// crypto.randomUUID is available in every browser that supports WebRTC, but guard anyway
// so a locked-down context falls back rather than throwing when running a command.
function newCommandId(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID();
  return `cmd-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

const emptyStats: SessionStats = {
  fps: 0, kbps: 0, width: 0, height: 0, rttMs: null, candidateType: null,
};

/**
 * The live viewer.
 *
 * This component owns the session. Mounting it opens the socket, which is what starts
 * the session; unmounting closes it, which is what ends it. That single rule replaced
 * the previous arrangement where two components each sent their own session request
 * (so the second always failed as "already in a session") and nothing reliably sent a
 * stop, leaving devices permanently locked.
 */
export const ScreenViewer: React.FC<ScreenViewerProps> = ({ device, onClose }) => {
  const { token } = useAuth();
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [state, setState] = useState<SessionState>('connecting');
  const [stats, setStats] = useState<SessionStats>(emptyStats);
  // Whether the video element is actually rendering frames. This, not a computed
  // statistic, is what decides if the operator can see the screen — so it is what the
  // loading overlay keys off. Gating on a derived fps meant that if getStats reported
  // anything unexpected, working video sat hidden behind the overlay forever.
  const [playing, setPlaying] = useState(false);
  const [sessionSeconds, setSessionSeconds] = useState(0);
  const [screenshotTaken, setScreenshotTaken] = useState(false);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);

  // What the device consented to expose. Seeded from the device row so the UI is right
  // on first paint, then confirmed by the session_capabilities frame the backend sends
  // when the socket opens.
  const [caps, setCaps] = useState({
    allowScreen: device.allow_screen ?? true,
    allowTerminal: device.allow_terminal ?? false,
  });

  // Terminal (Phase 1 command runner). Rides the same session socket, so it is only
  // available while this viewer is mounted.
  const [showTerminal, setShowTerminal] = useState(false);
  const [termEntries, setTermEntries] = useState<TerminalEntry[]>([]);
  const [termInput, setTermInput] = useState('');
  const [termShell, setTermShell] = useState<'powershell' | 'cmd'>('powershell');

  const containerRef = useRef<HTMLDivElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  const sessionRef = useRef<SessionConnection | null>(null);
  const termScrollRef = useRef<HTMLDivElement>(null);
  const termInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!token) return;

    setState('connecting');
    setStats(emptyStats);
    setErrorMessage(null);
    setPlaying(false);
    // A new device (or a reconnect) starts a fresh terminal history; keeping the old
    // output would attribute one machine's results to another.
    setTermEntries([]);

    const session = new SessionConnection(device.id, token, {
      onTrack: (stream) => {
        // The element is always mounted, so the ref is ready the moment a track
        // arrives; there is no window where the stream has nowhere to go.
        const video = videoRef.current;
        if (!video) return;
        video.srcObject = stream;
        // autoPlay plus muted should be enough, but calling play() explicitly covers
        // the cases where the browser declines the implicit start, and surfaces the
        // reason instead of leaving a silently black element.
        void video.play().catch((err) => {
          console.warn('[ScreenViewer] autoplay was blocked:', err);
        });
      },
      onStats: setStats,
      onStateChange: setState,
      onError: setErrorMessage,
      onCapabilities: (c) => {
        setCaps({ allowScreen: c.allowScreen, allowTerminal: c.allowTerminal });
        // A terminal-only device has no video to wait for, so open the terminal straight
        // away rather than leaving the operator staring at a black panel.
        if (!c.allowScreen && c.allowTerminal) setShowTerminal(true);
      },
      onTerminalResult: (result) => {
        setTermEntries((prev) =>
          prev.map((e) =>
            e.commandId === result.commandId
              ? {
                  ...e,
                  status: 'done',
                  stdout: result.stdout,
                  stderr: result.stderr,
                  exitCode: result.exitCode,
                  error: result.error,
                }
              : e,
          ),
        );
      },
    });

    sessionRef.current = session;
    void session.start();

    return () => {
      session.close();
      sessionRef.current = null;
    };
  }, [device.id, token, attempt]);

  // Session duration.
  useEffect(() => {
    const timer = window.setInterval(() => setSessionSeconds((s) => s + 1), 1000);
    return () => window.clearInterval(timer);
  }, []);

  const isLive = playing;

  const toggleFullscreen = useCallback(() => {
    if (!containerRef.current) return;
    if (!document.fullscreenElement) {
      containerRef.current.requestFullscreen().catch(() => {});
      setIsFullscreen(true);
    } else {
      document.exitFullscreen().catch(() => {});
      setIsFullscreen(false);
    }
  }, []);

  // Grabs the current video frame. Drawn from the <video> element rather than kept as a
  // canvas copy of every frame, so nothing is buffered when nobody asks for a shot.
  const captureScreenshot = useCallback(() => {
    const video = videoRef.current;
    if (!video || !video.videoWidth) return;

    const canvas = document.createElement('canvas');
    canvas.width = video.videoWidth;
    canvas.height = video.videoHeight;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    ctx.drawImage(video, 0, 0);

    const link = document.createElement('a');
    link.href = canvas.toDataURL('image/png');
    link.download = `DRS-${device.name}-${new Date().toISOString().replace(/[:.]/g, '-')}.png`;
    link.click();

    setScreenshotTaken(true);
    window.setTimeout(() => setScreenshotTaken(false), 2000);
  }, [device.name]);

  const formatDuration = (secs: number) => {
    const m = Math.floor(secs / 60);
    const s = secs % 60;
    return `${m}:${s < 10 ? '0' : ''}${s}`;
  };

  const runCommand = useCallback(
    (e: React.FormEvent) => {
      e.preventDefault();
      const command = termInput.trim();
      const session = sessionRef.current;
      if (!command || !session) return;

      const commandId = newCommandId();
      const sent = session.sendCommand(command, commandId, termShell);
      if (!sent) {
        // The socket is not open (session dropped). Show it as a failed entry rather than
        // silently swallowing the command.
        setTermEntries((prev) => [
          ...prev,
          { commandId, command, shell: termShell, status: 'done', error: 'Not connected to the device.' },
        ]);
        return;
      }
      setTermEntries((prev) => [...prev, { commandId, command, shell: termShell, status: 'running' }]);
      setTermInput('');
    },
    [termInput, termShell],
  );

  // Keep the newest output in view as it streams in.
  useEffect(() => {
    const el = termScrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [termEntries, showTerminal]);

  // Put the cursor in the command box the moment the panel opens.
  useEffect(() => {
    if (showTerminal) termInputRef.current?.focus();
  }, [showTerminal]);

  // 'relay' means media is going through a TURN server rather than directly, which is
  // worth surfacing: it costs bandwidth and adds latency, and the operator should know
  // the difference rather than being told everything is "P2P".
  const pathLabel =
    stats.candidateType === 'relay' ? 'Relayed (TURN)'
      : stats.candidateType ? 'Direct (P2P)'
      : 'Connecting';

  return (
    <div
      ref={containerRef}
      className="flex flex-col h-full w-full bg-slate-950 rounded-2xl border border-slate-800 overflow-hidden shadow-2xl relative"
    >
      <div className="h-14 bg-slate-900/90 backdrop-blur border-b border-slate-800 px-5 flex items-center justify-between z-10">
        <div className="flex items-center gap-3">
          <div className={`w-2.5 h-2.5 rounded-full ${isLive ? 'bg-emerald-400' : 'bg-amber-400 animate-pulse'}`} />
          <div>
            <div className="flex items-center gap-2">
              <span className="font-bold text-sm text-slate-100">{device.name}</span>
              <span className="text-[10px] uppercase font-bold px-2 py-0.5 rounded bg-sky-500/10 text-sky-400 border border-sky-500/20">
                VP8 · {pathLabel}
              </span>
            </div>
            <div className="text-[11px] text-slate-400 flex items-center gap-3">
              <span>{device.type === 'windows' ? 'Windows desktop' : 'Android screen'}</span>
              <span>•</span>
              <span className="flex items-center gap-1">
                <Clock className="w-3 h-3 text-slate-500" /> {formatDuration(sessionSeconds)}
              </span>
            </div>
          </div>
        </div>

        {/* Every figure here comes from RTCPeerConnection.getStats(). */}
        <div className="hidden md:flex items-center gap-4 text-xs font-mono">
          <div className="flex items-center gap-1.5 text-emerald-400">
            <Activity className="w-3.5 h-3.5" />
            <span>{isLive ? `${stats.fps} fps` : '—'}</span>
          </div>
          <div className="flex items-center gap-1.5 text-sky-400">
            <Wifi className="w-3.5 h-3.5" />
            <span>{isLive ? `${stats.kbps} kbps` : '—'}</span>
          </div>
          <div className="flex items-center gap-1.5 text-slate-400">
            <Link2 className="w-3.5 h-3.5" />
            <span>{stats.rttMs !== null ? `${stats.rttMs} ms` : '—'}</span>
          </div>
          <div className="text-slate-400">
            {stats.width ? `${stats.width}×${stats.height}` : '—'}
          </div>
        </div>

        <div className="flex items-center gap-2">
          {caps.allowTerminal && (
            <button
              onClick={() => setShowTerminal((v) => !v)}
              className={`flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium transition-colors border ${
                showTerminal
                  ? 'bg-sky-500/15 text-sky-300 border-sky-500/30'
                  : 'bg-slate-800 hover:bg-slate-700 text-slate-200 border-transparent'
              }`}
              title="Open a command terminal on this device"
            >
              <TerminalSquare className="w-3.5 h-3.5 text-sky-400" />
              <span className="hidden sm:inline">Terminal</span>
            </button>
          )}

          <button
            onClick={captureScreenshot}
            disabled={!isLive}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-slate-800 hover:bg-slate-700 text-xs font-medium text-slate-200 transition-colors disabled:opacity-40"
            title="Save a still image"
          >
            <Camera className="w-3.5 h-3.5 text-sky-400" />
            <span className="hidden sm:inline">{screenshotTaken ? 'Saved' : 'Screenshot'}</span>
          </button>

          <button
            onClick={toggleFullscreen}
            className="p-2 rounded-lg bg-slate-800 hover:bg-slate-700 text-slate-300 transition-colors"
            title="Toggle fullscreen"
          >
            {isFullscreen ? <Minimize2 className="w-4 h-4" /> : <Maximize2 className="w-4 h-4" />}
          </button>

          <button
            onClick={onClose}
            className="flex items-center gap-1.5 px-3.5 py-1.5 rounded-lg bg-rose-500/10 hover:bg-rose-500/20 text-rose-400 border border-rose-500/30 text-xs font-semibold transition-all"
            title="End session"
          >
            <PowerOff className="w-3.5 h-3.5" />
            <span>Disconnect</span>
          </button>
        </div>
      </div>

      <div className="flex-1 flex flex-col min-h-0">
        <div className="flex-1 min-h-0 bg-black flex items-center justify-center relative overflow-hidden">
        {/*
          Kept mounted at all times, including behind the overlays, so ontrack always
          has somewhere to attach. muted is required for autoplay to be allowed.
        */}
        <video
          ref={videoRef}
          autoPlay
          muted
          playsInline
          onPlaying={() => setPlaying(true)}
          onEmptied={() => setPlaying(false)}
          className="max-w-full max-h-full object-contain"
        />

        {errorMessage && (
          <div className="absolute inset-0 flex flex-col items-center justify-center bg-slate-950/95 backdrop-blur-md z-30 p-6 text-center">
            <div className="w-12 h-12 rounded-2xl bg-amber-500/10 border border-amber-500/20 flex items-center justify-center text-amber-400 mb-4">
              <AlertTriangle className="w-6 h-6" />
            </div>
            <h3 className="text-base font-bold text-slate-100 mb-1">Unable to stream this device</h3>
            <p className="text-xs text-slate-400 max-w-md mb-6 leading-relaxed">{errorMessage}</p>
            <div className="flex items-center gap-3">
              <button
                onClick={() => setAttempt((n) => n + 1)}
                className="flex items-center gap-2 px-4 py-2 rounded-xl bg-sky-500 hover:bg-sky-400 text-white text-xs font-semibold shadow-lg shadow-sky-500/20 transition-all"
              >
                <RefreshCw className="w-3.5 h-3.5" />
                <span>Retry</span>
              </button>
              <button
                onClick={onClose}
                className="px-4 py-2 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-300 text-xs font-semibold transition-colors"
              >
                Back to devices
              </button>
            </div>
          </div>
        )}

        {!caps.allowScreen && !errorMessage && (
          <div className="absolute inset-0 flex flex-col items-center justify-center bg-slate-950/95 backdrop-blur-sm z-20 p-6 text-center">
            <div className="w-12 h-12 rounded-2xl bg-slate-800 border border-slate-700 flex items-center justify-center text-slate-400 mb-4">
              <ShieldAlert className="w-6 h-6" />
            </div>
            <h3 className="text-base font-bold text-slate-100 mb-1">Screen sharing is off for this device</h3>
            <p className="text-xs text-slate-400 max-w-md leading-relaxed">
              The person who set up this agent did not enable screen sharing.
              {caps.allowTerminal ? ' The terminal is still available below.' : ''}
            </p>
          </div>
        )}

        {caps.allowScreen && !isLive && !errorMessage && (
          <div className="absolute inset-0 flex flex-col items-center justify-center bg-slate-950/90 backdrop-blur-sm z-20">
            <Loader2 className="w-8 h-8 text-sky-400 animate-spin mb-3" />
            {/*
              Three distinct states rather than one spinner, because they fail for
              different reasons: no session, no peer connection, or a peer connection
              carrying no decodable video.
            */}
            <div className="text-sm font-semibold text-slate-200">
              {state === 'connecting'
                ? 'Opening session…'
                : state === 'live'
                  ? 'Waiting for the first frame…'
                  : 'Negotiating peer connection…'}
            </div>
            <div className="text-xs text-slate-500 mt-1">
              {state === 'connecting'
                ? 'Asking the device to start capturing'
                : state === 'live'
                  ? 'Connected to the device; the video stream has not started yet'
                  : 'Exchanging network candidates with the device'}
            </div>
          </div>
        )}

        <div className="absolute bottom-4 left-4 flex items-center gap-2 px-3 py-1.5 rounded-lg bg-slate-900/80 backdrop-blur border border-slate-800 text-[11px] text-slate-400">
          <ShieldAlert className="w-3.5 h-3.5 text-amber-400" />
          <span>The device shows a tray indicator while it is being viewed</span>
        </div>
        </div>

        {showTerminal && (
          <div className="h-72 flex flex-col border-t border-slate-800 bg-slate-950">
            <div className="flex items-center justify-between px-4 h-9 border-b border-slate-800 bg-slate-900/60 shrink-0">
              <div className="flex items-center gap-2 text-[11px] font-semibold text-slate-300">
                <TerminalSquare className="w-3.5 h-3.5 text-sky-400" />
                <span>Terminal — {device.name}</span>
                <span className="text-slate-600 font-normal hidden sm:inline">runs as the logged-in user</span>
              </div>
              <div className="flex items-center gap-1">
                {(['powershell', 'cmd'] as const).map((sh) => (
                  <button
                    key={sh}
                    onClick={() => setTermShell(sh)}
                    className={`px-2 py-0.5 rounded text-[10px] font-mono uppercase tracking-wide transition-colors ${
                      termShell === sh ? 'bg-sky-500/15 text-sky-300' : 'text-slate-500 hover:text-slate-300'
                    }`}
                  >
                    {sh}
                  </button>
                ))}
              </div>
            </div>

            <div
              ref={termScrollRef}
              className="flex-1 min-h-0 overflow-y-auto px-4 py-2 font-mono text-xs leading-relaxed text-slate-300"
            >
              {termEntries.length === 0 && (
                <div className="text-slate-600 select-none">
                  Type a command below and press Enter. Try{' '}
                  <span className="text-slate-400">whoami</span>,{' '}
                  <span className="text-slate-400">ipconfig</span>, or{' '}
                  <span className="text-slate-400">Get-Process</span>.
                </div>
              )}
              {termEntries.map((entry) => (
                <div key={entry.commandId} className="mb-2">
                  <div className="flex items-start gap-2 text-sky-400">
                    <span className="text-slate-600 shrink-0">{entry.shell === 'cmd' ? '>' : 'PS>'}</span>
                    <span className="whitespace-pre-wrap break-all">{entry.command}</span>
                  </div>
                  {entry.status === 'running' ? (
                    <div className="flex items-center gap-2 text-slate-500 pl-6">
                      <Loader2 className="w-3 h-3 animate-spin" /> running…
                    </div>
                  ) : (
                    <div className="pl-6">
                      {entry.stdout && (
                        <pre className="whitespace-pre-wrap break-all text-slate-300 font-mono">{entry.stdout}</pre>
                      )}
                      {entry.stderr && (
                        <pre className="whitespace-pre-wrap break-all text-amber-400 font-mono">{entry.stderr}</pre>
                      )}
                      {entry.error && (
                        <pre className="whitespace-pre-wrap break-all text-rose-400 font-mono">{entry.error}</pre>
                      )}
                      {entry.exitCode !== undefined && entry.exitCode !== 0 && !entry.error && (
                        <div className="text-[10px] text-rose-500/80">exit code {entry.exitCode}</div>
                      )}
                    </div>
                  )}
                </div>
              ))}
            </div>

            <form
              onSubmit={runCommand}
              className="flex items-center gap-2 px-3 h-11 border-t border-slate-800 bg-slate-900/60 shrink-0"
            >
              <span className="text-[11px] font-mono text-sky-500 pl-1 shrink-0">
                {termShell === 'cmd' ? 'cmd' : 'PS'}
              </span>
              <input
                ref={termInputRef}
                value={termInput}
                onChange={(e) => setTermInput(e.target.value)}
                placeholder="Enter a command…"
                spellCheck={false}
                autoComplete="off"
                className="flex-1 bg-transparent outline-none text-xs font-mono text-slate-100 placeholder:text-slate-600"
              />
              <button
                type="submit"
                disabled={!termInput.trim()}
                className="flex items-center gap-1 px-2.5 py-1 rounded-md bg-sky-500/15 text-sky-300 text-[11px] font-medium disabled:opacity-40 hover:bg-sky-500/25 transition-colors"
              >
                <CornerDownLeft className="w-3 h-3" /> Run
              </button>
            </form>
          </div>
        )}
      </div>
    </div>
  );
};
