import { defineConfig } from 'vite'
import { fileURLToPath } from 'node:url'

const outDir = fileURLToPath(new URL('../webui/dist', import.meta.url))
const rootDir = fileURLToPath(new URL('.', import.meta.url))

export default defineConfig({
  server: {
    port: 3000,
  },
  build: {
    outDir,
    emptyOutDir: true,
  },
  root: rootDir,
})
