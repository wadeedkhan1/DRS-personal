/**
 * The agent runtime: the one thing that owns the connection.
 *
 * This exists because of where the connection used to live. It was created by a
 * `useEffect` in `App.tsx`, so its cleanup ran whenever that component unmounted — and
 * swiping the app out of recents destroys the activity, which makes React Native unmount
 * the root component. The agent therefore disconnected itself the moment the user
 * dismissed it, and the foreground service holding the process up was stopped by the same
 * cleanup. Dismissing an app is not the same as switching it off.
 *
 * So the connection lives here instead, at module scope: started once, observed by the UI
 * rather than owned by it. Mounting and unmounting `App` now has no effect on it at all.
 * The only two things that stop it are the notification's Disconnect action and the
 * in-app button, both of which land in `stopRuntime`.
 */
import {
  AppState,
  Linking,
  NativeEventEmitter,
  NativeModules,
} from 'react-native';

import {Connection, ConnStatus} from './net/connection';
import {Identity, loadIdentity, saveIdentity} from './config/storage';
import {enroll} from './net/enroll';
import {defaultAcquireStream} from './webrtc/session';
import {log} from './log';
import {
  cancelCaptureApproval,
  getAgentEnabled,
  getDeviceTelemetry,
  isAppForeground,
  postCaptureApproval,
  setAgentEnabled,
  setAgentState,
  startConnectionService,
  stopConnectionService,
} from './telemetry';

/** How long the phone's owner has to approve a backgrounded capture request. */
const APPROVAL_TIMEOUT_MS = 60_000;

const STATUS_LABEL: Record<ConnStatus, string> = {
  connecting: 'Connecting…',
  online: 'Online — available for monitoring',
  in_session: 'Live — sharing screen',
  reconnecting: 'Reconnecting…',
  fatal: 'Disconnected',
  stopped: 'Disconnected',
};

export interface RuntimeState {
  /** Whether the user wants the agent connected, independent of whether it is. */
  enabled: boolean;
  status: ConnStatus;
  /** The operator's name during a session, or a server error message. */
  message?: string;
  statLine: string | null;
  identity: Identity | null;
  /** True until the persisted identity and enabled flag have been read. */
  loading: boolean;
}

type Listener = (state: RuntimeState) => void;

let state: RuntimeState = {
  enabled: false,
  status: 'stopped',
  statLine: null,
  identity: null,
  loading: true,
};

const listeners = new Set<Listener>();
let conn: Connection | null = null;
let booted = false;

/**
 * Serialises start/stop so they cannot interleave.
 *
 * Both do several awaits before touching `conn`, and they arrive from places that know
 * nothing about each other — the boot path, the in-app button, the notification action.
 * Interleaved, a stop that ran while a start was awaiting the foreground service would
 * find `conn` still null, stop nothing, and leave a live socket behind a UI that says
 * Disconnected — with no service holding the process up.
 */
let opChain: Promise<unknown> = Promise.resolve();

function serialise<T>(op: () => Promise<T>): Promise<T> {
  const next = opChain.then(op, op);
  opChain = next.catch(() => undefined);
  return next;
}

function publish(patch: Partial<RuntimeState>): void {
  state = {...state, ...patch};
  listeners.forEach(l => l(state));
}

export function getRuntimeState(): RuntimeState {
  return state;
}

/**
 * Observe the runtime. Unsubscribing does NOT stop it — that is the whole point of this
 * module, and the difference from the arrangement it replaced.
 */
export function subscribeRuntime(l: Listener): () => void {
  listeners.add(l);
  l(state);
  return () => {
    listeners.delete(l);
  };
}

/**
 * Wait until the app is actually in the foreground, or give up.
 *
 * Waiting on `AppState` rather than on a "the user tapped Approve" event is deliberate:
 * a tap only *starts* an activity, and `startActivity` returns long before that activity
 * is resumed. Proceeding on the tap would call `getDisplayMedia` while still effectively
 * backgrounded — the exact silent failure this whole path exists to avoid.
 *
 * The subscription is local, so two overlapping requests cannot cancel each other's wait.
 */
