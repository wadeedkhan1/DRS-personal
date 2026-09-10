import { useCallback, useEffect, useRef, useState } from 'react';
import { SessionConnection, SessionQuality, SessionState, SessionStats } from '../api/session';
import { useAuth } from '../context/AuthContext';

const emptyStats: SessionStats = {
  fps: 0, kbps: 0, width: 0, height: 0, rttMs: null, candidateType: null,
};

/**
 * Per-device handoff gate: the close of the last session on a device, if one is still
 * settling.
 *
 * A device has exactly one viewer slot, and the server frees it when it sees the socket
 * close — which happens a round trip after the browser asks. Anything reopening a session
 * on the same device immediately (the monitoring wall enlarging a tile: the grid session
 * ends and a full-quality one begins) would otherwise race its own predecessor and be
 * refused as "already being viewed" by itself.
 *
 * Keyed by device rather than held in a ref because the two sessions are usually two
 * different component instances, so there is no single hook for a ref to live in.
 */
const closeGates = new Map<string, Promise<void>>();

export interface DeviceSession {
  /** The live media, or null until the first track arrives. Attach it to a <video>. */
  stream: MediaStream | null;
  state: SessionState;
  stats: SessionStats;
  error: string | null;
  caps: { allowScreen: boolean; allowTerminal: boolean };
  /** Tear the session down and open a fresh one. */
  retry: () => void;
}

/**
 * One monitoring session, without any chrome.
 *
 * This is `ScreenViewer`'s session handling with the UI removed, so a page can hold
 * several at once. The same rule still applies to every one of them: the hook opening its
 * socket *is* what starts the session on the device, and the cleanup closing it is what
 * ends it — so each mounted hook exclusively claims its device for as long as it lives
 * (SRS FR-5.3, enforced server-side).
 *
 * Pass `enabled: false` for a device that should not be watched — an offline one, or a
 * tile past the wall's concurrency cap. That is different from not rendering the hook at
 * all, which React's rules of hooks would not allow inside a list of tiles.
 *
 * `quality` is read on each (re)connect, so raising it is how the wall enlarges a tile:
 * changing it tears the cheap session down and opens an expensive one in its place.
 * Pass a stable object — an inline literal would reconnect on every render.
 */
export function useDeviceSession(
  deviceId: string,
  options: {
    enabled?: boolean;
    quality?: SessionQuality;
    /** Seeds the capability state so the UI is right before the server confirms. */
    initialCaps?: { allowScreen: boolean; allowTerminal: boolean };
  } = {},
): DeviceSession {
  const { enabled = true, quality, initialCaps } = options;
  const { token } = useAuth();

  const [stream, setStream] = useState<MediaStream | null>(null);
  const [state, setState] = useState<SessionState>('connecting');
  const [stats, setStats] = useState<SessionStats>(emptyStats);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [caps, setCaps] = useState({
    allowScreen: initialCaps?.allowScreen ?? true,
    allowTerminal: initialCaps?.allowTerminal ?? false,
  });

  const sessionRef = useRef<SessionConnection | null>(null);

  const retry = useCallback(() => setAttempt((n) => n + 1), []);

  // Depending on the object identity of `quality` would reconnect whenever a parent
  // re-rendered with a fresh literal. The two numbers are what actually matter.
  const fps = quality?.fps;
  const maxWidth = quality?.maxWidth;

  useEffect(() => {
    if (!token || !enabled) {
      setState('closed');
      return;
    }

    setState('connecting');
    setStats(emptyStats);
    setError(null);
    setStream(null);

    const session = new SessionConnection(
      deviceId,
      token,
      {
        onTrack: setStream,
        onStats: setStats,
        onStateChange: setState,
        onError: setError,
        onCapabilities: (c) =>
          setCaps({ allowScreen: c.allowScreen, allowTerminal: c.allowTerminal }),
      },
      { fps, maxWidth },
    );
    sessionRef.current = session;

    // Wait out any still-closing session on this device before claiming it. Resolved
    // already in the common case, so this costs a microtask.
    const gate = closeGates.get(deviceId) ?? Promise.resolve();
    let abandoned = false;
    void gate.then(() => {
      if (!abandoned) void session.start();
    });

    return () => {
      abandoned = true;
      const closing = session.close();
      closeGates.set(deviceId, closing);
      // Stop the map growing a stale entry per device once the close has landed.
      void closing.then(() => {
        if (closeGates.get(deviceId) === closing) closeGates.delete(deviceId);
      });
      sessionRef.current = null;
    };
  }, [deviceId, token, enabled, fps, maxWidth, attempt]);

  return { stream, state, stats, error, caps, retry };
}
