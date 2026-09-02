import React, { useState, useEffect } from 'react';
import { ApiClient } from '../api/client';
import { User, DeviceGroup } from '../types';
import { X, Copy, Check, Monitor, Smartphone } from 'lucide-react';

interface EnrollModalProps {
  isOpen: boolean;
  onClose: () => void;
  onEnrolled: () => void;
}

export const EnrollDeviceModal: React.FC<EnrollModalProps> = ({ isOpen, onClose, onEnrolled }) => {
  const [deviceType, setDeviceType] = useState<'windows' | 'android'>('windows');
  const [assignedAdminId, setAssignedAdminId] = useState<string>('');
  const [groupId, setGroupId] = useState<string>('');
  const [admins, setAdmins] = useState<User[]>([]);
  const [groups, setGroups] = useState<DeviceGroup[]>([]);
  const [token, setToken] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (isOpen) {
      setToken(null);
      setCopied(false);
      ApiClient.listUsers().then(users => setAdmins(users.filter(u => u.role === 'admin'))).catch(() => {});
      ApiClient.listGroups().then(setGroups).catch(() => {});
    }
  }, [isOpen]);

  if (!isOpen) return null;

  const handleGenerate = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    try {
      const res = await ApiClient.generateEnrollmentToken(
        deviceType,
        assignedAdminId || undefined,
        groupId || undefined
      );
      setToken(res.enrollment_token);
      onEnrolled();
    } catch (err: any) {
      alert('Failed to generate token: ' + err.message);
    } finally {
      setLoading(false);
    }
  };

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/80 backdrop-blur-sm animate-in fade-in duration-200">
      <div className="w-full max-w-lg rounded-2xl bg-slate-900 border border-slate-800 shadow-2xl p-6 relative">
        <button
          onClick={onClose}
          className="absolute top-5 right-5 p-2 text-slate-400 hover:text-slate-200 rounded-lg hover:bg-slate-800"
        >
          <X className="w-4 h-4" />
        </button>

        <h3 className="text-lg font-bold text-slate-100 mb-1">Enroll New Endpoint Agent</h3>
        <p className="text-xs text-slate-400 mb-6">
          Generate an enrollment code to connect a Windows PC or Android smartphone.
        </p>

        {!token ? (
          <form onSubmit={handleGenerate} className="space-y-4">
            {/* Device Type Selection */}
            <div>
              <label className="block text-xs font-semibold text-slate-300 uppercase tracking-wider mb-2">
                Platform Type
              </label>
              <div className="grid grid-cols-2 gap-3">
                <button
                  type="button"
                  onClick={() => setDeviceType('windows')}
                  className={`flex items-center gap-3 p-3.5 rounded-xl border transition-all text-left ${
                    deviceType === 'windows'
                      ? 'border-sky-500 bg-sky-500/10 text-sky-300'
                      : 'border-slate-800 bg-slate-800/40 text-slate-400 hover:border-slate-700'
                  }`}
                >
                  <Monitor className="w-5 h-5 text-sky-400" />
                  <div>
                    <div className="text-sm font-semibold">Windows</div>
                    <div className="text-[11px] opacity-70">Win 10/11 & Server</div>
                  </div>
                </button>

                <button
                  type="button"
                  onClick={() => setDeviceType('android')}
                  className={`flex items-center gap-3 p-3.5 rounded-xl border transition-all text-left ${
                    deviceType === 'android'
                      ? 'border-sky-500 bg-sky-500/10 text-sky-300'
                      : 'border-slate-800 bg-slate-800/40 text-slate-400 hover:border-slate-700'
                  }`}
                >
                  <Smartphone className="w-5 h-5 text-emerald-400" />
                  <div>
                    <div className="text-sm font-semibold">Android</div>
                    <div className="text-[11px] opacity-70">Android 8.0+ APK</div>
                  </div>
                </button>
              </div>
            </div>

            {/* Assigned Admin Selection */}
            <div>
              <label className="block text-xs font-semibold text-slate-300 uppercase tracking-wider mb-1.5">
                Assign to Admin (Optional)
              </label>
              <select
                value={assignedAdminId}
                onChange={(e) => setAssignedAdminId(e.target.value)}
                className="w-full px-3.5 py-2.5 rounded-xl bg-slate-800/80 border border-slate-700 text-sm text-slate-200 focus:outline-none focus:border-sky-500"
              >
                <option value="">Unassigned (Super Admin Only)</option>
                {admins.map((adm) => (
                  <option key={adm.id} value={adm.id}>
                    {adm.email}
                  </option>
                ))}
              </select>
            </div>

            {/* Group Selection */}
            <div>
              <label className="block text-xs font-semibold text-slate-300 uppercase tracking-wider mb-1.5">
                Department / Team Group (Optional)
              </label>
              <select
                value={groupId}
                onChange={(e) => setGroupId(e.target.value)}
                className="w-full px-3.5 py-2.5 rounded-xl bg-slate-800/80 border border-slate-700 text-sm text-slate-200 focus:outline-none focus:border-sky-500"
              >
                <option value="">No Group</option>
                {groups.map((g) => (
                  <option key={g.id} value={g.id}>
                    {g.name}
                  </option>
                ))}
              </select>
            </div>

            <div className="pt-3">
              <button
                type="submit"
                disabled={loading}
                className="w-full py-3 px-4 rounded-xl bg-gradient-to-r from-sky-500 to-blue-600 hover:from-sky-400 hover:to-blue-500 text-white font-semibold text-sm shadow-lg shadow-sky-500/25 transition-all disabled:opacity-50"
              >
                {loading ? 'Generating...' : 'Generate Enrollment Token'}
              </button>
            </div>
          </form>
        ) : (
          <div className="space-y-4">
            <div className="p-4 rounded-xl bg-emerald-500/10 border border-emerald-500/20 text-center">
              <div className="text-xs font-semibold text-emerald-400 uppercase tracking-wider mb-1">
                Enrollment Token
              </div>
              <div className="text-2xl font-mono font-bold text-white tracking-widest my-2">
                {token}
              </div>
              <p className="text-[11px] text-slate-400">
                Reusable — does not expire. It can enroll multiple devices; keep it private.
              </p>
            </div>

            {deviceType === 'windows' ? (
              <div className="space-y-2">
                <label className="block text-xs font-semibold text-slate-300 uppercase tracking-wider">
                  Run this on the Windows device
                </label>
                <div className="flex items-center gap-2 p-3 rounded-xl bg-slate-950 border border-slate-800 font-mono text-xs text-sky-300">
                  <span className="truncate">
                    drs-agent.exe enroll -server {window.location.origin} -token {token}
                  </span>
                  <button
                    onClick={() =>
                      copyToClipboard(
                        `drs-agent.exe enroll -server ${window.location.origin} -token ${token}`,
                      )
                    }
                    className="p-1.5 text-slate-400 hover:text-white rounded bg-slate-800 shrink-0"
                    title="Copy command"
                  >
                    {copied ? <Check className="w-3.5 h-3.5 text-emerald-400" /> : <Copy className="w-3.5 h-3.5" />}
                  </button>
                </div>
                <p className="text-[11px] text-slate-500">
                  Then run <span className="font-mono text-slate-400">drs-agent.exe install</span> to
                  start it automatically at login.
                </p>
              </div>
            ) : (
              <div className="space-y-2">
                <label className="block text-xs font-semibold text-slate-300 uppercase tracking-wider">
                  On the Android device
                </label>
                <ol className="text-[11px] text-slate-400 space-y-1 list-decimal list-inside">
                  <li>Install the DRS Agent APK and open it.</li>
                  <li>
                    Enter the server URL{' '}
                    <span className="font-mono text-slate-300">{window.location.origin}</span>{' '}
                    and the token above, then tap <span className="text-slate-300">Enroll</span>.
                  </li>
                  <li>Approve the screen-capture prompt when an operator starts a session.</li>
                </ol>
                <div className="flex items-center gap-2 p-2.5 rounded-xl bg-slate-950 border border-slate-800 font-mono text-xs text-emerald-300">
                  <span className="truncate">{window.location.origin}</span>
                  <button
                    onClick={() => copyToClipboard(window.location.origin)}
                    className="p-1.5 text-slate-400 hover:text-white rounded bg-slate-800 shrink-0 ml-auto"
                    title="Copy server URL"
                  >
                    {copied ? <Check className="w-3.5 h-3.5 text-emerald-400" /> : <Copy className="w-3.5 h-3.5" />}
                  </button>
                </div>
              </div>
            )}

            <button
              onClick={onClose}
              className="w-full py-2.5 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-200 font-medium text-xs transition-colors"
            >
              Done & Close
            </button>
          </div>
        )}
      </div>
    </div>
  );
};
