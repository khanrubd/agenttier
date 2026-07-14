// Copyright 2024 AgentTier Authors.
// SPDX-License-Identifier: Apache-2.0

import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  build: {
    rollupOptions: {
      output: {
        // Split vendor chunks to keep the main bundle well under the 750 KB
        // budget and give future dependency bumps room to grow. xterm + its
        // addons account for the bulk of the current 720 KB bundle; splitting
        // them into their own chunk lets browsers cache them independently of
        // app code churn, and brings the per-chunk sizes down to a range where
        // the Vite >500 KB warning no longer fires on every build.
        manualChunks: {
          // React runtime — changes rarely, cache-friendly.
          'vendor-react': ['react', 'react-dom', 'react-router-dom'],
          // xterm terminal emulator + addons — largest contributor to
          // bundle size; isolated so a terminal-only code change doesn't
          // bust the React cache and vice-versa.
          'vendor-xterm': [
            'xterm',
            'xterm-addon-fit',
            'xterm-addon-web-links',
            'xterm-addon-webgl',
          ],
        },
      },
    },
  },
  server: {
    port: 3000,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
      '/ws': {
        target: 'ws://localhost:8080',
        ws: true,
      },
    },
  },
});
