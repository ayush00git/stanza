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
      '/runs': apiTarget,
    },
  },
})
