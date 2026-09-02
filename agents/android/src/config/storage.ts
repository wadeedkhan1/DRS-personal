/**
 * On-device identity, ported from agents/windows/internal/config/config.go.
 *
 * The Windows agent stores this as a 0600 JSON file at %AppData%/drs/agent.json. Here it
 * lives in AsyncStorage. serverUrl + the enrollment token are the only things the user
 * types; everything else comes back from the enroll response and is the permanent identity.
 */
import AsyncStorage from '@react-native-async-storage/async-storage';

const KEY = 'drs.agent.identity';

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
