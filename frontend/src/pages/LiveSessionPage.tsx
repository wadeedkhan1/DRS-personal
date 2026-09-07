import React, { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { useDevices } from '../context/DeviceContext';
import { ApiClient } from '../api/client';
import { ScreenViewer } from '../components/ScreenViewer';
import { Device } from '../types';
import { AlertCircle, ArrowLeft } from 'lucide-react';

/**
 * One session, at its own URL.
 *
 * The device is resolved from the device list when it is already loaded, and fetched by
 * id when it is not. That second path is what makes the URL shareable: a cold load has no
 * device list yet, so without it a pasted link or a refresh mid-session would render
 * nothing.
 *
 * Mounting ScreenViewer is what opens the session, exactly as before — this page adds a
 * route, not a second place that can start one.
 */
export const LiveSessionPage: React.FC = () => {
  const { deviceId } = useParams<{ deviceId: string }>();
  const navigate = useNavigate();
  const { devices, loading } = useDevices();

  const fromList = devices.find((d) => d.id === deviceId);
  const [fetched, setFetched] = useState<Device | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    // Wait for the list before falling back to a fetch, otherwise every cold load makes
    // a request for a device that is about to arrive anyway.
    if (!deviceId || fromList || loading) return;
    let cancelled = false;
    ApiClient.getDevice(deviceId)
      .then((d) => {
        if (!cancelled) setFetched(d);
      })
      .catch(() => {
        // The backend answers "not found" and "not permitted" identically on purpose, so
        // this is as specific as the message can honestly get.
        if (!cancelled) setError('That device does not exist, or it is not assigned to you.');
      });
    return () => {
      cancelled = true;
    };
  }, [deviceId, fromList, loading]);

  const device = fromList ?? fetched;

  if (error) {
    return (
      <div className="space-y-6">
        <button
          onClick={() => navigate('/devices')}
          className="flex items-center gap-2 text-xs text-slate-400 hover:text-slate-200"
        >
          <ArrowLeft className="w-3.5 h-3.5" />
          <span>Back to devices</span>
        </button>
        <div className="p-12 text-center rounded-2xl bg-slate-900/30 border border-slate-800 flex flex-col items-center">
          <AlertCircle className="w-10 h-10 text-rose-400/70 mb-3" />
          <h3 className="text-sm font-semibold text-slate-200">Device unavailable</h3>
          <p className="text-xs text-slate-500 mt-1 max-w-sm">{error}</p>
        </div>
      </div>
    );
  }

  if (!device) {
    return (
      <div className="p-16 flex flex-col items-center justify-center text-slate-400">
        <div className="w-8 h-8 rounded-full border-2 border-sky-500 border-t-transparent animate-spin mb-3"></div>
        <span className="text-xs font-mono">Resolving device...</span>
      </div>
    );
  }

  return (
    <div className="h-[calc(100vh-8rem)] w-full">
      <ScreenViewer device={device} onClose={() => navigate('/devices')} />
    </div>
  );
};
