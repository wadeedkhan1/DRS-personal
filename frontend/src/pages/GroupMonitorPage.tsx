import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useDevices } from '../context/DeviceContext';
import { MonitorTile } from '../components/MonitorTile';
import { SessionQuality } from '../api/session';
import { Device } from '../types';
import {
  FolderKanban, LayoutGrid, Maximize2, Minimize2, MonitorOff, Tv,
} from 'lucide-react';

/**
 * What a grid tile asks its device to produce.
 *
 * A wall of a dozen panes at the deployment default (24fps / 1280px) would be a dozen
 * devices' encoders and one browser decoding ~30Mbps. A thumbnail is enough to see that
 * someone is at their desk and what they are doing; the detail is what enlarging is for.
 */
const TILE_QUALITY: SessionQuality = { fps: 4, maxWidth: 480 };

/**
 * How many tiles connect on their own.
 *
 * Every live tile exclusively claims its device (SRS FR-5.3), so this is a courtesy to
 * the other operators as much as a resource limit: opening a wall should not silently
 * lock every machine in the org. Devices past the cap render a Start button.
 */
const MAX_LIVE_TILES = 12;

/**
 * The monitoring wall: many devices at once, one enlarged on click.
 *
 * There is no new authorisation path here. The devices come from `useDevices()`, which is
 * scoped by `GET /api/devices`; each tile opens an ordinary `/ws/session`, which re-checks
 * `canViewDevice` against live team membership; and a scoped wall resolves its team from
 * `useDevices().groups`, which for an Admin contains only teams they are a member of. An
 * operator who cannot open a device one at a time cannot open it here either.
 */