function waitForForeground(timeoutMs: number): Promise<void> {
  if (AppState.currentState === 'active') {
    return Promise.resolve();
  }
  return new Promise<void>((resolve, reject) => {
    const sub = AppState.addEventListener('change', next => {
      if (next === 'active') {
        clearTimeout(timer);
        sub.remove();
        resolve();
      }
    });
    const timer = setTimeout(() => {
      sub.remove();
      reject(
        new Error('Screen sharing was not approved on the device in time.'),
      );
    }, timeoutMs);
  });
}

/**
 * Acquire the screen, asking the owner to open the app first when it is not on screen.
 *
 * Android will not let a backgrounded process start the MediaProjection consent activity.
 * Calling `getDisplayMedia` anyway produces no dialog and no frames, which reaches the
 * operator as an unexplained black screen. Asking explicitly turns that into either
 * consent or an honest error.
 *
 * Note the notification does not grant anything — it only opens the app. Consent remains
 * Android's own MediaProjection dialog, which is why this waits to be foregrounded and
 * then calls `getDisplayMedia` normally.
 */
async function gatedAcquire(operator?: string) {
  if (await isAppForeground()) {
    return defaultAcquireStream();
  }

  log(
    'capture requested while backgrounded — asking the owner to open the app',
  );
  await postCaptureApproval(operator);

  try {
    await waitForForeground(APPROVAL_TIMEOUT_MS);
  } finally {
    await cancelCaptureApproval();
  }

  log('app foregrounded — acquiring capture');
  return defaultAcquireStream();
}

/**
 * Connect, if there is an identity to connect with. Idempotent: calling it while already
 * running is a no-op, so the UI can call it freely without tracking whether it needs to.
 */
export function startRuntime(): Promise<void> {
  return serialise(doStart);
}

async function doStart(): Promise<void> {
  const identity = state.identity ?? (await loadIdentity());
  if (!identity) {
    publish({identity: null, enabled: false, status: 'stopped'});
    return;
  }

  await setAgentEnabled(true);
  publish({identity, enabled: true});

  if (conn) {
    return;
  }

  await startConnectionService();
  setAgentState(STATUS_LABEL.connecting).catch(() => {});

  conn = new Connection(identity, {
    acquire: () => gatedAcquire(state.message),
    onStatus: (status, message) => {
      publish({
        status,
        message,
        ...(status !== 'in_session' ? {statLine: null} : {}),
      });
      // The notification is the only status the owner sees once the app is dismissed,
      // so it carries who is watching during a session.
      setAgentState(
        STATUS_LABEL[status],
        status === 'in_session' && message ? `Viewed by ${message}` : undefined,
      ).catch(() => {});
    },
    onStat: line => publish({statLine: line}),
  });
  conn.start();
}

/**
 * Disconnect for good.
 *
 * Closes the socket before stopping the service so the server sees the agent go rather
 * than waiting out three missed heartbeats, and clears the persisted flag so a later
 * process restart does not helpfully reconnect what the user just switched off.
 */
export function stopRuntime(): Promise<void> {
  return serialise(doStop);
}

async function doStop(): Promise<void> {
  await setAgentEnabled(false);
  conn?.stop();
  conn = null;
  await cancelCaptureApproval();
  await stopConnectionService();
  publish({
    enabled: false,
    status: 'stopped',
    message: undefined,
    statLine: null,
  });
}

/** Forget the enrolled identity too. The UI's Reset flow clears storage separately. */
export async function resetRuntimeIdentity(): Promise<void> {
  await stopRuntime();
  publish({identity: null});
}

/** Called after a successful enrollment, to connect with the identity just saved. */
export async function adoptIdentity(identity: Identity): Promise<void> {
  publish({identity});
  await startRuntime();
}

