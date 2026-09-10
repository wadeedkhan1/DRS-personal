export type UserRole = 'super_admin' | 'admin';

export interface User {
  id: string;
  org_id: string;
  email: string;
  role: UserRole;
  created_at: string;
  updated_at: string;
}

export type DeviceType = 'windows' | 'android';
export type DeviceStatus = 'online' | 'offline' | 'in_session';

export interface Device {
  id: string;
  org_id: string;
  name: string;
  type: DeviceType;
  os_version: string;
  ip_address: string;
  assigned_admin_id?: string | null;
  assigned_admin_email?: string | null;
  group_id?: string | null;
  group_name?: string | null;
  allow_screen?: boolean;
  allow_terminal?: boolean;
  last_seen_at: string;
  status: DeviceStatus;
  metadata?: Record<string, any>;
  created_at: string;
  updated_at: string;
}

/**
 * A team: a set of devices, and a set of admins who can therefore see them. The counts
 * are computed by the server per query rather than stored.
 */
export interface DeviceGroup {
  id: string;
  org_id: string;
  name: string;
  description: string;
  created_at: string;
  device_count: number;
  member_count: number;
}

/** The response to minting an invite link. The token is shown once and never re-fetched. */
export interface EnrollmentTokenCreated {
  id: string;
  enrollment_token: string;
  expires_at: string;
}

/**
 * One outstanding invite link.
 *
 * Carries no token: the plaintext exists only in the response that created it, and only
 * the hash is stored. A link is identified for revocation by its id.
 */
export interface EnrollmentTokenSummary {
  id: string;
  label: string;
  device_type: DeviceType;
  group_id: string | null;
  group_name: string;
  created_by: string | null;
  created_by_email: string;
  created_at: string;
  revoked_at: string | null;
  expires_at: string;
  /** How many devices this link has enrolled — what decides if revoking it is safe. */
  device_count: number;
}

/** One admin on a team. */
export interface GroupMember {
  user_id: string;
  email: string;
  role: UserRole;
  created_at: string;
}

export interface Session {
  id: string;
  device_id: string;
  device_name?: string;
  admin_id: string;
  admin_email?: string;
  started_at: string;
  ended_at?: string;
  mode: 'view' | 'control';
  connection_type: 'p2p' | 'failed';
  status: 'active' | 'completed' | 'terminated';
}

export interface AuditLog {
  id: string;
  org_id?: string;
  actor_user_id?: string;
  actor_email: string;
  action: string;
  target_type: string;
  target_id: string;
  metadata: Record<string, any>;
  ip_address: string;
  created_at: string;
}

export interface UsageReport {
  total_devices: number;
  online_devices: number;
  total_sessions: number;
  active_sessions: number;
  avg_duration_minutes: number;
  total_admins: number;
}