export const GroupMonitorPage: React.FC = () => {
  const { groupId } = useParams<{ groupId: string }>();
  const { devices, groups, loading } = useDevices();

  const [focusedId, setFocusedId] = useState<string | null>(null);
  const [started, setStarted] = useState<string[]>([]);
  const [isFullscreen, setIsFullscreen] = useState(false);
  const wallRef = useRef<HTMLDivElement>(null);

  const group = groupId ? groups.find((g) => g.id === groupId) : undefined;

  const scoped = useMemo(() => {
    const list = groupId ? devices.filter((d) => d.group_id === groupId) : devices;
    // Reachable first, then offline, then by name. `online` and `in_session` rank the
    // SAME on purpose: a tile going live is exactly what turns its device in_session, so
    // ranking them apart would have the wall reorder itself every time a session opened,
    // shuffling devices in and out of the cap and tearing down sessions it had just made.
    const rank = (d: Device) => (d.status === 'offline' ? 1 : 0);
    return [...list].sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
  }, [devices, groupId]);

  // The first MAX_LIVE_TILES reachable devices connect by themselves; anything the
  // operator explicitly started joins them.
  //
  // Sticky: a device that is already live keeps its slot while it stays reachable, so a
  // device blinking offline and back cannot evict a neighbour and then take its place
  // again. Only genuine departures free a slot. Recomputing this is idempotent, so
  // StrictMode's double-invoke is harmless.
  const liveSlots = useRef<Set<string>>(new Set());
  const autoLive = useMemo(() => {
    const reachable = scoped.filter((d) => d.status !== 'offline');
    const keep = new Set<string>();
    for (const d of reachable) {
      if (liveSlots.current.has(d.id)) keep.add(d.id);
    }
    for (const d of reachable) {
      if (keep.size >= MAX_LIVE_TILES) break;
      keep.add(d.id);
    }
    liveSlots.current = keep;
    return keep;
  }, [scoped]);

  const isLive = useCallback(
    (id: string) => autoLive.has(id) || started.includes(id),
    [autoLive, started],
  );

  const focused = focusedId ? scoped.find((d) => d.id === focusedId) ?? null : null;

  // A device that vanishes from the scoped list (unassigned, deleted, or the team
  // changed under us) must not stay pinned in the focus pane.
  useEffect(() => {
    if (focusedId && !scoped.some((d) => d.id === focusedId)) setFocusedId(null);
  }, [scoped, focusedId]);

  useEffect(() => {
    if (!focusedId) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setFocusedId(null);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [focusedId]);

  useEffect(() => {
    const onChange = () => setIsFullscreen(Boolean(document.fullscreenElement));
    document.addEventListener('fullscreenchange', onChange);
    return () => document.removeEventListener('fullscreenchange', onChange);
  }, []);

  const toggleFullscreen = () => {
    if (document.fullscreenElement) {
      void document.exitFullscreen();
    } else {
      void wallRef.current?.requestFullscreen().catch(() => {
        // Some browsers refuse without a trusted gesture; leaving the wall inline is a
        // fine outcome, so this is not worth surfacing.
      });
    }
  };

  const liveCount = scoped.filter((d) => isLive(d.id) && d.status !== 'offline').length;

  // A team id that resolves to nothing means either it does not exist or this Admin is
  // not on it. Both answer the same way, matching how the server treats an invisible
  // device — the page must not become a way to find out which teams exist.
  if (groupId && !loading && !group) {
    return (
      <div className="space-y-6">
        <div className="p-12 text-center rounded-2xl bg-slate-900/20 border border-slate-800/60">
          <FolderKanban className="w-10 h-10 text-slate-700 mx-auto mb-3" />
          <p className="text-sm text-slate-300 font-medium">Team not found</p>
          <p className="text-xs text-slate-500 mt-1">
            It may have been deleted, or you may not be a member of it.
          </p>
          <Link
            to="/teams"
            className="inline-block mt-4 px-4 py-2.5 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-300 border border-slate-700/60 text-xs font-semibold transition-all"
          >
            Back to teams
          </Link>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Monitoring Wall</h1>
          <p className="text-xs text-slate-400 mt-0.5">
            {group ? (
              <>
                Team <span className="text-slate-300 font-medium">{group.name}</span> ·{' '}
              </>
            ) : null}
            {scoped.length} device{scoped.length === 1 ? '' : 's'} · {liveCount} watching · tiles
            stream at {TILE_QUALITY.fps} fps / {TILE_QUALITY.maxWidth}px, click one for full quality
          </p>
        </div>
        <button
          onClick={toggleFullscreen}
          className="shrink-0 inline-flex items-center gap-2 px-4 py-2.5 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-300 border border-slate-700/60 text-xs font-semibold transition-all"
        >
          {isFullscreen ? <Minimize2 className="w-3.5 h-3.5" /> : <Maximize2 className="w-3.5 h-3.5" />}
          {isFullscreen ? 'Exit fullscreen' : 'Fullscreen'}
        </button>
      </div>

      {loading && scoped.length === 0 ? (
        <div className="p-12 flex flex-col items-center gap-3 rounded-2xl bg-slate-900/20 border border-slate-800/60">
          <div className="w-8 h-8 rounded-full border-2 border-sky-500 border-t-transparent animate-spin" />
          <span className="text-xs font-mono text-slate-500">Loading devices…</span>
        </div>
      ) : scoped.length === 0 ? (
        <div className="p-12 text-center rounded-2xl bg-slate-900/20 border border-slate-800/60">
          <MonitorOff className="w-10 h-10 text-slate-700 mx-auto mb-3" />
          <p className="text-sm text-slate-300 font-medium">Nothing to monitor</p>
          <p className="text-xs text-slate-500 mt-1">
            {group
              ? 'This team has no devices assigned to it yet.'
              : 'No devices are visible to you yet.'}
          </p>
        </div>
      ) : (
        <div
          ref={wallRef}
          className={`bg-slate-950 flex flex-col gap-4 ${
            isFullscreen ? 'h-screen p-4' : 'h-[calc(100vh-14rem)] rounded-2xl'
          }`}
        >
          {focused && (
            <div className="flex-1 min-h-0">
              <MonitorTile
                key={`focus-${focused.id}`}
                device={focused}
                live
                focused
                onFocus={() => setFocusedId(null)}
              />
            </div>
          )}

          <div
            className={`grid gap-3 overflow-y-auto ${
              focused
                ? 'grid-cols-3 sm:grid-cols-4 lg:grid-cols-6 shrink-0 max-h-40'
                : 'grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 flex-1 auto-rows-fr'
            }`}
          >
            {scoped.map((device) => {
              // The focused device already holds a full-quality session. Leaving its grid
              // tile connected would have the wall open two sessions on one device and
              // the server would reject the second — so it parks while it is enlarged.
              const enlarged = focused?.id === device.id;
              return (
                <div key={device.id} className={focused ? 'aspect-video' : 'min-h-[9rem]'}>
                  <MonitorTile
                    device={device}
                    live={isLive(device.id) && !enlarged}
                    enlargedElsewhere={enlarged}
                    quality={TILE_QUALITY}
                    onFocus={() => setFocusedId(enlarged ? null : device.id)}
                    onStart={
                      isLive(device.id)
                        ? undefined
                        : () => setStarted((prev) => [...prev, device.id])
                    }
                  />
                </div>
              );
            })}
          </div>
        </div>
      )}

      <div className="flex items-center gap-4 text-[11px] text-slate-500">
        <span className="inline-flex items-center gap-1.5">
          <LayoutGrid className="w-3.5 h-3.5" />
          Each live tile holds that device exclusively — another operator will see it as busy.
        </span>
        <Link to="/viewer" className="inline-flex items-center gap-1.5 text-sky-400 hover:text-sky-300">
          <Tv className="w-3.5 h-3.5" />
          Single device viewer
        </Link>
      </div>
    </div>
  );
};
