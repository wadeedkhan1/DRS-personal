import React, { useState, useEffect } from 'react';
import { ApiClient } from '../api/client';
import { AuditLog } from '../types';
import { RefreshCw } from 'lucide-react';

export const AuditLogsPage: React.FC = () => {
  const [logs, setLogs] = useState<AuditLog[]>([]);

  const loadLogs = async () => {
    try {
      const data = await ApiClient.listAuditLogs();
      setLogs(data);
    } catch (e) {
      console.error(e);
    }
  };

  useEffect(() => {
    loadLogs();
  }, []);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Audit Trail & Compliance</h1>
          <p className="text-xs text-slate-400 mt-0.5">
            Immutable log of all user logins, monitoring sessions, and device modifications.
          </p>
        </div>

        <button
          onClick={loadLogs}
          className="p-2.5 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-300 border border-slate-700"
          title="Refresh Audit Logs"
        >
          <RefreshCw className="w-4 h-4" />
        </button>
      </div>

      <div className="rounded-2xl bg-slate-900/60 border border-slate-800 overflow-hidden">
        <table className="w-full text-left text-xs">
          <thead className="bg-slate-950/60 text-slate-400 uppercase tracking-wider text-[10px] font-semibold border-b border-slate-800">
            <tr>
              <th className="px-6 py-4">Timestamp</th>
              <th className="px-6 py-4">Actor</th>
              <th className="px-6 py-4">Action Event</th>
              <th className="px-6 py-4">Target Type</th>
              <th className="px-6 py-4">IP Address</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-800/60 text-slate-300">
            {logs.map((log) => (
              <tr key={log.id} className="hover:bg-slate-800/30 transition-colors">
                <td className="px-6 py-4 text-slate-400 font-mono">
                  {new Date(log.created_at).toLocaleString()}
                </td>
                <td className="px-6 py-4 font-medium text-slate-200">
                  {log.actor_email || 'System / Agent'}
                </td>
                <td className="px-6 py-4">
                  <span className="inline-flex items-center px-2 py-0.5 rounded text-[11px] font-mono font-medium bg-sky-500/10 text-sky-400 border border-sky-500/20">
                    {log.action}
                  </span>
                </td>
                <td className="px-6 py-4 uppercase text-slate-400 font-semibold text-[10px]">
                  {log.target_type}
                </td>
                <td className="px-6 py-4 font-mono text-slate-400">
                  {log.ip_address || '—'}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
};
