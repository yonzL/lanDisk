import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// In dev, API calls are proxied to a locally running lanDisk.exe. The backend's
// default port is 80 (needs a privileged service), so for development run
// `lanDisk.exe config set port 8080` once and leave LAND_DEV_API at its default;
// override the env var if your dev instance listens elsewhere.
const devApi = process.env.LAND_DEV_API ?? 'http://127.0.0.1:8080'

// The build lands in web/dist, which go:embed freezes into lanDisk.exe.
export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': devApi,
      '/preview-assets': devApi,
    },
  },
})
