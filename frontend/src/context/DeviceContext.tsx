import React, { createContext, useContext, useState, useEffect, useCallback } from 'react';
import { Device, DeviceGroup, DeviceStatus } from '../types';
import { ApiClient } from '../api/client';
import { PresenceSocket } from '../api/signaling';
import { useAuth } from './AuthContext';

interface DeviceContextType {
  devices: Device[];
  groups: DeviceGroup[];
  loading: boolean;
  /** Whether the live presence feed is connected, for the header indicator. */
  presenceConnected: boolean;
  refreshDevices: () => Promise<void>;
  refreshGroups: () => Promise<void>;
}

const DeviceContext = createContext<DeviceContextType | undefined>(undefined);

/**
 * Holds the device list and keeps its status live.
 *
 * It deliberately knows nothing about sessions. A session is one socket, opened and
 * closed by the viewer component that owns it (see ScreenViewer), so there is exactly
 * one place that can start or stop one. Splitting that responsibility across a context
 * and a component is what previously caused duplicate session requests and sessions
 * that were never ended.
 */
export const DeviceProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const { token } = useAuth();
  const [devices, setDevices] = useState<Device[]>([]);
  const [groups, setGroups] = useState<DeviceGroup[]>([]);
  const [loading, setLoading] = useState<boolean>(true);
  const [presenceConnected, setPresenceConnected] = useState(false);

  const refreshDevices = useCallback(async () => {
    try {
      setDevices(await ApiClient.listDevices());
    } catch (e) {
      console.error('Failed to load devices:', e);
    } finally {
      setLoading(false);
    }
  }, []);

  const refreshGroups = useCallback(async () => {
    try {
      setGroups(await ApiClient.listGroups());
    } catch (e) {
      console.error('Failed to load groups:', e);
    }
  }, []);

  useEffect(() => {
    if (!token) return;

    void refreshDevices();
    void refreshGroups();

    const presence = new PresenceSocket(token, setPresenceConnected);
    const unsubscribe = presence.onPresence((deviceId, status) => {
      setDevices((prev) =>
        prev.map((d) =>
          d.id === deviceId
            ? { ...d, status: status as DeviceStatus, last_seen_at: new Date().toISOString() }
            : d,
        ),
      );
    });
    presence.connect();

    return () => {
      unsubscribe();
      presence.disconnect();
      setPresenceConnected(false);
    };
  }, [token, refreshDevices, refreshGroups]);

  return (
    <DeviceContext.Provider value={{ devices, groups, loading, presenceConnected, refreshDevices, refreshGroups }}>
      {children}
    </DeviceContext.Provider>
  );
};

export const useDevices = () => {
  const context = useContext(DeviceContext);
  if (!context) throw new Error('useDevices must be used within a DeviceProvider');
  return context;
};
