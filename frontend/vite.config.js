import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Dev proxy points /api and /ws at the control plane so the SPA talks to a
// same-origin backend without CORS. In production nginx fronts both.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/ws': { target: 'ws://localhost:8080', ws: true },
    },
  },
  test: {
    // jsdom for all specs: the contract/auth specs need localStorage + fetch, and
    // the pure-logic specs run fine under it too.
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.js'],
  },
});
