/**
 * Tiny in-app logger with a ring buffer, so the agent's activity can be shown on the
 * phone screen (and copied out) without needing adb or a dev console. Everything also
 * mirrors to console.log, so `adb logcat -s ReactNativeJS` still works.
 */
type Listener = (lines: string[]) => void;

const MAX_LINES = 300;
let lines: string[] = [];
const listeners = new Set<Listener>();

function stamp(): string {
  // HH:MM:SS from the ISO string; local wall-clock is not needed for debugging order.
  return new Date().toISOString().slice(11, 19);
}

export function log(msg: string): void {
  const line = `${stamp()}  ${msg}`;
  lines.push(line);
  if (lines.length > MAX_LINES) {
    lines = lines.slice(-MAX_LINES);
  }
  // eslint-disable-next-line no-console
  console.log('[DRS]', msg);
  listeners.forEach(l => l(lines));
}

export function subscribeLogs(l: Listener): () => void {
  listeners.add(l);
  l(lines);
  return () => {
    listeners.delete(l);
  };
}

export function clearLogs(): void {
  lines = [];
  listeners.forEach(l => l(lines));
}

export function getLogText(): string {
  return lines.join('\n');
}
