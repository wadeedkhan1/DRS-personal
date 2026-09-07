import React from 'react';
import { useNavigate } from 'react-router-dom';
import { useDevices } from '../context/DeviceContext';
import { StatusBadge } from '../components/StatusBadge';
import { Tv, Monitor, Smartphone, Play } from 'lucide-react';

/**
 * Device picker for live viewing.
 *
 * Picking a device navigates to that device's own session URL; this page never mounts a
 * viewer itself. Keeping exactly one component able to open a session is what stopped a
 * single click from producing two competing session requests, and routing preserves that
 * — LiveSessionPage is still the only mount point.
 */
export const LiveViewerPage: React.FC = () => {
  const { devices } = useDevices();
  const navigate = useNavigate();

  // A device already in a session cannot be joined: the backend allows one viewer at a
  // time (SRS FR-5.3), so offering it here would only produce a refusal.
  const availableDevices = devices.filter((d) => d.status === 'online');
  const busyDevices = devices.filter((d) => d.status === 'in_session');

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">Live screen monitoring</h1>
        <p className="text-xs text-slate-400 mt-0.5">
          Select an online endpoint to open a peer-to-peer VP8 video session. Video streams
          directly between the device and this browser.
        </p>
      </div>

      {availableDevices.length === 0 ? (
        <div className="p-16 text-center rounded-2xl bg-slate-900/30 border border-slate-800 flex flex-col items-center">
          <Tv className="w-12 h-12 text-slate-600 mb-4" />
          <h3 className="text-base font-semibold text-slate-200">No devices available</h3>
          <p className="text-xs text-slate-500 mt-1 max-w-md">
            {busyDevices.length > 0
              ? `${busyDevices.length} device(s) are currently being viewed by another operator.`
              : 'No agents are connected. Make sure the DRS agent is running on the target machine.'}
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {availableDevices.map((dev) => (
            <div
              key={dev.id}
              className="p-5 rounded-2xl bg-slate-900/60 border border-slate-800 flex items-center justify-between gap-4"
            >
              <div className="flex items-center gap-3">
                <div className="w-10 h-10 rounded-xl bg-slate-800 border border-slate-700 flex items-center justify-center text-slate-300">
                  {dev.type === 'windows' ? (
                    <Monitor className="w-5 h-5 text-sky-400" />
                  ) : (
                    <Smartphone className="w-5 h-5 text-emerald-400" />
                  )}
                </div>
                <div>
                  <div className="text-sm font-semibold text-slate-100">{dev.name}</div>
                  <div className="text-[11px] text-slate-400">{dev.ip_address || 'Unknown address'}</div>
                </div>
              </div>

              <div className="flex items-center gap-3">
                <StatusBadge status={dev.status} />
                <button
                  onClick={() => navigate(`/devices/${dev.id}/live`)}
                  className="p-2.5 rounded-xl bg-sky-500 hover:bg-sky-400 text-white shadow-md shadow-sky-500/20 transition-all"
                  title="Start session"
                >
                  <Play className="w-4 h-4 fill-current" />
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
};
