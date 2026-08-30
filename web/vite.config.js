import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// The dashboard builds straight into the Go package that embeds it, so a
// `go build` after `npm run build` produces one self-contained binary.
export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: '../internal/api/dist',
    emptyOutDir: true,
    // Source maps would roughly double the binary size for no benefit to
    // an operator who is not debugging the dashboard itself.
    sourcemap: false,
  },
  server: {
    port: 5173,
    // In development the dashboard runs from Vite and talks to a locally
    // running `publix serve`.
    proxy: {
      '/api': { target: 'http://127.0.0.1:4321', changeOrigin: true },
    },
  },
});
