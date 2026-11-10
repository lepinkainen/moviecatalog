import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [svelte()],
  build: {
    // Built straight into the Go package that embeds it.
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
  },
  server: {
    // Listen on the LAN so the phone can use the dev server too.
    host: true,
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
});
