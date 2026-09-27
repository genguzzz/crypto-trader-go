import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: { proxy: { '/api': { target: process.env.CTG_API || 'http://127.0.0.1:8686', changeOrigin: true } } }, // CTG_API=https://… proxies to a remote instance
  build: { outDir: 'dist', emptyOutDir: true, chunkSizeWarningLimit: 800 },
})
