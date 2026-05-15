import { defineConfig } from "vite";

// Dev: `pnpm -F @livepeer-network-modules/daydream-portal-admin dev`.
// Build: `pnpm -F @livepeer-network-modules/daydream-portal-admin build:bundle`.
//
// Runs on a different port than the portal SPA so both can be served
// concurrently against the same backend.

export default defineConfig({
  root: "src",
  server: {
    port: 5174,
    host: "127.0.0.1",
    proxy: {
      "/admin": "http://127.0.0.1:8080",
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
