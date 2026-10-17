import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

const nodeTarget = process.env.XENIA_NODE_URL || 'http://127.0.0.1:8080'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    host: '0.0.0.0',
    proxy: {
      '/health': nodeTarget,
      '/register': nodeTarget,
      '/users': nodeTarget,
      '/ws': { target: nodeTarget.replace(/^http/, 'ws'), ws: true },
      '/internal': nodeTarget,
    },
  },
})
