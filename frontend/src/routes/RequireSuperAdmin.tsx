import React from 'react';
import { Navigate, Outlet } from 'react-router-dom';
import { useAuth } from '../context/AuthContext';

/**
 * Hides the super-admin-only pages from an Admin.
 *
 * This is a convenience, not a security boundary: the real gate is
 * middleware.RequireRole on the server, which every one of these pages' requests hits
 * regardless of what the browser chose to render. All this avoids is painting a screen
 * whose every call would come back 403.
 */
export const RequireSuperAdmin: React.FC = () => {
  const { isSuperAdmin } = useAuth();
  return isSuperAdmin ? <Outlet /> : <Navigate to="/devices" replace />;
};
