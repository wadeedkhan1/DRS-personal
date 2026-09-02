import React from 'react';
import { useAuth } from '../context/AuthContext';
import { useDevices } from '../context/DeviceContext';
import { Shield, Radio, LogOut, User as UserIcon } from 'lucide-react';

export const Navbar: React.FC = () => {
  const { user, logout, isSuperAdmin } = useAuth();
  const { presenceConnected } = useDevices();

  return (
    <header className="h-16 border-b border-slate-800 bg-slate-900/60 backdrop-blur-md px-6 flex items-center justify-between sticky top-0 z-30">
      <div className="flex items-center gap-3">
        <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-sky-500 to-blue-700 flex items-center justify-center shadow-lg shadow-sky-500/20">
          <Shield className="w-5 h-5 text-white" />
        </div>
        <div>
          <div className="flex items-center gap-2">
            <span className="font-bold text-lg tracking-tight bg-gradient-to-r from-white via-slate-200 to-slate-400 bg-clip-text text-transparent">
              DRS Platform
            </span>
            <span className="text-[10px] uppercase font-bold tracking-wider px-1.5 py-0.5 rounded bg-sky-500/10 text-sky-400 border border-sky-500/20">
              Phase 1 MVP
            </span>
          </div>
          <p className="text-xs text-slate-400">Remote Screen Monitoring & Endpoint Control</p>
        </div>
      </div>

      <div className="flex items-center gap-4">
        {/* Live presence feed status. Reflects the actual socket, not merely that an object exists. */}
        <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-slate-800/60 border border-slate-700/60 text-xs">
          <Radio className={`w-3.5 h-3.5 ${presenceConnected ? 'text-emerald-400' : 'text-amber-400 animate-pulse'}`} />
          <span className="text-slate-300">
            Live updates: <span className={presenceConnected ? 'text-emerald-400 font-medium' : 'text-amber-400 font-medium'}>
              {presenceConnected ? 'Connected' : 'Reconnecting…'}
            </span>
          </span>
        </div>

        {/* User Badge */}
        <div className="flex items-center gap-3 pl-2 border-l border-slate-800">
          <div className="flex items-center gap-2.5">
            <div className="w-8 h-8 rounded-full bg-slate-800 border border-slate-700 flex items-center justify-center text-slate-300">
              <UserIcon className="w-4 h-4" />
            </div>
            <div className="text-left hidden sm:block">
              <div className="text-xs font-medium text-slate-200">{user?.email}</div>
              <div className="text-[10px] font-semibold text-sky-400 uppercase tracking-wider">
                {isSuperAdmin ? 'Super Admin' : 'Admin'}
              </div>
            </div>
          </div>

          {/* Logout */}
          <button
            onClick={logout}
            className="p-2 text-slate-400 hover:text-rose-400 hover:bg-rose-500/10 rounded-lg transition-colors"
            title="Sign Out"
          >
            <LogOut className="w-4 h-4" />
          </button>
        </div>
      </div>
    </header>
  );
};
