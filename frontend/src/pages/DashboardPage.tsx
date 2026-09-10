import React, { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { useDevices } from '../context/DeviceContext';
import { useAuth } from '../context/AuthContext';
import { Device } from '../types';
import { StatusBadge } from '../components/StatusBadge';
import { EnrollDeviceModal } from '../components/EnrollDeviceModal';
import { AssignDeviceModal } from '../components/AssignDeviceModal';
import { ApiClient } from '../api/client';
import {
  Monitor,
  Smartphone,
  Search,
  Plus,
  Play,
  RefreshCw,
  Trash2,
  UserCog,
  FolderKanban,
} from 'lucide-react';

export const DashboardPage: React.FC = () => {
  const { devices, groups, refreshDevices } = useDevices();
  const { isSuperAdmin } = useAuth();
  const navigate = useNavigate();

  const [searchQuery, setSearchQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<string>('all');
  const [typeFilter, setTypeFilter] = useState<string>('all');
  const [groupFilter, setGroupFilter] = useState<string>('all');
  const [isEnrollModalOpen, setIsEnrollModalOpen] = useState(false);
  const [assigningDevice, setAssigningDevice] = useState<Device | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);

  const handleDelete = async (device: Device) => {
    if (
      !window.confirm(
        `Delete "${device.name}"? This removes the device and ends any active session. ` +
          `The endpoint agent will need to re-enroll to appear again.`,
      )
    ) {
      return;
    }
    setDeletingId(device.id);
    try {
      await ApiClient.deleteDevice(device.id);
      await refreshDevices();
    } catch (e: any) {
      window.alert(`Failed to delete device: ${e?.message ?? e}`);
    } finally {
      setDeletingId(null);
    }
  };

  const filteredDevices = devices.filter((d) => {
    const matchesSearch =
      d.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
      d.ip_address.toLowerCase().includes(searchQuery.toLowerCase()) ||
      (d.assigned_admin_email && d.assigned_admin_email.toLowerCase().includes(searchQuery.toLowerCase()));

    const matchesStatus = statusFilter === 'all' || d.status === statusFilter;
    const matchesType = typeFilter === 'all' || d.type === typeFilter;
    // 'none' picks out the devices no team owns, which is the set worth finding after a
    // team is deleted.
    const matchesGroup =
      groupFilter === 'all' ||
      (groupFilter === 'none' ? !d.group_id : d.group_id === groupFilter);

    return matchesSearch && matchesStatus && matchesType && matchesGroup;
  });

  const onlineCount = devices.filter((d) => d.status === 'online').length;
  const inSessionCount = devices.filter((d) => d.status === 'in_session').length;
  const offlineCount = devices.filter((d) => d.status === 'offline').length;

  return (
    <div className="space-y-6">
      {/* Header & Metrics */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Endpoints & Devices</h1>
          <p className="text-xs text-slate-400 mt-0.5">
            Real-time screen monitoring status for enrolled Windows & Android devices.
          </p>
        </div>

        <div className="flex items-center gap-3">
          <button
            onClick={() => refreshDevices()}
            className="p-2.5 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-300 transition-colors border border-slate-700/60"
            title="Refresh List"
          >
            <RefreshCw className="w-4 h-4" />
          </button>

          {/* Open to both roles. An Admin's invite link is pinned to them and may only
              target a team they are on, so it can grant no access they do not already
              have — see GenerateEnrollmentToken. */}
          <button
            onClick={() => setIsEnrollModalOpen(true)}
            className="flex items-center gap-2 px-4 py-2.5 rounded-xl bg-gradient-to-r from-sky-500 to-blue-600 hover:from-sky-400 hover:to-blue-500 text-white text-xs font-semibold shadow-lg shadow-sky-500/20 transition-all"
          >
            <Plus className="w-4 h-4" />
            <span>Invite devices</span>
          </button>
        </div>
      </div>

      {/* Metrics Row */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <div className="p-4 rounded-2xl bg-slate-900/60 border border-slate-800">
          <div className="text-xs font-semibold text-slate-400">Total Devices</div>
          <div className="text-2xl font-bold text-slate-100 mt-1">{devices.length}</div>
        </div>
        <div className="p-4 rounded-2xl bg-slate-900/60 border border-slate-800">
          <div className="text-xs font-semibold text-emerald-400">Online</div>
          <div className="text-2xl font-bold text-emerald-400 mt-1">{onlineCount}</div>
        </div>
        <div className="p-4 rounded-2xl bg-slate-900/60 border border-slate-800">
          <div className="text-xs font-semibold text-amber-400">In Session</div>
          <div className="text-2xl font-bold text-amber-400 mt-1">{inSessionCount}</div>
        </div>
        <div className="p-4 rounded-2xl bg-slate-900/60 border border-slate-800">
          <div className="text-xs font-semibold text-slate-500">Offline</div>
          <div className="text-2xl font-bold text-slate-400 mt-1">{offlineCount}</div>
        </div>
      </div>

      {/* Filter & Search Bar */}
      <div className="p-4 rounded-2xl bg-slate-900/40 border border-slate-800 flex flex-col md:flex-row gap-3 items-center justify-between">
        <div className="relative w-full md:w-80">
          <Search className="w-4 h-4 text-slate-500 absolute left-3.5 top-3" />
          <input
            type="text"
            placeholder="Search by device, IP, or admin..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="w-full pl-10 pr-4 py-2 rounded-xl bg-slate-950/60 border border-slate-800 text-xs text-slate-200 placeholder:text-slate-500 focus:outline-none focus:border-sky-500"
          />
        </div>

        <div className="flex flex-wrap items-center gap-3 w-full md:w-auto">
          {/* Status Filter */}
          <select
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value)}
            className="px-3 py-2 rounded-xl bg-slate-950/60 border border-slate-800 text-xs text-slate-300 focus:outline-none focus:border-sky-500"
          >
            <option value="all">All Statuses</option>
            <option value="online">Online</option>
            <option value="in_session">In Session</option>
            <option value="offline">Offline</option>
          </select>

          {/* OS Filter */}
          <select
            value={typeFilter}
            onChange={(e) => setTypeFilter(e.target.value)}
            className="px-3 py-2 rounded-xl bg-slate-950/60 border border-slate-800 text-xs text-slate-300 focus:outline-none focus:border-sky-500"
          >
            <option value="all">All Platforms</option>
            <option value="windows">Windows</option>
            <option value="android">Android</option>
          </select>

          {/* Team Filter */}
          <select
            value={groupFilter}
            onChange={(e) => setGroupFilter(e.target.value)}
            className="px-3 py-2 rounded-xl bg-slate-950/60 border border-slate-800 text-xs text-slate-300 focus:outline-none focus:border-sky-500"
          >
            <option value="all">All Teams</option>
            {groups.map((g) => (
              <option key={g.id} value={g.id}>
                {g.name}
              </option>
            ))}
            <option value="none">No team</option>
          </select>
        </div>
      </div>

      {/* Device List Grid / Cards */}
      {filteredDevices.length === 0 ? (
        <div className="p-12 text-center rounded-2xl bg-slate-900/20 border border-slate-800/60">
          <Monitor className="w-10 h-10 text-slate-600 mx-auto mb-3" />
          <h3 className="text-sm font-semibold text-slate-300">No matching devices found</h3>
          <p className="text-xs text-slate-500 mt-1 max-w-sm mx-auto">
            {isSuperAdmin
              ? 'Click "+ Enroll Device" to generate a token and connect a Windows or Android agent.'
              : 'You do not have any devices assigned to your account yet.'}
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {filteredDevices.map((device) => {
            const isOnline = device.status === 'online' || device.status === 'in_session';
            return (
              <div
                key={device.id}
                className="rounded-2xl bg-slate-900/60 border border-slate-800 hover:border-slate-700/80 p-5 transition-all flex flex-col justify-between space-y-4"
              >
                <div>
                  <div className="flex items-start justify-between gap-3 mb-3">
                    <div className="flex items-center gap-3">
                      <div className="w-10 h-10 rounded-xl bg-slate-800 border border-slate-700/60 flex items-center justify-center text-slate-300">
                        {device.type === 'windows' ? (
                          <Monitor className="w-5 h-5 text-sky-400" />
                        ) : (
                          <Smartphone className="w-5 h-5 text-emerald-400" />
                        )}
                      </div>
                      <div>
                        <h4 className="text-sm font-semibold text-slate-100">{device.name}</h4>
                        <div className="text-[11px] text-slate-400 capitalize">
                          {device.type} • {device.os_version || 'Unknown OS'}
                        </div>
                      </div>
                    </div>
                    <StatusBadge status={device.status} />
                  </div>

                  {/* What this device consented to expose, chosen by whoever installed the agent. */}
                  <div className="flex items-center gap-1.5 mb-3">
                    <span
                      className={`text-[10px] font-semibold px-2 py-0.5 rounded border ${
                        device.allow_screen === false
                          ? 'bg-slate-800/60 text-slate-500 border-slate-700/60 line-through'
                          : 'bg-sky-500/10 text-sky-300 border-sky-500/20'
                      }`}
                    >
                      Screen
                    </span>
                    <span
                      className={`text-[10px] font-semibold px-2 py-0.5 rounded border ${
                        device.allow_terminal
                          ? 'bg-amber-500/10 text-amber-300 border-amber-500/20'
                          : 'bg-slate-800/60 text-slate-500 border-slate-700/60 line-through'
                      }`}
                    >
                      Terminal
                    </span>
                  </div>

                  <div className="space-y-1.5 text-xs text-slate-400 pt-2 border-t border-slate-800/80">
                    <div className="flex justify-between">
                      <span className="text-slate-500">IP Address:</span>
                      <span className="font-mono text-slate-300">{device.ip_address || '—'}</span>
                    </div>
                    <div className="flex justify-between">
                      <span className="text-slate-500">Assigned Admin:</span>
                      <span className="text-slate-300">{device.assigned_admin_email || 'Unassigned'}</span>
                    </div>
                    <div className="flex justify-between items-center gap-2">
                      <span className="text-slate-500">Team:</span>
                      {device.group_id && device.group_name ? (
                        <Link
                          to={`/teams/${device.group_id}`}
                          className="inline-flex items-center gap-1.5 text-sky-400 hover:text-sky-300 truncate"
                        >
                          <FolderKanban className="w-3 h-3 shrink-0" />
                          <span className="truncate">{device.group_name}</span>
                        </Link>
                      ) : (
                        <span className="text-slate-500">No team</span>
                      )}
                    </div>
                    <div className="flex justify-between">
                      <span className="text-slate-500">Last Active:</span>
                      <span className="text-slate-300">
                        {device.last_seen_at ? new Date(device.last_seen_at).toLocaleTimeString() : 'Never'}
                      </span>
                    </div>
                  </div>
                </div>

                <div className="pt-3 border-t border-slate-800/80 flex items-center gap-2">
                  <button
                    disabled={!isOnline}
                    onClick={() => navigate(`/devices/${device.id}/live`)}
                    className="flex-1 py-2 px-3 rounded-xl bg-sky-500/10 hover:bg-sky-500/20 text-sky-400 border border-sky-500/30 text-xs font-semibold flex items-center justify-center gap-2 transition-all disabled:opacity-30 disabled:pointer-events-none"
                  >
                    <Play className="w-3.5 h-3.5 fill-current" />
                    <span>Live View</span>
                  </button>

                  {isSuperAdmin && (
                    <>
                      <button
                        onClick={() => setAssigningDevice(device)}
                        title="Assign admin & team"
                        className="py-2 px-3 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-300 border border-slate-700/60 transition-colors"
                      >
                        <UserCog className="w-3.5 h-3.5" />
                      </button>
                      <button
                        disabled={deletingId === device.id}
                        onClick={() => handleDelete(device)}
                        title="Delete device"
                        className="py-2 px-3 rounded-xl bg-rose-500/10 hover:bg-rose-500/20 text-rose-400 border border-rose-500/30 transition-all disabled:opacity-40 disabled:pointer-events-none"
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </button>
                    </>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      )}

      <EnrollDeviceModal
        isOpen={isEnrollModalOpen}
        onClose={() => setIsEnrollModalOpen(false)}
        onEnrolled={() => refreshDevices()}
      />

      <AssignDeviceModal device={assigningDevice} onClose={() => setAssigningDevice(null)} />
    </div>
  );
};
