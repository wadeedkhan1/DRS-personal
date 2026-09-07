import React from 'react';
import { Link } from 'react-router-dom';
import { Compass } from 'lucide-react';

/** Catch-all inside the authenticated shell, so a bad path is a page rather than a blank body. */
export const NotFoundPage: React.FC = () => (
  <div className="p-16 text-center rounded-2xl bg-slate-900/20 border border-slate-800/60 flex flex-col items-center">
    <Compass className="w-12 h-12 text-slate-600 mb-4" />
    <h1 className="text-lg font-bold text-slate-200">Page not found</h1>
    <p className="text-xs text-slate-500 mt-1.5 max-w-sm">
      That address does not match anything in the portal. It may have been a link from an older
      version.
    </p>
    <Link
      to="/devices"
      className="mt-6 px-4 py-2.5 rounded-xl bg-sky-500/10 hover:bg-sky-500/20 text-sky-400 border border-sky-500/30 text-xs font-semibold transition-all"
    >
      Back to endpoints
    </Link>
  </div>
);
