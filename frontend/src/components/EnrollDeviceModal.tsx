import React, { useState, useEffect } from 'react';
import { ApiClient } from '../api/client';
import { User } from '../types';
import { useAuth } from '../context/AuthContext';
import { useDevices } from '../context/DeviceContext';
import { X, Copy, Check, Monitor, Smartphone, Download, AlertCircle, Users } from 'lucide-react';
import { useAgentDownload } from '../hooks/useAgentDownload';
import { copyText } from '../utils/clipboard';

interface EnrollModalProps {
  isOpen: boolean;
  onClose: () => void;
  onEnrolled: () => void;
}

// Declared at module scope on purpose. Defined inside the modal it was a fresh component type
// on every render, so React tore the button down and rebuilt it after each keystroke and each
// copy — losing focus and the tick along with it.
const CopyButton: React.FC<{ copied: boolean; onCopy: () => void; title: string }> = ({
  copied,
  onCopy,
  title,
}) => (
  <button
    type="button"
    onClick={onCopy}
    className="p-1.5 text-slate-400 hover:text-white rounded bg-slate-800 shrink-0 ml-auto"
    title={title}
  >
    {copied ? <Check className="w-3.5 h-3.5 text-emerald-400" /> : <Copy className="w-3.5 h-3.5" />}
  </button>
);

