import React from 'react';
import { Outlet } from 'react-router-dom';
import { Navbar } from '../components/Navbar';
import { Sidebar } from '../components/Sidebar';
import { DeviceProvider } from '../context/DeviceContext';

/**
 * The authenticated chrome: header, sidebar, and the page itself.
 *
 * DeviceProvider lives here rather than at the root because it owns the presence socket,
 * which needs a token. Mounting it above the auth gate would open a socket on the login
 * screen; mounting it per page would tear the socket down and rebuild it on every
 * navigation.
 */
export const AppLayout: React.FC = () => (
  <DeviceProvider>
    <div className="min-h-screen bg-slate-950 flex flex-col">
      <Navbar />
      <div className="flex-1 flex overflow-hidden">
        <Sidebar />
        <main className="flex-1 overflow-y-auto p-8">
          <div className="max-w-7xl mx-auto">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  </DeviceProvider>
);
