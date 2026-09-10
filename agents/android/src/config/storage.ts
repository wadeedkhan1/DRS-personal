/**
 * On-device identity, ported from agents/windows/internal/config/config.go.
 *
 * The Windows agent stores this as a 0600 JSON file at %AppData%/drs/agent.json. Here it
 * lives in AsyncStorage. serverUrl + the enrollment token are the only things the user
 * types; everything else comes back from the enroll response and is the permanent identity.
 */
import AsyncStorage from '@react-native-async-storage/async-storage';

const KEY = 'drs.agent.identity';

// Stored separately from the identity because it must SURVIVE re-enrollment: it says
// "this is the same physical device", which is exactly what a new identity does not.
const MACHINE_ID_KEY = 'drs.agent.machineId';

export interface Identity {
  serverUrl: string;
  wsUrl: string;
  deviceId: string;
  agentSecret: string;
  heartbeatIntervalSeconds: number;
}

export async function loadIdentity(): Promise<Identity | null> {
  try {
    const raw = await AsyncStorage.getItem(KEY);
    if (!raw) {
      return null;
    }
    const id = JSON.parse(raw) as Identity;
    if (!id.deviceId || !id.agentSecret) {
      return null;
    }
    if (!id.wsUrl) {
      id.wsUrl = deriveWsUrl(id.serverUrl);
    }
    return id;
  } catch {
    return null;
  }
}

export async function saveIdentity(id: Identity): Promise<void> {
  await AsyncStorage.setItem(KEY, JSON.stringify(id));
}

export async function clearIdentity(): Promise<void> {
  await AsyncStorage.removeItem(KEY);
}

/**
 * A stable identifier for this handset, created once and kept.
 *
 * Devices are de-duplicated server-side on this rather than on the device name, because
 * names are not unique: every "Samsung SM-G991B" enrolled from one invite link reports
 * the same one, and matching on it would have the second phone take over the first's
 * device row and rotate its secret — leaving the first agent reconnecting forever with a
 * credential the server had discarded.
 *
 * Random rather than an Android ID or IMEI: those need permissions or are unavailable on
 * modern releases, and a value generated here is enough to distinguish two installs. The
 * cost is that clearing app data makes the phone look like a new device, which already
 * loses the identity anyway.
 */
export async function getMachineId(): Promise<string> {
  try {
    const existing = await AsyncStorage.getItem(MACHINE_ID_KEY);
    if (existing) {
      return existing;
    }
  } catch {
    return '';
  }

  // Math.random is fine here: this identifies, it does not authenticate. Uniqueness
  // across a fleet is all that is asked of it, and 128 bits of it is ample.
  let id = '';
  for (let i = 0; i < 32; i++) {
    id += Math.floor(Math.random() * 16).toString(16);
  }
  try {
    await AsyncStorage.setItem(MACHINE_ID_KEY, id);
  } catch {
    return '';
  }
  return id;
}

/**
 * Derive the agent WebSocket URL from a base server URL, mirroring config.go:
 * https -> wss, http -> ws, then append /ws/agent. Used only when the enroll response
 * did not carry an explicit wsUrl.
 */
export function deriveWsUrl(serverUrl: string): string {
  let base = serverUrl.trim().replace(/\/+$/, '');
  if (base.startsWith('https://')) {
    base = 'wss://' + base.slice('https://'.length);
  } else if (base.startsWith('http://')) {
    base = 'ws://' + base.slice('http://'.length);
  }
  return base + '/ws/agent';
}
