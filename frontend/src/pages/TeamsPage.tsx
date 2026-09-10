import React, { useState } from 'react';
import { Link } from 'react-router-dom';
import { useDevices } from '../context/DeviceContext';
import { useAuth } from '../context/AuthContext';
import { ApiClient } from '../api/client';
import { DeviceGroup } from '../types';
import {
  FolderKanban,
  Plus,
  Monitor,
  Users,
  Pencil,
  Trash2,
  X,
  AlertCircle,
  ChevronRight,
  LayoutGrid,
} from 'lucide-react';

/**
 * Teams & Groups.
 *
 * This page did not exist: the sidebar had an entry for it and App.tsx had no branch, so
 * clicking it rendered an empty content area. The data layer was all already here and
 * unused — device_groups, GET/POST /api/groups, ApiClient.createGroup, and the groups
 * DeviceContext has been fetching on login and handing to nobody.
 *
 * A team is not a caption. Adding an admin to one grants them every device in it, so the
 * copy on this page says so wherever a team is edited.
 */
export const TeamsPage: React.FC = () => {
  const { groups, refreshGroups, loading } = useDevices();
  const { isSuperAdmin } = useAuth();

  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<DeviceGroup | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);

  const handleDelete = async (group: DeviceGroup) => {
    const devicesNote =
      group.device_count > 0
        ? `\n\nIts ${group.device_count} device(s) will remain enrolled but become ungrouped.`
        : '';
    const membersNote =
      group.member_count > 0
        ? `\n${group.member_count} admin(s) will lose access to those devices unless they are individually assigned.`
        : '';
    if (!window.confirm(`Delete the team "${group.name}"?${devicesNote}${membersNote}`)) return;

    setDeletingId(group.id);
    try {
      await ApiClient.deleteGroup(group.id);
      await refreshGroups();
    } catch (e: any) {
      window.alert(`Failed to delete team: ${e?.message ?? e}`);
    } finally {
      setDeletingId(null);
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Teams &amp; Groups</h1>
          <p className="text-xs text-slate-400 mt-0.5">
            {isSuperAdmin
              ? 'Group endpoints by department, client or site. Admins added to a team can see every device in it.'
              : 'The teams you belong to. Every device in these teams is visible to you.'}
          </p>
        </div>

        {isSuperAdmin && (
          <button
            onClick={() => setCreating(true)}
            className="flex items-center gap-2 px-4 py-2.5 rounded-xl bg-gradient-to-r from-sky-500 to-blue-600 hover:from-sky-400 hover:to-blue-500 text-white text-xs font-semibold shadow-lg shadow-sky-500/20 transition-all"
          >
            <Plus className="w-4 h-4" />
            <span>New Team</span>
          </button>
        )}
      </div>

      {loading ? (
        <div className="p-16 flex flex-col items-center justify-center text-slate-400">
          <div className="w-8 h-8 rounded-full border-2 border-sky-500 border-t-transparent animate-spin mb-3"></div>
          <span className="text-xs font-mono">Loading teams...</span>
        </div>
      ) : groups.length === 0 ? (
        <div className="p-12 text-center rounded-2xl bg-slate-900/20 border border-slate-800/60">
          <FolderKanban className="w-10 h-10 text-slate-600 mx-auto mb-3" />
          <h3 className="text-sm font-semibold text-slate-300">No teams yet</h3>
          <p className="text-xs text-slate-500 mt-1.5 max-w-md mx-auto leading-relaxed">
            {isSuperAdmin
              ? 'A team groups endpoints — a department, a client, a site — and controls who can reach them. ' +
                'Add an admin to a team and they can see every device in it, without you assigning each machine by hand.'
              : 'You have not been added to any team. You can still see the endpoints assigned to you directly.'}
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {groups.map((group) => (
            <div
              key={group.id}
              className="rounded-2xl bg-slate-900/60 border border-slate-800 hover:border-slate-700/80 p-5 transition-all flex flex-col justify-between space-y-4"
            >
              <div>
                <div className="flex items-start justify-between gap-3 mb-3">
                  <div className="flex items-center gap-3 min-w-0">
                    <div className="w-10 h-10 rounded-xl bg-slate-800 border border-slate-700/60 flex items-center justify-center shrink-0">
                      <FolderKanban className="w-5 h-5 text-sky-400" />
                    </div>
                    <div className="min-w-0">
                      <h4 className="text-sm font-semibold text-slate-100 truncate">{group.name}</h4>
                      <div className="text-[11px] text-slate-500">
                        Created {new Date(group.created_at).toLocaleDateString()}
                      </div>
                    </div>
                  </div>
                </div>

                <p className="text-xs text-slate-400 leading-relaxed min-h-[2rem]">
                  {group.description || <span className="text-slate-600 italic">No description</span>}
                </p>

                <div className="grid grid-cols-2 gap-3 pt-3 mt-1 border-t border-slate-800/80">
                  <div className="flex items-center gap-2">
                    <Monitor className="w-3.5 h-3.5 text-slate-500" />
                    <span className="text-xs text-slate-300">
                      <span className="font-semibold">{group.device_count}</span>{' '}
                      <span className="text-slate-500">device{group.device_count === 1 ? '' : 's'}</span>
                    </span>
                  </div>
                  <div className="flex items-center gap-2">
                    <Users className="w-3.5 h-3.5 text-slate-500" />
                    <span className="text-xs text-slate-300">
                      <span className="font-semibold">{group.member_count}</span>{' '}
                      <span className="text-slate-500">admin{group.member_count === 1 ? '' : 's'}</span>
                    </span>
                  </div>
                </div>
              </div>

              <div className="pt-3 border-t border-slate-800/80 flex items-center gap-2">
                <Link
                  to={`/teams/${group.id}`}
                  className="flex-1 py-2 px-3 rounded-xl bg-sky-500/10 hover:bg-sky-500/20 text-sky-400 border border-sky-500/30 text-xs font-semibold flex items-center justify-center gap-1.5 transition-all"
                >
                  <span>{isSuperAdmin ? 'Manage' : 'View'}</span>
                  <ChevronRight className="w-3.5 h-3.5" />
                </Link>

                {group.device_count > 0 && (
                  <Link
                    to={`/teams/${group.id}/monitor`}
                    title="Open the monitoring wall for this team"
                    className="py-2 px-3 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-300 border border-slate-700/60 transition-colors"
                  >
                    <LayoutGrid className="w-3.5 h-3.5" />
                  </Link>
                )}

                {isSuperAdmin && (
                  <>
                    <button
                      onClick={() => setEditing(group)}
                      title="Rename team"
                      className="py-2 px-3 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-300 border border-slate-700/60 transition-colors"
                    >
                      <Pencil className="w-3.5 h-3.5" />
                    </button>
                    <button
                      disabled={deletingId === group.id}
                      onClick={() => handleDelete(group)}
                      title="Delete team"
                      className="py-2 px-3 rounded-xl bg-rose-500/10 hover:bg-rose-500/20 text-rose-400 border border-rose-500/30 transition-all disabled:opacity-40 disabled:pointer-events-none"
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                    </button>
                  </>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      {(creating || editing) && (
        <TeamFormModal
          group={editing}
          onClose={() => {
            setCreating(false);
            setEditing(null);
          }}
          onSaved={refreshGroups}
        />
      )}
    </div>
  );
};

/** Create and rename share a form; the only difference is which call it makes. */
const TeamFormModal: React.FC<{
  group: DeviceGroup | null;
  onClose: () => void;
  onSaved: () => Promise<void>;
}> = ({ group, onClose, onSaved }) => {
  const [name, setName] = useState(group?.name ?? '');
  const [description, setDescription] = useState(group?.description ?? '');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError(null);
    try {
      if (group) {
        await ApiClient.updateGroup(group.id, { name, description });
      } else {
        await ApiClient.createGroup(name, description);
      }
      await onSaved();
      onClose();
    } catch (err: any) {
      setError(err?.message ?? 'Failed to save team');
    } finally {
      setSaving(false);
    }
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

        <h3 className="text-lg font-bold text-slate-100 mb-1">
          {group ? 'Rename team' : 'New team'}
        </h3>
        <p className="text-xs text-slate-400 mb-6">
          {group
            ? 'Devices and members are unaffected by a rename.'
            : 'Name it after the department, client or site it covers.'}
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
              Team Name
            </label>
            <input
              type="text"
              required
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Finance"
              className="w-full px-3 py-2.5 rounded-xl bg-slate-950/60 border border-slate-800 text-sm text-slate-100 placeholder:text-slate-600 focus:outline-none focus:border-sky-500"
            />
          </div>

          <div>
            <label className="block text-xs font-semibold text-slate-300 uppercase tracking-wider mb-1.5">
              Description
            </label>
            <textarea
              rows={3}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Head office finance workstations"
              className="w-full px-3 py-2.5 rounded-xl bg-slate-950/60 border border-slate-800 text-sm text-slate-100 placeholder:text-slate-600 focus:outline-none focus:border-sky-500 resize-none"
            />
          </div>

          <div className="pt-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={saving || !name.trim()}
              className="flex-1 py-2.5 rounded-xl bg-gradient-to-r from-sky-500 to-blue-600 hover:from-sky-400 hover:to-blue-500 text-white text-xs font-semibold shadow-lg shadow-sky-500/20 transition-all disabled:opacity-50"
            >
              {saving ? 'Saving…' : group ? 'Save changes' : 'Create team'}
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
