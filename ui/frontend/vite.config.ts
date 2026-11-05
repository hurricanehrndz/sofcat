import { fileURLToPath } from "node:url";

import { defineConfig } from "vite";

// Only development and demo modes get the browser mock; every other mode
// (including the production bundle Wails embeds) resolves the real generated
// bindings. Demo is the public, service-free build (`npm run build:demo`). A
// rebranded build points SOFCAT_PUBLIC_DIR at a copy of public/ with the
// brand's icon (`just brand=… build`).
const MOCK_MODES = new Set(["development", "demo"]);

export default defineConfig(({ mode }) => ({
  publicDir: process.env.SOFCAT_PUBLIC_DIR || "public",
  resolve: {
    alias: {
      "sofcat-api-impl": fileURLToPath(
        new URL(MOCK_MODES.has(mode) ? "./src/mock-api.ts" : "./src/wails-api.ts", import.meta.url),
      ),
    },
  },
}));
