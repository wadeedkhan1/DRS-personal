import React, { useEffect, useRef } from 'react';
import { Device } from '../types';
import { SessionQuality } from '../api/session';
import { useDeviceSession } from '../hooks/useDeviceSession';
import {
  AlertTriangle, Loader2, Monitor, PlayCircle, PowerOff, RefreshCw, ShieldAlert, Smartphone,
} from 'lucide-react';

interface MonitorTileProps {
  device: Device;
  /** false parks the tile: it renders a placeholder and opens no session. */
  live: boolean;
  quality?: SessionQuality;
  /** Rendered large in the focus pane rather than as a grid cell. */
  focused?: boolean;
  /** This device's grid cell while it is the one enlarged above. */
  enlargedElsewhere?: boolean;
  onFocus?: () => void;
  /** Offered on a parked tile so the operator can start it past the concurrency cap. */
  onStart?: () => void;
}

/**
 * One pane of the monitoring wall.
 *
 * Every live tile is a real session that exclusively claims its device, so a tile is
 * never opened speculatively: an offline device, or one parked behind the wall's
 * concurrency cap, renders a placeholder and holds no socket.
 */
export const MonitorTile: React.FC<MonitorTileProps> = ({
  device, live, quality, focused = false, enlargedElsewhere = false, onFocus, onStart,
}) => {
  const offline = device.status === 'offline';
  const enabled = live && !offline;

  const { stream, state, stats, error, caps, retry } = useDeviceSession(device.id, {
    enabled,
    quality,
    initialCaps: {
      allowScreen: device.allow_screen ?? true,
      allowTerminal: device.allow_terminal ?? false,
    },
  });

  const videoRef = useRef<HTMLVideoElement>(null);
  const PlatformIcon = device.type === 'android' ? Smartphone : Monitor;

  // The element is always mounted while the tile is enabled, so a track always has
  // somewhere to go the moment it arrives.
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    video.srcObject = stream;
    if (stream) {
      void video.play().catch((err) => {
        console.warn('[MonitorTile] autoplay was blocked:', err);
      });
    }
  }, [stream]);

  const frame = focused
    ? 'rounded-2xl bg-slate-900/60 border border-sky-500/40 shadow-lg shadow-sky-500/10'
    : 'rounded-2xl bg-slate-900/60 border border-slate-800 hover:border-slate-700/80 cursor-pointer';

  return (
    <div
      className={`group relative w-full h-full overflow-hidden transition-all ${frame}`}
      onClick={onFocus}
      title={focused ? undefined : `Enlarge ${device.name}`}
    >
      <div className="relative w-full h-full bg-black flex items-center justify-center">
        {enabled ? (
          <video
            ref={videoRef}
            autoPlay
            muted
            playsInline
            className="max-w-full max-h-full object-contain"
          />
        ) : null}

        {/* Parked or unreachable: say which, rather than showing an empty black box. */}
        {!enabled && (
          <div className="absolute inset-0 flex flex-col items-center justify-center gap-2 text-center px-3">
            <PlatformIcon
              className={`w-8 h-8 ${enlargedElsewhere ? 'text-sky-500/60' : 'text-slate-700'}`}
            />
            <span className="text-[11px] text-slate-500">
              {offline
                ? 'Device is offline'
                : enlargedElsewhere
                ? 'Enlarged above'
                : 'Not being watched'}
            </span>
            {!offline && !enlargedElsewhere && onStart && (
              <button
                onClick={(e) => { e.stopPropagation(); onStart(); }}
                className="mt-1 inline-flex items-center gap-1.5 px-3 py-1.5 rounded-xl bg-sky-500/10 hover:bg-sky-500/20 text-sky-400 border border-sky-500/30 text-[11px] font-semibold transition-all"
              >
                <PlayCircle className="w-3.5 h-3.5" />
                Start
              </button>
            )}
          </div>
        )}

        {/* The device shares neither screen nor terminal, or screen is off. */}
        {enabled && !caps.allowScreen && !error && (
          <div className="absolute inset-0 z-20 flex flex-col items-center justify-center gap-2 bg-slate-950/80 px-3 text-center">
            <ShieldAlert className="w-7 h-7 text-amber-400" />
            <span className="text-[11px] text-slate-300">Screen sharing is off</span>
          </div>
        )}

        {enabled && error && (
          <div className="absolute inset-0 z-30 flex flex-col items-center justify-center gap-2 bg-slate-950/85 px-4 text-center">
            <AlertTriangle className="w-7 h-7 text-rose-400" />
            <span className="text-[11px] text-slate-300 leading-relaxed line-clamp-3">{error}</span>
            <button
              onClick={(e) => { e.stopPropagation(); retry(); }}
              className="mt-1 inline-flex items-center gap-1.5 px-3 py-1.5 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-300 border border-slate-700/60 text-[11px] font-semibold transition-all"
            >
              <RefreshCw className="w-3.5 h-3.5" />
              Retry
            </button>
          </div>
        )}

        {enabled && caps.allowScreen && !error && !stream && (
          <div className="absolute inset-0 z-20 flex flex-col items-center justify-center gap-2 bg-slate-950/60">
            <Loader2 className="w-6 h-6 text-sky-400 animate-spin" />
            <span className="text-[10px] font-mono text-slate-400">
              {state === 'connecting' ? 'Opening…' : 'Negotiating…'}
            </span>
          </div>
        )}

        {/* Name strip. Always visible: on a wall of a dozen panes, a tile you cannot
            identify without hovering is not much use. */}
        <div className="absolute inset-x-0 bottom-0 z-10 flex items-center justify-between gap-2 px-2.5 py-1.5 bg-gradient-to-t from-slate-950/95 to-transparent">
          <div className="flex items-center gap-1.5 min-w-0">
            <PlatformIcon className="w-3 h-3 text-slate-400 shrink-0" />
            <span className="text-[11px] font-medium text-slate-200 truncate">{device.name}</span>
          </div>
          {enabled && stream && (
            <span className="text-[10px] font-mono text-slate-400 shrink-0">
              {stats.fps} fps · {stats.kbps} kbps
            </span>
          )}
        </div>

        {/* Top-left: a live dot on a grid tile, the negotiated resolution on the focused
            one — where there is room for it and it is the number that matters. */}
        {enabled && stream && !focused && (
          <span className="absolute top-2 left-2 z-10 w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
        )}
        {enabled && stream && focused && (
          <span className="absolute top-2 left-2 z-10 inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-slate-950/80 border border-slate-700/60 text-[10px] font-mono text-slate-300">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse" />
            {stats.width > 0 ? `${stats.width}×${stats.height}` : 'full quality'}
          </span>
        )}

        {/* Collapsing the focused tile is its only control; everything else lives on the
            page header, so the grid stays uncluttered. */}
        {focused && (
          <button
            onClick={(e) => { e.stopPropagation(); onFocus?.(); }}
            className="absolute top-2 right-2 z-30 inline-flex items-center gap-1.5 px-2.5 py-1 rounded-xl bg-rose-500/10 hover:bg-rose-500/20 text-rose-400 border border-rose-500/30 text-[10px] font-semibold transition-all"
          >
            <PowerOff className="w-3 h-3" />
            Close
          </button>
        )}
      </div>
    </div>
  );
};
