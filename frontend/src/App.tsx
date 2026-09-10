import React from 'react';
import { Navigate, Route, Routes } from 'react-router-dom';
import { LoginPage } from './pages/LoginPage';
import { EnrollLandingPage } from './pages/EnrollLandingPage';
import { DashboardPage } from './pages/DashboardPage';
import { LiveViewerPage } from './pages/LiveViewerPage';
import { LiveSessionPage } from './pages/LiveSessionPage';
import { TeamsPage } from './pages/TeamsPage';
import { TeamDetailPage } from './pages/TeamDetailPage';
import { GroupMonitorPage } from './pages/GroupMonitorPage';
import { InviteLinksPage } from './pages/InviteLinksPage';
import { UsersPage } from './pages/UsersPage';
import { SessionsPage } from './pages/SessionsPage';
import { AuditLogsPage } from './pages/AuditLogsPage';
import { ReportsPage } from './pages/ReportsPage';
import { NotFoundPage } from './pages/NotFoundPage';
import { AppLayout } from './routes/AppLayout';
import { RequireAuth } from './routes/RequireAuth';
import { RequireSuperAdmin } from './routes/RequireSuperAdmin';

/**
 * The route table.
 *
 * This used to be a single `currentTab` string and a chain of `&&`, which meant the URL
 * never changed: no deep links, no back button, and a refresh always landed on the device
 * list even in the middle of a session. Every section now has a real path.
 */
export const MainApp: React.FC = () => (
  <Routes>
    {/* Public. /enroll is where an invite link points — see EnrollLandingPage. */}
    <Route path="/login" element={<LoginPage />} />
    <Route path="/enroll" element={<EnrollLandingPage />} />

    <Route element={<RequireAuth />}>
      <Route element={<AppLayout />}>
        <Route index element={<Navigate to="/devices" replace />} />
        <Route path="devices" element={<DashboardPage />} />
        {/* Deep-linkable session. Pasting or refreshing this resolves the device by id. */}
        <Route path="devices/:deviceId/live" element={<LiveSessionPage />} />
        <Route path="viewer" element={<LiveViewerPage />} />
        {/* The monitoring wall. Both forms use the same page: unscoped shows every
            device the operator may see, scoped narrows it to one team. Neither needs a
            role guard beyond RequireAuth — the device list and each tile's session are
            already scoped server-side. */}
        <Route path="monitor" element={<GroupMonitorPage />} />
        {/* Both roles: the list is scoped server-side to links the caller created. */}
        <Route path="invites" element={<InviteLinksPage />} />
        <Route path="teams" element={<TeamsPage />} />
        <Route path="teams/:groupId" element={<TeamDetailPage />} />
        <Route path="teams/:groupId/monitor" element={<GroupMonitorPage />} />
        <Route path="sessions" element={<SessionsPage />} />
        <Route path="audit" element={<AuditLogsPage />} />
        <Route path="reports" element={<ReportsPage />} />

        <Route element={<RequireSuperAdmin />}>
          <Route path="users" element={<UsersPage />} />
        </Route>

        <Route path="*" element={<NotFoundPage />} />
      </Route>
    </Route>
  </Routes>
);
