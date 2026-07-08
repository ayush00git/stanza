import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Proxy the Go API in dev so the browser talks to the frontend origin only
// (no CORS) and SSE streams through untouched.
const apiTarget = 'http://localhost:8080'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      '/health': apiTarget,
      '/search': apiTarget,
      '/complex': apiTarget,
      '/chembl': apiTarget,
      '/dock': apiTarget,
      // /runs is BOTH an API prefix and the SPA route prefix (/runs, /runs/:id).
      // Serve the app for browser navigations (Accept: text/html) so a reload of
      // /runs/:id loads the viewer; proxy fetch()/XHR (Accept: */*) to the API.
      '/runs': {
        target: apiTarget,
        bypass(req) {
          if (req.headers.accept?.includes('text/html')) return '/index.html'
        },
      },
    },
  },
})
