import React, { useEffect, useState } from 'react';
import { ApiClient } from '../api/client';
import { Session } from '../types';
import { RefreshCw, Clock } from 'lucide-react';

/**
 * Session history.
 *
 * GET /api/sessions, the Session type and ApiClient.listSessions() were all built and had
 * no caller and no page. A Super Admin sees the whole org; an Admin sees their own
 * sessions plus any session on a device they can reach through a team.
 */
export const SessionsPage: React.FC = () => {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [loading, setLoading] = useState(true);

  const loadSessions = async () => {
    try {
      setSessions(await ApiClient.listSessions());
    } catch (e) {
      console.error('Failed to load sessions:', e);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadSessions();
  }, []);

  // An active session has no ended_at yet, so its elapsed time is measured against now.
  const duration = (session: Session) => {
    const start = new Date(session.started_at).getTime();
    const end = session.ended_at ? new Date(session.ended_at).getTime() : Date.now();
    const secs = Math.max(0, Math.round((end - start) / 1000));
    const mins = Math.floor(secs / 60);
    return mins > 0 ? `${mins}m ${secs % 60}s` : `${secs}s`;
  };

  const statusStyle = (status: Session['status']) => {
    if (status === 'active') return 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20';
    if (status === 'terminated') return 'bg-rose-500/10 text-rose-400 border-rose-500/20';
    return 'bg-slate-800/60 text-slate-400 border-slate-700/60';
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Session History</h1>
          <p className="text-xs text-slate-400 mt-0.5">
            Every monitoring session, who opened it, and how long it ran. Most recent first.
          </p>
        </div>

        <button
          onClick={loadSessions}
          className="p-2.5 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-300 border border-slate-700"
          title="Refresh sessions"
        >
          <RefreshCw className="w-4 h-4" />
        </button>
      </div>

      {loading ? (
        <div className="p-16 flex flex-col items-center justify-center text-slate-400">
          <div className="w-8 h-8 rounded-full border-2 border-sky-500 border-t-transparent animate-spin mb-3"></div>
          <span className="text-xs font-mono">Loading sessions...</span>
        </div>
      ) : sessions.length === 0 ? (
        <div className="p-12 text-center rounded-2xl bg-slate-900/20 border border-slate-800/60">
          <Clock className="w-10 h-10 text-slate-600 mx-auto mb-3" />
          <h3 className="text-sm font-semibold text-slate-300">No sessions recorded yet</h3>
          <p className="text-xs text-slate-500 mt-1">
            Open a live view on an online endpoint and it will appear here.
          </p>
        </div>
      ) : (
        <div className="rounded-2xl bg-slate-900/60 border border-slate-800 overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full text-left text-xs">
              <thead className="bg-slate-950/60 text-slate-400 uppercase tracking-wider text-[10px] font-semibold border-b border-slate-800">
                <tr>
                  <th className="px-6 py-4">Started</th>
                  <th className="px-6 py-4">Device</th>
                  <th className="px-6 py-4">Operator</th>
                  <th className="px-6 py-4">Duration</th>
                  <th className="px-6 py-4">Mode</th>
                  <th className="px-6 py-4">Status</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-800/60 text-slate-300">
                {sessions.map((session) => (
                  <tr key={session.id} className="hover:bg-slate-800/30 transition-colors">
                    <td className="px-6 py-4 text-slate-400 font-mono whitespace-nowrap">
                      {new Date(session.started_at).toLocaleString()}
                    </td>
                    <td className="px-6 py-4 font-medium text-slate-200">
                      {session.device_name || '—'}
                    </td>
                    <td className="px-6 py-4 text-slate-300">{session.admin_email || '—'}</td>
                    <td className="px-6 py-4 font-mono text-slate-400 whitespace-nowrap">
                      {duration(session)}
                    </td>
                    <td className="px-6 py-4 uppercase text-slate-400 font-semibold text-[10px]">
                      {session.mode}
                    </td>
                    <td className="px-6 py-4">
                      <span
                        className={`inline-flex items-center px-2 py-0.5 rounded text-[11px] font-mono font-medium border ${statusStyle(
                          session.status,
                        )}`}
                      >
                        {session.status}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
};
