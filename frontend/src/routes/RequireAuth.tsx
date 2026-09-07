import React from 'react';
import { Navigate, Outlet, useLocation } from 'react-router-dom';
import { useAuth } from '../context/AuthContext';

/**
 * Gate for every authenticated route.
 *
 * The loading branch matters more than it looks: AuthProvider starts with whatever is in
 * localStorage and only confirms it against /api/auth/me afterwards. Redirecting to
 * /login before that resolves would bounce a legitimately logged-in user off a deep link
 * on every cold load.
 */
export const RequireAuth: React.FC = () => {
  const { user, loading } = useAuth();
  const location = useLocation();

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

  // The attempted path travels in location state so signing in returns the user to where
  // they were headed rather than dropping them on the dashboard.
  if (!user) {
    return <Navigate to="/login" replace state={{ from: location }} />;
  }

  return <Outlet />;
};
