import React, { useCallback, useEffect, useState } from 'react';
import { ApiClient } from '../api/client';
import { EnrollmentTokenSummary } from '../types';
import { useAuth } from '../context/AuthContext';
import { EnrollDeviceModal } from '../components/EnrollDeviceModal';
import {
  AlertCircle, Ban, Link2, Monitor, Plus, RefreshCw, Smartphone,
} from 'lucide-react';

function formatDate(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString();
}

/**
 * Outstanding invite links, and the off switch for them.
 *
 * This page exists because of what an invite link became. It used to be a code somebody
 * typed into an agent; it is now a download that enrolls the machine running it and
 * registers itself to start at login. That is a credential in executable form, and one
 * that gets forwarded — so being able to see what is outstanding, and kill it, is part
 * of the feature rather than an extra.
 *
 * Scoped server-side: an Admin sees the links they created, a Super Admin the org's.
 */
export const InviteLinksPage: React.FC = () => {
  const { isSuperAdmin } = useAuth();
  const [tokens, setTokens] = useState<EnrollmentTokenSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const load = useCallback(async () => {
    try {
      setTokens(await ApiClient.listEnrollmentTokens());
      setError(null);
    } catch (e: any) {
      setError(e?.message ?? 'Failed to load invite links');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const revoke = async (t: EnrollmentTokenSummary) => {
    const name = t.label || 'this invite link';
    if (
      !window.confirm(
        `Revoke ${name}?\n\n` +
          'It will stop enrolling new devices immediately, and any installer already ' +
          'downloaded from it stops working.\n\n' +
          `The ${t.device_count} device${t.device_count === 1 ? '' : 's'} it has already ` +
          'enrolled keep working — delete those individually if you want them gone.',
      )
    ) {
      return;
    }
    setBusyId(t.id);
    try {
      await ApiClient.revokeEnrollmentToken(t.id);
      await load();
    } catch (e: any) {
      window.alert(`Failed to revoke: ${e?.message ?? e}`);
    } finally {
      setBusyId(null);
    }
  };

  const active = tokens.filter((t) => !t.revoked_at);

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Invite links</h1>
          <p className="text-xs text-slate-400 mt-0.5">
            {active.length} active · a link hands out an agent that enrolls itself, so
            revoke one when its rollout is finished
          </p>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <button
            onClick={() => void load()}
            title="Refresh"
            className="p-2.5 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-300 border border-slate-700/60 transition-colors"
          >
            <RefreshCw className="w-4 h-4" />
          </button>
          <button
            onClick={() => setCreating(true)}
            className="flex items-center gap-2 px-4 py-2.5 rounded-xl bg-gradient-to-r from-sky-500 to-blue-600 hover:from-sky-400 hover:to-blue-500 text-white text-xs font-semibold shadow-lg shadow-sky-500/20 transition-all"
          >
            <Plus className="w-4 h-4" />
            <span>New invite link</span>
          </button>
        </div>
      </div>

      {error && (
        <div className="p-3 rounded-xl bg-rose-500/10 border border-rose-500/30 flex items-start gap-2.5">
          <AlertCircle className="w-4 h-4 text-rose-400 mt-0.5 shrink-0" />
          <p className="text-xs text-rose-300">{error}</p>
        </div>
      )}

      {loading ? (
        <div className="p-12 flex flex-col items-center gap-3 rounded-2xl bg-slate-900/20 border border-slate-800/60">
          <div className="w-8 h-8 rounded-full border-2 border-sky-500 border-t-transparent animate-spin" />
          <span className="text-xs font-mono text-slate-500">Loading invite links…</span>
        </div>
      ) : tokens.length === 0 ? (
        <div className="p-12 text-center rounded-2xl bg-slate-900/20 border border-slate-800/60">
          <Link2 className="w-10 h-10 text-slate-700 mx-auto mb-3" />
          <p className="text-sm text-slate-300 font-medium">No invite links yet</p>
          <p className="text-xs text-slate-500 mt-1">
            Create one and send it to the machines you want to enroll. The same link works
            for as many as you like.
          </p>
        </div>
      ) : (
        <div className="rounded-2xl bg-slate-900/40 border border-slate-800 overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full text-left text-xs">
              <thead className="bg-slate-950/60 text-slate-400 uppercase tracking-wider text-[10px] font-semibold">
                <tr>
                  <th className="px-5 py-3">Link</th>
                  <th className="px-5 py-3">Team</th>
                  {isSuperAdmin && <th className="px-5 py-3">Created by</th>}
                  <th className="px-5 py-3">Created</th>
                  <th className="px-5 py-3">Devices</th>
                  <th className="px-5 py-3">Status</th>
                  <th className="px-5 py-3"></th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-800/60">
                {tokens.map((t) => {
                  const PlatformIcon = t.device_type === 'android' ? Smartphone : Monitor;
                  const revoked = Boolean(t.revoked_at);
                  return (
                    <tr
                      key={t.id}
                      className={`hover:bg-slate-800/30 ${revoked ? 'opacity-50' : ''}`}
                    >
                      <td className="px-5 py-3">
                        <div className="flex items-center gap-2">
                          <PlatformIcon className="w-3.5 h-3.5 text-slate-500 shrink-0" />
                          <span className="text-slate-200 font-medium">
                            {t.label || 'Untitled link'}
                          </span>
                        </div>
                      </td>
                      <td className="px-5 py-3 text-slate-400">{t.group_name || '—'}</td>
                      {isSuperAdmin && (
                        <td className="px-5 py-3 text-slate-400">{t.created_by_email || '—'}</td>
                      )}
                      <td className="px-5 py-3 text-slate-500">{formatDate(t.created_at)}</td>
                      <td className="px-5 py-3 text-slate-300 font-mono">{t.device_count}</td>
                      <td className="px-5 py-3">
                        {revoked ? (
                          <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-[10px] font-medium bg-slate-800 text-slate-400 border border-slate-700/50">
                            Revoked
                          </span>
                        ) : (
                          <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-[10px] font-medium bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">
                            <span className="w-1.5 h-1.5 rounded-full bg-emerald-400" />
                            Active
                          </span>
                        )}
                      </td>
                      <td className="px-5 py-3 text-right">
                        {!revoked && (
                          <button
                            onClick={() => void revoke(t)}
                            disabled={busyId === t.id}
                            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-rose-500/10 hover:bg-rose-500/20 text-rose-400 border border-rose-500/30 text-[11px] font-semibold transition-all disabled:opacity-40"
                          >
                            <Ban className="w-3 h-3" />
                            Revoke
                          </button>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}

      <p className="text-[11px] text-slate-500 leading-relaxed">
        Revoking stops a link enrolling anything new, and kills any installer already
        downloaded from it. Devices it enrolled earlier keep working — they hold their own
        credentials, so removing one is a separate decision.
      </p>

      <EnrollDeviceModal
        isOpen={creating}
        onClose={() => {
          setCreating(false);
          void load();
        }}
        onEnrolled={() => void load()}
      />
    </div>
  );
};
