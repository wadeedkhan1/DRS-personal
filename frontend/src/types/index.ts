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
  last_seen_at: string;
  status: DeviceStatus;
  metadata?: Record<string, any>;
  created_at: string;
  updated_at: string;
}

export interface DeviceGroup {
  id: string;
  org_id: string;
  name: string;
  description: string;
  created_at: string;
  device_count?: number;
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
