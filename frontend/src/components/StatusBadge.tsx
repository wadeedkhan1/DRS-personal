import React from 'react';
import { DeviceStatus } from '../types';

export const StatusBadge: React.FC<{ status: DeviceStatus }> = ({ status }) => {
  switch (status) {
    case 'online':
      return (
        <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">
          <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse"></span>
          Online
        </span>
      );
    case 'in_session':
      return (
        <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium bg-amber-500/10 text-amber-400 border border-amber-500/20">
          <span className="w-1.5 h-1.5 rounded-full bg-amber-400 animate-ping"></span>
          In Session
        </span>
      );
    case 'offline':
    default:
      return (
        <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium bg-slate-800 text-slate-400 border border-slate-700/50">
          <span className="w-1.5 h-1.5 rounded-full bg-slate-500"></span>
          Offline
        </span>
      );
  }
};
