import { useEffect, useState } from 'react';

export type AgentPlatform = 'windows' | 'android';

/**
 * Where nginx serves the agent binaries from. Matches the filenames in
 * `deploy/downloads/` — see the `location /downloads/` block in
 * `deploy/nginx/conf.d/drs.conf`.
 */
const AGENT_FILES: Record<AgentPlatform, string> = {
  windows: '/downloads/drs-agent.exe',
  android: '/downloads/drs-agent.apk',
};

export interface AgentDownload {
  url: string;
  /** `null` while the probe is in flight, then whether the binary is actually there. */
  available: boolean | null;
}

/**
 * Whether this deployment has an agent binary staged for download.
 *
 * The binaries are uploaded by an operator rather than built into the image (the Windows
 * agent links libvpx through CGO), so a fresh deployment legitimately has none. A
 * download button that 404s is worse than no button at all — the recipient has no way to
 * tell whether they did something wrong — so the file is probed with HEAD and the UI says
 * which of the two situations it is in.
 *
 * The `location /downloads/` block returns a real 404 for a missing file. Without it the
 * SPA's try_files fallback would answer with index.html and a 200, and this probe would
 * report every binary as present.
 */
export function useAgentDownload(platform: AgentPlatform): AgentDownload {
  const url = AGENT_FILES[platform];
  const [available, setAvailable] = useState<boolean | null>(null);

  useEffect(() => {
    let cancelled = false;
    setAvailable(null);

    fetch(url, { method: 'HEAD' })
      .then((res) => {
        if (!cancelled) setAvailable(res.ok);
      })
      .catch(() => {
        if (!cancelled) setAvailable(false);
      });

    return () => {
      cancelled = true;
    };
  }, [url]);

  return { url, available };
}
