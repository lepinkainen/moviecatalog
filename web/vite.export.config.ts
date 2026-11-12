import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vite';

// The read-only library page for `moviecatalog export`, built separately from
// the app so the export copies only its own assets.
export default defineConfig({
  plugins: [svelte()],
  // Relative asset paths, so the site also works from a subdirectory of the host.
  base: './',
  build: {
    outDir: '../internal/webui/export',
    emptyOutDir: true,
    rollupOptions: { input: 'export.html' },
  },
});
