import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';

const __dirname = dirname(fileURLToPath(import.meta.url));

export default defineConfig({
  root: resolve(__dirname, 'src/frontend/web-ui'),
  base: '/admin/console/',
  build: {
    outDir: resolve(__dirname, 'src/frontend/admin/dist'),
    emptyOutDir: true,
  },
});
