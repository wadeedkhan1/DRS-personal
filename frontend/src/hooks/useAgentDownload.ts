import { useEffect, useState } from 'react';
import { ApiClient } from '../api/client';

export type AgentPlatform = 'windows' | 'android';

export interface AgentDownload {
  /**
   * Where to download the agent for this platform, already carrying the invite's
   * configuration. Only meaningful with a token — see `useAgentDownload`.
   */
  url: string;
  /** `null` while the check is in flight, then whether a binary is actually published. */
  available: boolean | null;
}

/** The download URL for an invite. Configuration is applied server-side per request. */
export function agentDownloadURL(platform: AgentPlatform, token: string): string {
  return `/api/enroll/agent?platform=${platform}&token=${encodeURIComponent(token)}`;
}

/**
 * The deep link that configures an already-installed Android agent.
 *
 * An APK cannot carry a config the way the .exe can — the system renames an installed
 * package to base.apk and an app cannot read its own installer — so the phone is handed
 * its server and token as a link instead.
 */
export function androidSetupLink(origin: string, token: string): string {
  return `drs://enroll?server=${encodeURIComponent(origin)}&token=${encodeURIComponent(token)}`;
}

/**
 * Whether this deployment has an agent binary staged for download.
 *
 * The binaries are uploaded by an operator rather than built into the image (the Windows
 * agent links libvpx through CGO), so a fresh deployment legitimately has none. A
 * download button that 404s is worse than no button at all — the recipient has no way to
 * tell whether they did something wrong — so the UI says which of the two situations it
 * is in.
 *
 * This asks the backend rather than probing nginx's `/downloads/` with HEAD, which was
 * wrong in development: `/downloads` is not in the Vite proxy, so the probe hit the SPA
 * fallback, got a 200, and reported every binary as present whether or not one existed.
 * The backend is the right authority anyway — it is the process that has to open the file.
 */
export function useAgentDownload(platform: AgentPlatform, token?: string): AgentDownload {
  const [available, setAvailable] = useState<boolean | null>(null);

  useEffect(() => {
    let cancelled = false;
    setAvailable(null);

    ApiClient.agentAvailability()
      .then((res) => {
        if (!cancelled) setAvailable(Boolean(res[platform]));
      })
      .catch(() => {
        if (!cancelled) setAvailable(false);
      });

    return () => {
      cancelled = true;
    };
  }, [platform]);

  return { url: token ? agentDownloadURL(platform, token) : '', available };
}
