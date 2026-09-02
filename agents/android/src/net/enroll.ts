/**
 * One-time enrollment, ported from agents/windows/internal/enroll/enroll.go.
 *
 * NOTE the casing split: this REST body is snake_case, unlike everything on the
 * WebSocket, which is camelCase. The response is camelCase.
 */
import {deriveWsUrl, Identity} from '../config/storage';
import {getDeviceTelemetry} from '../telemetry';
import {log} from '../log';

interface EnrollResponse {
  deviceId: string;
  agentSecret: string;
  wsUrl: string;
  heartbeatIntervalSeconds: number;
}

/**
 * Redeem an enrollment token and return the permanent identity. The caller persists it.
 * The old stub POSTed correctly but threw the response away — this keeps it.
 */
export async function enroll(
  serverUrl: string,
  token: string,
  name: string,
): Promise<Identity> {
  const base = serverUrl.trim().replace(/\/+$/, '');
  const telemetry = await getDeviceTelemetry();
  log(`enrolling at ${base} as "${name}" (${telemetry.osVersion})`);

  const res = await fetch(`${base}/api/devices/enroll`, {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({
      enrollment_token: token.trim(),
      name: name.trim(),
      type: 'android',
      os_version: telemetry.osVersion,
    }),
  });

  if (!res.ok) {
    const text = await res.text().catch(() => '');
    throw new Error(
      res.status === 400 || res.status === 404
        ? 'Invalid or expired enrollment token.'
        : `Enrollment failed (${res.status}). ${text}`.trim(),
    );
  }

  const body = (await res.json()) as EnrollResponse;
  if (!body.deviceId || !body.agentSecret) {
    throw new Error('Enrollment response missing device identity.');
  }
  log(`enrolled: deviceId=${body.deviceId.slice(0, 8)}… wsUrl=${body.wsUrl}`);

  return {
    serverUrl: base,
    wsUrl: body.wsUrl || deriveWsUrl(base),
    deviceId: body.deviceId,
    agentSecret: body.agentSecret,
    heartbeatIntervalSeconds: body.heartbeatIntervalSeconds || 10,
  };
}