export const EnrollDeviceModal: React.FC<EnrollModalProps> = ({ isOpen, onClose, onEnrolled }) => {
  const { isSuperAdmin, user } = useAuth();
  // The context already holds an RBAC-scoped team list and is mounted above this modal,
  // so there is no reason for a second fetch. For an Admin it contains only their teams,
  // which is exactly what the server will accept.
  const { groups } = useDevices();

  const [deviceType, setDeviceType] = useState<'windows' | 'android'>('windows');
  const [assignedAdminId, setAssignedAdminId] = useState<string>('');
  const [groupId, setGroupId] = useState<string>('');
  const [label, setLabel] = useState<string>('');
  const [admins, setAdmins] = useState<User[]>([]);
  const [token, setToken] = useState<string | null>(null);
  // Keyed by what was copied, not a single boolean: one flag made copying any button
  // tick every button, which reads as "I copied the wrong thing".
  const [copiedKey, setCopiedKey] = useState<string | null>(null);
  // A copy can genuinely fail (no clipboard permission, and no execCommand either). Saying
  // so beats a tick that lies — the admin is about to paste this into a chat window.
  const [copyFailed, setCopyFailed] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Whether this deployment has an agent staged. It decides whether the invite link is
  // self-contained or the admin still has to send the binary separately, which is the
  // difference between a one-step and a two-step handover — so say which, rather than
  // leaving the admin to find out from the recipient.
  const windowsAgent = useAgentDownload('windows', token ?? undefined);
  const androidAgent = useAgentDownload('android', token ?? undefined);

  useEffect(() => {
    if (!isOpen) return;
    setToken(null);
    setCopiedKey(null);
    setCopyFailed(false);
    setError(null);
    // /api/users is Super Admin only — asking as an Admin would 403 and leave a
    // mysteriously empty dropdown, so only the picker's actual audience fetches it.
    if (isSuperAdmin) {
      ApiClient.listUsers()
        .then((users) => setAdmins(users.filter((u) => u.role === 'admin')))
        .catch(() => {});
    }
  }, [isOpen, isSuperAdmin]);

  if (!isOpen) return null;

  const handleGenerate = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError(null);
    try {
      const res = await ApiClient.generateEnrollmentToken(
        deviceType,
        assignedAdminId || undefined,
        groupId || undefined,
        label.trim() || undefined,
      );
      setToken(res.enrollment_token);
      onEnrolled();
    } catch (err: any) {
      setError(err?.message ?? 'Failed to generate the invite link.');
    } finally {
      setLoading(false);
    }
  };

  // The link the recipient opens. It carries the token; the download behind it carries
  // the server address too, baked into the binary.
  const inviteLink = token ? `${window.location.origin}/enroll?token=${token}` : '';
  const selectedGroup = groups.find((g) => g.id === groupId);

  const copyToClipboard = async (key: string, text: string) => {
    const ok = await copyText(text);
    setCopyFailed(!ok);
    if (!ok) return;
    setCopiedKey(key);
    setTimeout(() => setCopiedKey((k) => (k === key ? null : k)), 2000);
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

        <h3 className="text-lg font-bold text-slate-100 mb-1">Create an invite link</h3>
        <p className="text-xs text-slate-400 mb-6">
          One link, reusable for as many devices as you like. The agent it hands out is
          already configured — whoever runs it types nothing.
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

            {/* Label — so an outstanding link is identifiable later, when the only
                other things distinguishing it are a UUID and a timestamp. */}
            <div>
              <label className="block text-xs font-semibold text-slate-300 uppercase tracking-wider mb-1.5">
                Name this link (Optional)
              </label>
              <input
                value={label}
                onChange={(e) => setLabel(e.target.value)}
                maxLength={120}
                placeholder="Finance rollout"
                className="w-full px-3.5 py-2.5 rounded-xl bg-slate-800/80 border border-slate-700 text-sm text-slate-200 placeholder:text-slate-600 focus:outline-none focus:border-sky-500"
              />
            </div>

            {/* Assigned admin. Only a Super Admin picks: an Admin's link is pinned to
                them server-side, so offering a choice here would be a lie. */}
            {isSuperAdmin ? (
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
            ) : (
              <div className="flex items-start gap-2.5 p-3 rounded-xl bg-slate-800/40 border border-slate-700/60">
                <Users className="w-3.5 h-3.5 text-sky-400 mt-0.5 shrink-0" />
                <p className="text-[11px] text-slate-400 leading-relaxed">
                  Devices enrolled with this link are assigned to you
                  {user?.email ? <span className="text-slate-300"> ({user.email})</span> : null}, so
                  they appear in your panel.
                </p>
              </div>
            )}

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

            {error && (
              <div className="p-3 rounded-xl bg-rose-500/10 border border-rose-500/30 flex items-start gap-2.5">
                <AlertCircle className="w-3.5 h-3.5 text-rose-400 mt-0.5 shrink-0" />
                <p className="text-[11px] text-rose-300 leading-relaxed">{error}</p>
              </div>
            )}

            <div className="pt-3">
              <button
                type="submit"
                disabled={loading}
                className="w-full py-3 px-4 rounded-xl bg-gradient-to-r from-sky-500 to-blue-600 hover:from-sky-400 hover:to-blue-500 text-white font-semibold text-sm shadow-lg shadow-sky-500/25 transition-all disabled:opacity-50"
              >
                {loading ? 'Generating...' : 'Create invite link'}
              </button>
            </div>
          </form>
        ) : (
          <div className="space-y-4">
            {/* The link is the deliverable now, so it leads. The raw token is kept below
                for the CLI path, but nobody needs to read it to use the link. */}
            <div className="p-4 rounded-xl bg-emerald-500/10 border border-emerald-500/20">
              <div className="text-xs font-semibold text-emerald-400 uppercase tracking-wider mb-2">
                Send this link
              </div>
              <div className="flex items-center gap-2 p-3 rounded-xl bg-slate-950 border border-slate-800 font-mono text-xs text-sky-300">
                <span className="truncate">{inviteLink}</span>
                <CopyButton
                  copied={copiedKey === 'link'}
                  onCopy={() => copyToClipboard('link', inviteLink)}
                  title="Copy invite link"
                />
              </div>
              <p className="text-[11px] text-slate-400 mt-2 leading-relaxed">
                Works for <span className="text-slate-300">any number of devices</span>, on
                Windows and Android alike
                {selectedGroup ? (
                  <>
                    . Everything enrolled with it joins{' '}
                    <span className="text-slate-300">{selectedGroup.name}</span>
                  </>
                ) : null}
                .
              </p>
            </div>

            {copyFailed && (
              <div className="p-2.5 rounded-xl bg-rose-500/10 border border-rose-500/30 flex items-start gap-2.5">
                <AlertCircle className="w-3.5 h-3.5 text-rose-400 mt-0.5 shrink-0" />
                <p className="text-[11px] text-rose-300 leading-relaxed">
                  The browser blocked the copy. Select the text and copy it by hand — or open
                  this panel over HTTPS, which is what the clipboard API requires.
                </p>
              </div>
            )}

            <div className="p-3 rounded-xl bg-slate-800/40 border border-slate-700/60">
              <p className="text-[11px] text-slate-400 leading-relaxed">
                <span className="text-slate-200 font-semibold">What the recipient does:</span>{' '}
                opens the link, downloads the agent, runs it. Nothing to type — the download
                already knows this server and this invite. On Android they tap{' '}
                <span className="text-slate-300">Set up agent</span> after installing.
              </p>
            </div>

            {/* Nothing staged means the link still carries the token, but the recipient
                has no software to use it with — say so here rather than let them find
                out from the recipient. */}
            {windowsAgent.available === false && androidAgent.available === false && (
              <div className="p-2.5 rounded-xl bg-amber-500/[0.06] border border-amber-500/20 flex items-start gap-2.5">
                <AlertCircle className="w-3.5 h-3.5 text-amber-400 mt-0.5 shrink-0" />
                <p className="text-[11px] text-slate-400 leading-relaxed">
                  No agent is published on this server, so the link offers no download and
                  the recipient will have to configure the agent by hand. Stage one by
                  uploading it to <span className="font-mono text-slate-300">deploy/downloads/</span>{' '}
                  and setting <span className="font-mono text-slate-300">AGENT_BINARY_DIR</span>.
                </p>
              </div>
            )}

            {windowsAgent.available === true && (
              <a
                href={windowsAgent.url}
                className="flex items-center gap-2.5 p-2.5 rounded-xl bg-slate-800/50 hover:bg-slate-800 border border-slate-700/60 text-[11px] text-slate-400 hover:text-slate-200 transition-colors"
              >
                <Download className="w-3.5 h-3.5 text-sky-400 shrink-0" />
                <span className="flex-1">Download the configured Windows agent yourself</span>
              </a>
            )}

            <details className="text-[11px] text-slate-500">
              <summary className="cursor-pointer text-slate-400 hover:text-slate-200">
                Enrollment code, and the command-line route
              </summary>
              <div className="space-y-2 mt-2">
                <div className="flex items-center gap-2 p-2.5 rounded-xl bg-slate-950 border border-slate-800 font-mono text-xs text-emerald-300">
                  <span className="truncate">{token}</span>
                  <CopyButton
                    copied={copiedKey === 'token'}
                    onCopy={() => copyToClipboard('token', token)}
                    title="Copy code"
                  />
                </div>
                <p className="leading-relaxed">
                  Only needed for an agent that was not downloaded through this link — an
                  unconfigured binary, or one already installed.
                </p>
                <div className="flex items-center gap-2 p-2.5 rounded-xl bg-slate-950 border border-slate-800 font-mono text-xs text-slate-300">
                  <span className="truncate">
                    drs-agent.exe enroll -server {window.location.origin} -token {token}
                  </span>
                  <CopyButton
                    copied={copiedKey === 'cli'}
                    onCopy={() =>
                      copyToClipboard(
                        'cli',
                        `drs-agent.exe enroll -server ${window.location.origin} -token ${token}`,
                      )
                    }
                    title="Copy command"
                  />
                </div>
              </div>
            </details>

            <div className="p-2.5 rounded-xl bg-amber-500/[0.06] border border-amber-500/20 flex items-start gap-2.5">
              <AlertCircle className="w-3.5 h-3.5 text-amber-400 mt-0.5 shrink-0" />
              <p className="text-[11px] text-slate-400 leading-relaxed">
                The download is a self-enrolling installer: anyone who runs it joins this
                organization and it starts with their machine. Treat the link like a
                password, and revoke it from{' '}
                <span className="text-slate-300">Invite links</span> once the rollout is done.
              </p>
            </div>

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
