import { Device, DeviceGroup, GroupMember, Session, AuditLog, UsageReport, User } from '../types';
import { ICEServerConfig } from './protocol';

const API_BASE = '/api';

export class ApiClient {
  private static getToken(): string | null {
    return localStorage.getItem('drs_token');
  }

  private static async request<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
    const token = this.getToken();
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      ...(options.headers as Record<string, string>),
    };

    if (token) {
      headers['Authorization'] = `Bearer ${token}`;
    }

    const response = await fetch(`${API_BASE}${endpoint}`, {
      ...options,
      headers,
    });

    if (!response.ok) {
      if (response.status === 401) {
        localStorage.removeItem('drs_token');
        localStorage.removeItem('drs_user');
        window.location.href = '/login';
      }
      const errorData = await response.json().catch(() => ({}));
      throw new Error(errorData.error || `HTTP ${response.status}: ${response.statusText}`);
    }

    return response.json();
  }

  // Auth
  static async login(email: string, password: string): Promise<{ token: string; user: User }> {
    const res = await this.request<{ token: string; user: User }>('/auth/login', {
      method: 'POST',
      body: JSON.stringify({ email, password }),
    });
    localStorage.setItem('drs_token', res.token);
    localStorage.setItem('drs_user', JSON.stringify(res.user));
    return res;
  }

  static async getMe(): Promise<User> {
    return this.request<User>('/auth/me');
  }

  // Devices
  static async listDevices(): Promise<Device[]> {
    return this.request<Device[]>('/devices');
  }

  static async getDevice(id: string): Promise<Device> {
    return this.request<Device>(`/devices/${id}`);
  }

  static async generateEnrollmentToken(
    deviceType: string,
    assignedAdminId?: string,
    groupId?: string,
  ): Promise<{ enrollment_token: string; expires_at: string }> {
    return this.request<{ enrollment_token: string; expires_at: string }>('/devices/enrollment-token', {
      method: 'POST',
      body: JSON.stringify({ device_type: deviceType, assigned_admin_id: assignedAdminId, group_id: groupId }),
    });
  }

  /**
   * The ICE configuration for a session. The agent is handed an identical list and
   * policy inside start_session, because two peers negotiating against different
   * candidate sets fail in ways that are almost impossible to diagnose from either end.
   */
  static async getIceConfig(): Promise<{ iceServers: ICEServerConfig[]; iceTransportPolicy: RTCIceTransportPolicy }> {
    const res = await this.request<{
      iceServers: ICEServerConfig[];
      iceTransportPolicy: string;
    }>('/session/ice');
    return {
      iceServers: res.iceServers ?? [],
      iceTransportPolicy: res.iceTransportPolicy === 'relay' ? 'relay' : 'all',
    };
  }

  static async assignDevice(id: string, adminId?: string, groupId?: string): Promise<void> {
    return this.request<void>(`/devices/${id}/assign`, {
      method: 'PUT',
      body: JSON.stringify({ admin_id: adminId || null, group_id: groupId || null }),
    });
  }

  static async deleteDevice(id: string): Promise<void> {
    return this.request<void>(`/devices/${id}`, {
      method: 'DELETE',
    });
  }

  // Teams / groups
  static async listGroups(): Promise<DeviceGroup[]> {
    return this.request<DeviceGroup[]>('/groups');
  }

  static async createGroup(name: string, description: string): Promise<DeviceGroup> {
    return this.request<DeviceGroup>('/groups', {
      method: 'POST',
      body: JSON.stringify({ name, description }),
    });
  }

  /**
   * Patches a team. Omitted fields are left alone rather than blanked, so a rename does
   * not have to restate the description.
   */
  static async updateGroup(
    id: string,
    changes: { name?: string; description?: string },
  ): Promise<DeviceGroup> {
    return this.request<DeviceGroup>(`/groups/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(changes),
    });
  }

  static async deleteGroup(id: string): Promise<void> {
    return this.request<void>(`/groups/${id}`, { method: 'DELETE' });
  }

  static async listGroupMembers(id: string): Promise<GroupMember[]> {
    return this.request<GroupMember[]>(`/groups/${id}/members`);
  }

  /** Adding an admin to a team grants them every device in it. */
  static async addGroupMember(id: string, userId: string): Promise<void> {
    return this.request<void>(`/groups/${id}/members`, {
      method: 'POST',
      body: JSON.stringify({ user_id: userId }),
    });
  }

  static async removeGroupMember(id: string, userId: string): Promise<void> {
    return this.request<void>(`/groups/${id}/members/${userId}`, { method: 'DELETE' });
  }

  // Users (Super Admin)
  static async listUsers(): Promise<User[]> {
    return this.request<User[]>('/users');
  }

  static async createUser(email: string, password: string, role: string): Promise<User> {
    return this.request<User>('/users', {
      method: 'POST',
      body: JSON.stringify({ email, password, role }),
    });
  }

  static async deleteUser(id: string): Promise<void> {
    return this.request<void>(`/users/${id}`, {
      method: 'DELETE',
    });
  }

  // Sessions & Audit Logs
  static async listSessions(): Promise<Session[]> {
    return this.request<Session[]>('/sessions');
  }

  static async listAuditLogs(): Promise<AuditLog[]> {
    return this.request<AuditLog[]>('/audit-logs');
  }

  static async getUsageReport(): Promise<UsageReport> {
    return this.request<UsageReport>('/reports/usage');
  }
}
