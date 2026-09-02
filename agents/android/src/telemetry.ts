/**
 * Device telemetry for the heartbeat, from the DRSScreenCapture native module.
 *
 * Android does not expose per-process CPU/RAM the way the Windows agent samples them, so
 * those are reported as 0; the fields exist to satisfy the heartbeat shape. Battery, OS
 * and model come from the native module and are surfaced in the dashboard's device card.
 */
import {NativeModules} from 'react-native';
import {SysInfo} from './protocol';

const {DRSScreenCapture} = NativeModules as {
  DRSScreenCapture?: {
    getDeviceTelemetry(): Promise<{
      batteryLevel: number;
      osVersion: string;
      deviceModel: string;
    }>;
    startConnectionService(): Promise<boolean>;
    stopConnectionService(): Promise<boolean>;
  };
};

export interface Telemetry {
  batteryLevel: number;
  osVersion: string;
  deviceModel: string;
}

export async function getDeviceTelemetry(): Promise<Telemetry> {
  if (!DRSScreenCapture) {
    return {batteryLevel: 100, osVersion: 'Android', deviceModel: 'Android Device'};
  }
  try {
    const t = await DRSScreenCapture.getDeviceTelemetry();
    return {
      batteryLevel: Math.round(t.batteryLevel ?? 100),
      osVersion: t.osVersion || 'Android',
      deviceModel: t.deviceModel || 'Android Device',
    };
  } catch {
    return {batteryLevel: 100, osVersion: 'Android', deviceModel: 'Android Device'};
  }
}

export async function buildSysInfo(): Promise<SysInfo> {
  const t = await getDeviceTelemetry();
  return {hostname: t.deviceModel, os: t.osVersion, platform: 'android'};
}

/** Keep-alive foreground service controls (hold the socket up while backgrounded). */
export async function startConnectionService(): Promise<void> {
  try {
    await DRSScreenCapture?.startConnectionService();
  } catch {
    // best effort
  }
}

export async function stopConnectionService(): Promise<void> {
  try {
    await DRSScreenCapture?.stopConnectionService();
  } catch {
    // best effort
  }
}
