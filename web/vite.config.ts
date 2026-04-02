import { defineConfig } from 'vite'
import { fileURLToPath } from 'node:url'

const outDir = fileURLToPath(new URL('../internal/webui/dist', import.meta.url))

export default defineConfig({
  server: {
    port: 3000,
  },
  build: {
    outDir,
  },
})
