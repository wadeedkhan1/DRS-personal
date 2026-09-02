import React, { useState, useEffect } from 'react';
import { ApiClient } from '../api/client';
import { UsageReport } from '../types';
import { Download, Monitor, Activity, Clock } from 'lucide-react';

export const ReportsPage: React.FC = () => {
  const [report, setReport] = useState<UsageReport | null>(null);

  useEffect(() => {
    ApiClient.getUsageReport().then(setReport).catch(() => {});
  }, []);

  const exportCSV = () => {
    if (!report) return;
    const csvContent = "data:text/csv;charset=utf-8," 
      + "Metric,Value\n"
      + `Total Devices,${report.total_devices}\n`
      + `Online Devices,${report.online_devices}\n`
      + `Total Sessions,${report.total_sessions}\n`
      + `Active Sessions,${report.active_sessions}\n`
      + `Avg Duration (Mins),${report.avg_duration_minutes.toFixed(2)}\n`
      + `Total Admins,${report.total_admins}\n`;

    const encodedUri = encodeURI(csvContent);
    const link = document.createElement("a");
    link.setAttribute("href", encodedUri);
    link.setAttribute("download", `DRS_Usage_Report_${Date.now()}.csv`);
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Platform Analytics & Reports</h1>
          <p className="text-xs text-slate-400 mt-0.5">
            Operational metrics, monitoring usage, and device availability summary.
          </p>
        </div>

        <button
          onClick={exportCSV}
          className="flex items-center gap-2 px-4 py-2.5 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-200 text-xs font-semibold border border-slate-700 transition-colors"
        >
          <Download className="w-4 h-4 text-sky-400" />
          <span>Export CSV</span>
        </button>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-5">
        <div className="p-6 rounded-2xl bg-slate-900/60 border border-slate-800">
          <div className="w-10 h-10 rounded-xl bg-sky-500/10 border border-sky-500/20 flex items-center justify-center text-sky-400 mb-4">
            <Monitor className="w-5 h-5" />
          </div>
          <div className="text-xs font-semibold text-slate-400">Total Enrolled Endpoints</div>
          <div className="text-3xl font-bold text-slate-100 mt-1">{report?.total_devices ?? 0}</div>
          <div className="text-[11px] text-emerald-400 mt-2 flex items-center gap-1">
            <span>●</span> {report?.online_devices ?? 0} devices currently online
          </div>
        </div>

        <div className="p-6 rounded-2xl bg-slate-900/60 border border-slate-800">
          <div className="w-10 h-10 rounded-xl bg-emerald-500/10 border border-emerald-500/20 flex items-center justify-center text-emerald-400 mb-4">
            <Activity className="w-5 h-5" />
          </div>
          <div className="text-xs font-semibold text-slate-400">Monitoring Sessions Initiated</div>
          <div className="text-3xl font-bold text-slate-100 mt-1">{report?.total_sessions ?? 0}</div>
          <div className="text-[11px] text-amber-400 mt-2 flex items-center gap-1">
            <span>●</span> {report?.active_sessions ?? 0} active live sessions
          </div>
        </div>

        <div className="p-6 rounded-2xl bg-slate-900/60 border border-slate-800">
          <div className="w-10 h-10 rounded-xl bg-indigo-500/10 border border-indigo-500/20 flex items-center justify-center text-indigo-400 mb-4">
            <Clock className="w-5 h-5" />
          </div>
          <div className="text-xs font-semibold text-slate-400">Average Session Duration</div>
          <div className="text-3xl font-bold text-slate-100 mt-1">
            {report?.avg_duration_minutes ? `${report.avg_duration_minutes.toFixed(1)}m` : '0.0m'}
          </div>
          <div className="text-[11px] text-slate-400 mt-2">
            Based on completed P2P monitoring sessions
          </div>
        </div>
      </div>
    </div>
  );
};