/**
 * Enroll from a `drs://enroll?server=…&token=…` link.
 *
 * This is the phone's equivalent of the config trailer appended to the Windows agent:
 * an installed APK cannot carry its own configuration, because the system renames it to
 * base.apk and an app cannot read its own installer. So the invite page hands the
 * details over as a link instead, and the recipient types nothing.
 *
 * An already-enrolled device ignores the link. Re-enrolling would rotate its secret and
 * silently move it to whichever team the new link points at — a link forwarded into a
 * group chat should not be able to reassign phones that are already managed.
 */
async function handleEnrollLink(url: string): Promise<void> {
  let parsed: {server: string; token: string} | null = null;
  try {
    // React Native has no URL query parser that is reliable across engines, so pull the
    // two parameters out directly rather than depending on one.
    const query = url.slice(url.indexOf('?') + 1);
    const get = (key: string) => {
      const m = new RegExp(`(?:^|&)${key}=([^&]*)`).exec(query);
      return m ? decodeURIComponent(m[1].replace(/\+/g, ' ')) : '';
    };
    const server = get('server').trim();
    const token = get('token').trim();
    if (server && token) {
      parsed = {server, token};
    }
  } catch {
    parsed = null;
  }

  if (!parsed) {
    log('ignoring an enroll link with no server or token');
    return;
  }

  if (state.identity) {
    log('ignoring an enroll link: this device is already enrolled');
    return;
  }

  log(`enrolling from a link at ${parsed.server}`);
  try {
    const telemetry = await getDeviceTelemetry();
    const identity = await enroll(
      parsed.server,
      parsed.token,
      telemetry.deviceModel || 'Android Phone',
    );
    await saveIdentity(identity);
    await adoptIdentity(identity);
    log('enrolled from link');
  } catch (e: any) {
    // Surfaced through the runtime state so the enrollment screen can show it, rather
    // than an alert from a module with no view of what is on screen.
    const message = e?.message ?? String(e);
    log(`enrollment from link failed: ${message}`);
    publish({status: 'fatal', message});
  }
}

/**
 * Start the runtime with the JS bundle.
 *
 * Called from `index.js` at module scope rather than from a component, so it also runs
 * when the bundle is loaded headlessly — the service recreating the React context after
 * the activity was destroyed, or a restart after the process was reclaimed. It only
 * reconnects if the agent was connected when it was last shut down.
 */
export function bootRuntime(): void {
  if (booted) {
    return;
  }
  booted = true;

  // Notification actions arrive here: they land in the app process but outside React,
  // so AgentCommandReceiver forwards them as events.
  try {
    const mod: any = NativeModules.DRSScreenCapture;
    if (mod) {
      new NativeEventEmitter(mod).addListener(
        'DRSAgentCommand',
        (payload: {action?: string}) => {
          if (payload?.action === 'stop') {
            log('disconnect requested from the notification');
            stopRuntime().catch(() => {});
          }
        },
      );
    }
  } catch (e) {
    log(`could not subscribe to native commands: ${String(e)}`);
  }

  // Invite links. Both directions are needed: getInitialURL for a link that launched a
  // cold app, and the listener for one that arrived while it was already running
  // (singleTask delivers that as a new intent rather than a new activity).
  Linking.addEventListener('url', ({url}) => {
    handleEnrollLink(url).catch(e => log(`enroll link failed: ${String(e)}`));
  });

  (async () => {
    const [identity, enabled] = await Promise.all([
      loadIdentity(),
      getAgentEnabled(),
    ]);
    publish({identity, enabled, loading: false});
    if (identity && enabled) {
      log('resuming previous connection');
      await startRuntime();
      return;
    }

    // Only consulted when there is no identity to resume, so a link that launched an
    // already-enrolled app cannot re-enroll it.
    if (!identity) {
      const initial = await Linking.getInitialURL();
      if (initial) {
        await handleEnrollLink(initial);
      }
    }
  })().catch(e => log(`runtime boot failed: ${String(e)}`));
}
