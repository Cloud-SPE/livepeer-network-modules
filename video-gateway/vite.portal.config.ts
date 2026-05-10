import { resolve } from "node:path";

import { defineConfig } from "vite";

export default defineConfig({
  root: resolve(__dirname, "src", "frontend", "portal", "src"),
  base: "/portal/",
  build: {
    outDir: resolve(__dirname, "src", "frontend", "portal", "dist"),
    emptyOutDir: true,
  },
});
