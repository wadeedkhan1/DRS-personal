import React, { useEffect, useState } from 'react';
import { ApiClient } from '../api/client';
import { useDevices } from '../context/DeviceContext';
import { Device, User } from '../types';
import { X, AlertCircle } from 'lucide-react';

interface AssignDeviceModalProps {
  /** The device to reassign, or null when the modal is closed. */
  device: Device | null;
  onClose: () => void;
}

/**
 * Moves a device between admins and teams.
 *
 * PUT /api/devices/{id}/assign has existed on both the server and in ApiClient since the
 * first commit with nothing calling it, so until now a device's admin and team could only
 * be set once, by the enrollment link that created it.
 */
export const AssignDeviceModal: React.FC<AssignDeviceModalProps> = ({ device, onClose }) => {
  const { groups, refreshDevices, refreshGroups } = useDevices();
  const [admins, setAdmins] = useState<User[]>([]);
  const [adminId, setAdminId] = useState('');
  const [groupId, setGroupId] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!device) return;
    setAdminId(device.assigned_admin_id ?? '');
    setGroupId(device.group_id ?? '');
    setError(null);
    ApiClient.listUsers()
      .then((users) => setAdmins(users.filter((u) => u.role === 'admin')))
      .catch(() => {});
  }, [device]);

  if (!device) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError(null);
    try {
      await ApiClient.assignDevice(device.id, adminId || undefined, groupId || undefined);
      // Both lists shift: the device's own row, and the per-team device counts.
      await Promise.all([refreshDevices(), refreshGroups()]);
      onClose();
    } catch (err: any) {
      setError(err?.message ?? 'Failed to update assignment');
    } finally {
      setSaving(false);
    }
  };

  const selectClass =
    'w-full px-3 py-2.5 rounded-xl bg-slate-950/60 border border-slate-800 text-sm text-slate-200 focus:outline-none focus:border-sky-500';

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/80 backdrop-blur-sm animate-in fade-in duration-200">
      <div className="w-full max-w-lg rounded-2xl bg-slate-900 border border-slate-800 shadow-2xl p-6 relative">
        <button
          onClick={onClose}
          className="absolute top-5 right-5 p-2 text-slate-400 hover:text-slate-200 rounded-lg hover:bg-slate-800"
        >
          <X className="w-4 h-4" />
        </button>

        <h3 className="text-lg font-bold text-slate-100 mb-1">Assign endpoint</h3>
        <p className="text-xs text-slate-400 mb-6">
          Who can see <span className="text-slate-200 font-semibold">{device.name}</span>, and which
          team it belongs to.
        </p>

        {error && (
          <div className="mb-5 p-3.5 rounded-xl bg-rose-500/10 border border-rose-500/20 flex items-center gap-3 text-xs text-rose-400">
            <AlertCircle className="w-4 h-4 shrink-0" />
            <span>{error}</span>
          </div>
        )}

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-xs font-semibold text-slate-300 uppercase tracking-wider mb-1.5">
              Assigned Admin
            </label>
            <select value={adminId} onChange={(e) => setAdminId(e.target.value)} className={selectClass}>
              <option value="">Unassigned</option>
              {admins.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.email}
                </option>
              ))}
            </select>
            <p className="text-[11px] text-slate-500 mt-1.5">
              This admin sees the device directly, whatever team it is in.
            </p>
          </div>

          <div>
            <label className="block text-xs font-semibold text-slate-300 uppercase tracking-wider mb-1.5">
              Team
            </label>
            <select value={groupId} onChange={(e) => setGroupId(e.target.value)} className={selectClass}>
              <option value="">No team</option>
              {groups.map((g) => (
                <option key={g.id} value={g.id}>
                  {g.name}
                </option>
              ))}
            </select>
            <p className="text-[11px] text-slate-500 mt-1.5">
              Every admin on the chosen team can also see this device.
            </p>
          </div>

          <div className="pt-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={saving}
              className="flex-1 py-2.5 rounded-xl bg-gradient-to-r from-sky-500 to-blue-600 hover:from-sky-400 hover:to-blue-500 text-white text-xs font-semibold shadow-lg shadow-sky-500/20 transition-all disabled:opacity-50"
            >
              {saving ? 'Saving…' : 'Save assignment'}
            </button>
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2.5 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-300 text-xs font-semibold border border-slate-700/60 transition-colors"
            >
              Cancel
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};
