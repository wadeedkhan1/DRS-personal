import React, { useState } from 'react';
import { useAuth } from './context/AuthContext';
import { LoginPage } from './pages/LoginPage';
import { Navbar } from './components/Navbar';
import { Sidebar } from './components/Sidebar';
import { DashboardPage } from './pages/DashboardPage';
import { LiveViewerPage } from './pages/LiveViewerPage';
import { UsersPage } from './pages/UsersPage';
import { AuditLogsPage } from './pages/AuditLogsPage';
import { ReportsPage } from './pages/ReportsPage';
import { Device } from './types';
import { DeviceProvider } from './context/DeviceContext';

export const MainApp: React.FC = () => {
  const { user, loading } = useAuth();
  const [currentTab, setCurrentTab] = useState('devices');
  const [streamDevice, setStreamDevice] = useState<Device | null>(null);

  if (loading) {
    return (
      <div className="min-h-screen bg-slate-950 flex items-center justify-center text-slate-400">
        <div className="flex flex-col items-center gap-3">
          <div className="w-8 h-8 rounded-full border-2 border-sky-500 border-t-transparent animate-spin"></div>
          <span className="text-xs font-mono">Initializing DRS Platform...</span>
        </div>
      </div>
    );
  }

  if (!user) {
    return <LoginPage />;
  }

  const handleSelectDeviceForStream = (dev: Device) => {
    setStreamDevice(dev);
    setCurrentTab('viewer');
  };

  return (
    <DeviceProvider>
      <div className="min-h-screen bg-slate-950 flex flex-col">
        <Navbar />
        <div className="flex-1 flex overflow-hidden">
          <Sidebar currentTab={currentTab} onTabChange={setCurrentTab} />
          <main className="flex-1 overflow-y-auto p-8">
            <div className="max-w-7xl mx-auto">
              {currentTab === 'devices' && (
                <DashboardPage onSelectDeviceForStream={handleSelectDeviceForStream} />
              )}
              {currentTab === 'viewer' && (
                <LiveViewerPage selectedDevice={streamDevice} />
              )}
              {currentTab === 'users' && <UsersPage />}
              {currentTab === 'audit' && <AuditLogsPage />}
              {currentTab === 'reports' && <ReportsPage />}
            </div>
          </main>
        </div>
      </div>
    </DeviceProvider>
  );
};
