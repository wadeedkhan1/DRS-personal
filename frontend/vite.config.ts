import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 3000,
    proxy: {
      // The agent download must come BEFORE '/api' — Vite matches in insertion order —
      // and must NOT set changeOrigin. The backend bakes the address it believes it is
      // reachable at into the downloaded agent, derived from the Host header. With
      // changeOrigin that header becomes 'localhost:8080', so an agent downloaded from
      // another machine on the LAN would be configured to talk to *its own* localhost
      // and could never connect. Left alone, Host stays the Vite origin, which proxies
      // both /api and /ws and therefore works from anywhere on the network.
      '/api/enroll/agent': {
        target: 'http://localhost:8080',
        changeOrigin: false,
      },
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
      '/ws': {
        target: 'ws://localhost:8080',
        ws: true,
      }
    }
  }
});
