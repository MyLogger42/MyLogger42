import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Vite config — output to ./dist (default) so the Go embed directive
// at /embed.go can pick the bundle up via //go:embed all:web/dist.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    // During `npm run dev` proxy API calls to the Go server so we don't
    // have to deal with CORS in development. The backend already sends
    // permissive CORS headers, but proxying keeps cookies/headers clean.
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        ws: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
});
