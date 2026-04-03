import { defineConfig } from 'vite'
import { fileURLToPath } from 'node:url'

const outDir = fileURLToPath(new URL('../internal/webui/dist', import.meta.url))
const rootDir = fileURLToPath(new URL('.', import.meta.url))
const indexHTML = fileURLToPath(new URL('./index.html', import.meta.url))
const relayHTML = fileURLToPath(new URL('./relay.html', import.meta.url))

export default defineConfig({
  server: {
    port: 3000,
  },
  build: {
    outDir,
    emptyOutDir: true,
    rollupOptions: {
      input: {
        index: indexHTML,
        relay: relayHTML,
      },
    },
  },
  root: rootDir,
})
