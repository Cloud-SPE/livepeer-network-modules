import { defineConfig } from "vite";

// Dev: `pnpm -F @livepeer-network-modules/daydream-portal-portal dev`.
// Build: `pnpm -F @livepeer-network-modules/daydream-portal-portal build:bundle`.
//
// Workspace imports from @livepeer-network-modules/customer-portal-shared
// resolve via the shared package's `exports` map (built artifacts in
// dist/), so make sure that package has been built before vite dev.

export default defineConfig({
  root: "src",
  server: {
    port: 5173,
    host: "127.0.0.1",
    proxy: {
      "/portal": "http://127.0.0.1:8080",
      "/healthz": "http://127.0.0.1:8080",
    },
  },
  build: {
    outDir: "../dist-bundle",
    emptyOutDir: true,
    target: "es2022",
  },
});
