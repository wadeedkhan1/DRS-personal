import React, { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { useDevices } from '../context/DeviceContext';
import { useAuth } from '../context/AuthContext';
import { ApiClient } from '../api/client';
import { GroupMember, User } from '../types';
import { StatusBadge } from '../components/StatusBadge';
import {
  ArrowLeft,
  Monitor,
  Smartphone,
  Users,
  UserPlus,
  UserMinus,
  Plus,
  X,
  ShieldCheck,
  AlertCircle,
  Play,
  LayoutGrid,
} from 'lucide-react';

/**
 * One team: the devices in it, and the admins who can therefore see them.
 *
 * The device panel is filtered out of the device list the context already holds rather
 * than fetched per team — that list is already scoped by the caller's RBAC, so an Admin
 * viewing their own team cannot see more here than anywhere else.
 */
export const TeamDetailPage: React.FC = () => {
  const { groupId } = useParams<{ groupId: string }>();
  const navigate = useNavigate();
  const { devices, groups, refreshDevices, refreshGroups } = useDevices();
  const { isSuperAdmin } = useAuth();

  const [members, setMembers] = useState<GroupMember[]>([]);
  const [membersError, setMembersError] = useState<string | null>(null);
  const [addingMember, setAddingMember] = useState(false);
  const [pickingDevice, setPickingDevice] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);

  const group = groups.find((g) => g.id === groupId);
  const teamDevices = devices.filter((d) => d.group_id === groupId);

  const loadMembers = useCallback(async () => {
    if (!groupId) return;
    try {
      setMembers(await ApiClient.listGroupMembers(groupId));
      setMembersError(null);
    } catch (e: any) {
      setMembersError(e?.message ?? 'Failed to load members');
    }
  }, [groupId]);

  useEffect(() => {
    void loadMembers();
  }, [loadMembers]);

  // groups arrives with the context, so a cold load on this URL has no group for a tick.
  // Only call it missing once the list has actually loaded and still lacks it.
  if (!group) {
    if (groups.length === 0) {
      return (
        <div className="p-16 flex flex-col items-center justify-center text-slate-400">
          <div className="w-8 h-8 rounded-full border-2 border-sky-500 border-t-transparent animate-spin mb-3"></div>
          <span className="text-xs font-mono">Loading team...</span>
        </div>
      );
    }
    return (
      <div className="space-y-6">
        <Link to="/teams" className="flex items-center gap-2 text-xs text-slate-400 hover:text-slate-200">
          <ArrowLeft className="w-3.5 h-3.5" />
          <span>Back to teams</span>
        </Link>
        <div className="p-12 text-center rounded-2xl bg-slate-900/30 border border-slate-800 flex flex-col items-center">
          <AlertCircle className="w-10 h-10 text-rose-400/70 mb-3" />
          <h3 className="text-sm font-semibold text-slate-200">Team not found</h3>
          <p className="text-xs text-slate-500 mt-1">
            It may have been deleted, or you may not be a member of it.
          </p>
        </div>
      </div>
    );
  }

  const removeDeviceFromTeam = async (deviceId: string, adminId?: string | null) => {
    setBusyId(deviceId);
    try {
      // The admin assignment is passed back through unchanged: assign replaces both
      // fields, so omitting it here would silently unassign the device's admin as a side
      // effect of taking it out of a team.
      await ApiClient.assignDevice(deviceId, adminId ?? undefined, undefined);
      await Promise.all([refreshDevices(), refreshGroups()]);
    } catch (e: any) {
      window.alert(`Failed to remove device: ${e?.message ?? e}`);
    } finally {
      setBusyId(null);
    }
  };

  const removeMember = async (member: GroupMember) => {
    if (
      !window.confirm(
        `Remove ${member.email} from "${group.name}"?\n\n` +
          `They will lose access to the ${teamDevices.length} device(s) in this team, ` +
          `except any assigned to them individually.`,
      )
    ) {
      return;
    }
    setBusyId(member.user_id);
    try {
      await ApiClient.removeGroupMember(group.id, member.user_id);
      await Promise.all([loadMembers(), refreshGroups()]);
    } catch (e: any) {
      window.alert(`Failed to remove member: ${e?.message ?? e}`);
    } finally {
      setBusyId(null);
    }
  };

  return (
    <div className="space-y-6">
      <Link to="/teams" className="inline-flex items-center gap-2 text-xs text-slate-400 hover:text-slate-200">
        <ArrowLeft className="w-3.5 h-3.5" />
        <span>Back to teams</span>
      </Link>

      <div>
        <h1 className="text-2xl font-bold text-slate-100">{group.name}</h1>
        <p className="text-xs text-slate-400 mt-0.5">
          {group.description || 'No description'}
        </p>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 items-start">
        {/* Devices */}
        <section className="rounded-2xl bg-slate-900/40 border border-slate-800 overflow-hidden">
          <header className="px-5 py-4 border-b border-slate-800 flex items-center justify-between gap-3">
            <div className="flex items-center gap-2.5">
              <Monitor className="w-4 h-4 text-sky-400" />
              <h2 className="text-sm font-semibold text-slate-100">
                Devices <span className="text-slate-500 font-normal">({teamDevices.length})</span>
              </h2>
            </div>
            <div className="flex items-center gap-2">
              {teamDevices.length > 0 && (
                <Link
                  to={`/teams/${group.id}/monitor`}
                  className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-sky-500/10 hover:bg-sky-500/20 text-sky-400 text-[11px] font-semibold border border-sky-500/30 transition-colors"
                >
                  <LayoutGrid className="w-3.5 h-3.5" />
                  <span>Monitor all</span>
                </Link>
              )}
              {isSuperAdmin && (
                <button
                  onClick={() => setPickingDevice(true)}
                  className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-slate-800 hover:bg-slate-700 text-slate-200 text-[11px] font-semibold border border-slate-700/60 transition-colors"
                >
                  <Plus className="w-3.5 h-3.5" />
                  <span>Add device</span>
                </button>
              )}
            </div>
          </header>

          {teamDevices.length === 0 ? (
            <p className="px-5 py-10 text-center text-xs text-slate-500">
              No devices in this team yet.
            </p>
          ) : (
            <ul className="divide-y divide-slate-800/80">
              {teamDevices.map((device) => (
                <li key={device.id} className="px-5 py-3.5 flex items-center justify-between gap-3">
                  <div className="flex items-center gap-3 min-w-0">
                    {device.type === 'windows' ? (
                      <Monitor className="w-4 h-4 text-sky-400 shrink-0" />
                    ) : (
                      <Smartphone className="w-4 h-4 text-emerald-400 shrink-0" />
                    )}
                    <div className="min-w-0">
                      <div className="text-xs font-semibold text-slate-100 truncate">{device.name}</div>
                      <div className="text-[11px] text-slate-500 truncate">
                        {device.assigned_admin_email || 'No individual admin'}
                      </div>
                    </div>
                  </div>

                  <div className="flex items-center gap-2 shrink-0">
                    <StatusBadge status={device.status} />
                    {(device.status === 'online' || device.status === 'in_session') && (
                      <button
                        onClick={() => navigate(`/devices/${device.id}/live`)}
                        title="Live view"
                        className="p-1.5 rounded-lg bg-sky-500/10 hover:bg-sky-500/20 text-sky-400 border border-sky-500/30 transition-all"
                      >
                        <Play className="w-3 h-3 fill-current" />
                      </button>
                    )}
                    {isSuperAdmin && (
                      <button
                        disabled={busyId === device.id}
                        onClick={() => removeDeviceFromTeam(device.id, device.assigned_admin_id)}
                        title="Remove from team"
                        className="p-1.5 rounded-lg bg-slate-800 hover:bg-rose-500/20 text-slate-400 hover:text-rose-400 border border-slate-700/60 transition-all disabled:opacity-40"
                      >
                        <X className="w-3 h-3" />
                      </button>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          )}
        </section>

        {/* Members */}
        <section className="rounded-2xl bg-slate-900/40 border border-slate-800 overflow-hidden">
          <header className="px-5 py-4 border-b border-slate-800 flex items-center justify-between gap-3">
            <div className="flex items-center gap-2.5">
              <Users className="w-4 h-4 text-emerald-400" />
              <h2 className="text-sm font-semibold text-slate-100">
                Admins <span className="text-slate-500 font-normal">({members.length})</span>
              </h2>
            </div>
            {isSuperAdmin && (
              <button
                onClick={() => setAddingMember(true)}
                className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-slate-800 hover:bg-slate-700 text-slate-200 text-[11px] font-semibold border border-slate-700/60 transition-colors"
              >
                <UserPlus className="w-3.5 h-3.5" />
                <span>Add admin</span>
              </button>
            )}
          </header>

          <div className="px-5 py-3 bg-sky-500/[0.04] border-b border-slate-800/80 flex items-start gap-2.5">
            <ShieldCheck className="w-3.5 h-3.5 text-sky-400 mt-0.5 shrink-0" />
            <p className="text-[11px] text-slate-400 leading-relaxed">
              An admin on this team can see and open a session on every device in it, whether or not
              the device is individually assigned to them.
            </p>
          </div>

          {membersError ? (
            <p className="px-5 py-10 text-center text-xs text-rose-400">{membersError}</p>
          ) : members.length === 0 ? (
            <p className="px-5 py-10 text-center text-xs text-slate-500">
              No admins on this team. Its devices are visible only to whoever they are individually
              assigned to.
            </p>
          ) : (
            <ul className="divide-y divide-slate-800/80">
              {members.map((member) => (
                <li key={member.user_id} className="px-5 py-3.5 flex items-center justify-between gap-3">
                  <div className="min-w-0">
                    <div className="text-xs font-semibold text-slate-100 truncate">{member.email}</div>
                    <div className="text-[11px] text-slate-500">
                      {member.role === 'super_admin' ? 'Super Admin' : 'Admin'} · added{' '}
                      {new Date(member.created_at).toLocaleDateString()}
                    </div>
                  </div>
                  {isSuperAdmin && (
                    <button
                      disabled={busyId === member.user_id}
                      onClick={() => removeMember(member)}
                      title="Remove from team"
                      className="p-1.5 rounded-lg bg-slate-800 hover:bg-rose-500/20 text-slate-400 hover:text-rose-400 border border-slate-700/60 transition-all disabled:opacity-40 shrink-0"
                    >
                      <UserMinus className="w-3 h-3" />
                    </button>
                  )}
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>

      {addingMember && (
        <AddMemberModal
          groupId={group.id}
          groupName={group.name}
          existing={members.map((m) => m.user_id)}
          onClose={() => setAddingMember(false)}
          onAdded={async () => {
            await Promise.all([loadMembers(), refreshGroups()]);
          }}
        />
      )}

      {pickingDevice && (
        <AddDeviceToTeamModal
          groupId={group.id}
          groupName={group.name}
          onClose={() => setPickingDevice(false)}
          onAdded={async () => {
            await Promise.all([refreshDevices(), refreshGroups()]);
          }}
        />
      )}
    </div>
  );
};

const modalShell =
  'fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/80 backdrop-blur-sm animate-in fade-in duration-200';
const modalCard =
  'w-full max-w-lg rounded-2xl bg-slate-900 border border-slate-800 shadow-2xl p-6 relative';

/** Picks an admin account to put on the team. */
const AddMemberModal: React.FC<{
  groupId: string;
  groupName: string;
  existing: string[];
  onClose: () => void;
  onAdded: () => Promise<void>;
}> = ({ groupId, groupName, existing, onClose, onAdded }) => {
  const [users, setUsers] = useState<User[]>([]);
  const [userId, setUserId] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    ApiClient.listUsers()
      .then((all) => setUsers(all.filter((u) => !existing.includes(u.id))))
      .catch((e) => setError(e?.message ?? 'Failed to load admins'));
  }, [existing]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!userId) return;
    setSaving(true);
    setError(null);
    try {
      await ApiClient.addGroupMember(groupId, userId);
      await onAdded();
      onClose();
    } catch (err: any) {
      setError(err?.message ?? 'Failed to add member');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className={modalShell}>
      <div className={modalCard}>
        <button
          onClick={onClose}
          className="absolute top-5 right-5 p-2 text-slate-400 hover:text-slate-200 rounded-lg hover:bg-slate-800"
        >
          <X className="w-4 h-4" />
        </button>

        <h3 className="text-lg font-bold text-slate-100 mb-1">Add an admin</h3>
        <p className="text-xs text-slate-400 mb-6">
          They will be able to see and open a session on every device in{' '}
          <span className="text-slate-200 font-semibold">{groupName}</span>.
        </p>

        {error && (
          <div className="mb-5 p-3.5 rounded-xl bg-rose-500/10 border border-rose-500/20 flex items-center gap-3 text-xs text-rose-400">
            <AlertCircle className="w-4 h-4 shrink-0" />
            <span>{error}</span>
          </div>
        )}

        <form onSubmit={handleSubmit} className="space-y-4">
          <select
            value={userId}
            onChange={(e) => setUserId(e.target.value)}
            className="w-full px-3 py-2.5 rounded-xl bg-slate-950/60 border border-slate-800 text-sm text-slate-200 focus:outline-none focus:border-sky-500"
          >
            <option value="">Select an account…</option>
            {users.map((u) => (
              <option key={u.id} value={u.id}>
                {u.email} ({u.role === 'super_admin' ? 'Super Admin' : 'Admin'})
              </option>
            ))}
          </select>
          {users.length === 0 && !error && (
            <p className="text-[11px] text-slate-500">
              Every admin account is already on this team.
            </p>
          )}

          <div className="pt-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={saving || !userId}
              className="flex-1 py-2.5 rounded-xl bg-gradient-to-r from-sky-500 to-blue-600 hover:from-sky-400 hover:to-blue-500 text-white text-xs font-semibold shadow-lg shadow-sky-500/20 transition-all disabled:opacity-50"
            >
              {saving ? 'Adding…' : 'Add to team'}
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

/** Moves an existing device into this team, preserving its individual admin. */
const AddDeviceToTeamModal: React.FC<{
  groupId: string;
  groupName: string;
  onClose: () => void;
  onAdded: () => Promise<void>;
}> = ({ groupId, groupName, onClose, onAdded }) => {
  const { devices } = useDevices();
  const [deviceId, setDeviceId] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const candidates = devices.filter((d) => d.group_id !== groupId);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const device = devices.find((d) => d.id === deviceId);
    if (!device) return;
    setSaving(true);
    setError(null);
    try {
      await ApiClient.assignDevice(device.id, device.assigned_admin_id ?? undefined, groupId);
      await onAdded();
      onClose();
    } catch (err: any) {
      setError(err?.message ?? 'Failed to move device');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className={modalShell}>
      <div className={modalCard}>
        <button
          onClick={onClose}
          className="absolute top-5 right-5 p-2 text-slate-400 hover:text-slate-200 rounded-lg hover:bg-slate-800"
        >
          <X className="w-4 h-4" />
        </button>

        <h3 className="text-lg font-bold text-slate-100 mb-1">Add a device</h3>
        <p className="text-xs text-slate-400 mb-6">
          Moves an endpoint into <span className="text-slate-200 font-semibold">{groupName}</span>. A
          device belongs to one team at a time; its individual admin is left as it is.
        </p>

        {error && (
          <div className="mb-5 p-3.5 rounded-xl bg-rose-500/10 border border-rose-500/20 flex items-center gap-3 text-xs text-rose-400">
            <AlertCircle className="w-4 h-4 shrink-0" />
            <span>{error}</span>
          </div>
        )}

        <form onSubmit={handleSubmit} className="space-y-4">
          <select
            value={deviceId}
            onChange={(e) => setDeviceId(e.target.value)}
            className="w-full px-3 py-2.5 rounded-xl bg-slate-950/60 border border-slate-800 text-sm text-slate-200 focus:outline-none focus:border-sky-500"
          >
            <option value="">Select an endpoint…</option>
            {candidates.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name} {d.group_name ? `— currently in ${d.group_name}` : '— no team'}
              </option>
            ))}
          </select>
          {candidates.length === 0 && (
            <p className="text-[11px] text-slate-500">Every device is already in this team.</p>
          )}

          <div className="pt-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={saving || !deviceId}
              className="flex-1 py-2.5 rounded-xl bg-gradient-to-r from-sky-500 to-blue-600 hover:from-sky-400 hover:to-blue-500 text-white text-xs font-semibold shadow-lg shadow-sky-500/20 transition-all disabled:opacity-50"
            >
              {saving ? 'Moving…' : 'Add to team'}
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
