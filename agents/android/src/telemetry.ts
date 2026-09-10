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
    setAgentState(status: string, detail: string | null): Promise<boolean>;
    getAgentEnabled(): Promise<boolean>;
    setAgentEnabled(enabled: boolean): Promise<boolean>;
    isAppForeground(): Promise<boolean>;
    postCaptureApprovalNotification(operator: string | null): Promise<boolean>;
    cancelCaptureApprovalNotification(): Promise<boolean>;
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

/** Drive the ongoing notification's text, so it reads like a VPN's status line. */
export async function setAgentState(status: string, detail?: string): Promise<void> {
  try {
    await DRSScreenCapture?.setAgentState(status, detail ?? null);
  } catch {
    // cosmetic only
  }
}

/**
 * The persisted "should be connected" flag. Native-owned, because a notification action
 * or a service restart has to read it when there may be no JS context at all.
 */
export async function getAgentEnabled(): Promise<boolean> {
  try {
    return (await DRSScreenCapture?.getAgentEnabled()) ?? false;
  } catch {
    return false;
  }
}

export async function setAgentEnabled(enabled: boolean): Promise<void> {
  try {
    await DRSScreenCapture?.setAgentEnabled(enabled);
  } catch {
    // best effort
  }
}

/**
 * Whether an activity is on screen. Android refuses to raise the MediaProjection consent
 * dialog from the background, so this decides whether capture can start directly or has
 * to be asked for through a notification first.
 */
export async function isAppForeground(): Promise<boolean> {
  try {
    return (await DRSScreenCapture?.isAppForeground()) ?? false;
  } catch {
    // Assume foreground: trying and failing is a clearer error than never trying.
    return true;
  }
}

export async function postCaptureApproval(operator?: string): Promise<void> {
  try {
    await DRSScreenCapture?.postCaptureApprovalNotification(operator ?? null);
  } catch {
    // best effort
  }
}

export async function cancelCaptureApproval(): Promise<void> {
  try {
    await DRSScreenCapture?.cancelCaptureApprovalNotification();
  } catch {
    // best effort
  }
}
