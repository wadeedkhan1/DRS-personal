import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { MainApp } from './App';
import { AuthProvider } from './context/AuthContext';
import './index.css';

// BrowserRouter rather than HashRouter: nginx already serves the SPA fallback
// (try_files $uri $uri/ /index.html) and Vite's dev server does the same, so real paths
// survive a refresh and can be pasted to a colleague.
ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <AuthProvider>
        <MainApp />
      </AuthProvider>
    </BrowserRouter>
  </React.StrictMode>
);
