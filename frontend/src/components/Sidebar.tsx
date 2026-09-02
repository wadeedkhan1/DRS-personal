import React from 'react';
import { useAuth } from '../context/AuthContext';
import { Monitor, Users, FolderKanban, History, BarChart3, Tv } from 'lucide-react';

interface SidebarProps {
  currentTab: string;
  onTabChange: (tab: string) => void;
}

export const Sidebar: React.FC<SidebarProps> = ({ currentTab, onTabChange }) => {
  const { isSuperAdmin } = useAuth();

  const navItems = [
    { id: 'devices', label: 'Endpoints / Devices', icon: Monitor },
    { id: 'viewer', label: 'Live Screen Viewer', icon: Tv },
    ...(isSuperAdmin
      ? [
          { id: 'users', label: 'Admin Accounts', icon: Users },
          { id: 'groups', label: 'Teams & Groups', icon: FolderKanban },
        ]
      : []),
    { id: 'audit', label: 'Audit Trail', icon: History },
    { id: 'reports', label: 'Usage Reports', icon: BarChart3 },
  ];

  return (
    <aside className="w-64 border-r border-slate-800 bg-slate-900/30 flex flex-col p-4 space-y-6">
      <div>
        <div className="text-[11px] font-semibold text-slate-500 uppercase tracking-wider px-3 mb-2">
          Navigation
        </div>
        <nav className="space-y-1">
          {navItems.map((item) => {
            const Icon = item.icon;
            const active = currentTab === item.id;
            return (
              <button
                key={item.id}
                onClick={() => onTabChange(item.id)}
                className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-xl text-sm font-medium transition-all ${
                  active
                    ? 'bg-sky-500/10 text-sky-400 border border-sky-500/20 shadow-sm shadow-sky-500/10'
                    : 'text-slate-400 hover:text-slate-200 hover:bg-slate-800/60'
                }`}
              >
                <Icon className={`w-4 h-4 ${active ? 'text-sky-400' : 'text-slate-400'}`} />
                <span>{item.label}</span>
              </button>
            );
          })}
        </nav>
      </div>

      <div className="mt-auto p-4 rounded-xl bg-slate-900/80 border border-slate-800/80">
        <div className="flex items-center gap-2 mb-1.5">
          <div className="w-2 h-2 rounded-full bg-emerald-400"></div>
          <span className="text-xs font-semibold text-slate-200">Peer-to-peer mode</span>
        </div>
        <p className="text-[11px] text-slate-400 leading-relaxed">
          Video connects directly between device and browser where the network allows, and falls back to the TURN relay where it does not. The viewer shows which path each session took.
        </p>
      </div>
    </aside>
  );
};
