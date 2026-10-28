import { fileURLToPath } from "node:url";

import { defineConfig } from "vite";

// Only development mode gets the browser mock; every other mode (including the
// production bundle Wails embeds) resolves the real generated bindings. A
// rebranded build points SOFCAT_PUBLIC_DIR at a copy of public/ with the
// brand's icon (`just brand=… build`).
export default defineConfig(({ mode }) => ({
  publicDir: process.env.SOFCAT_PUBLIC_DIR || "public",
  resolve: {
    alias: {
      "sofcat-api-impl": fileURLToPath(
        new URL(mode === "development" ? "./src/mock-api.ts" : "./src/wails-api.ts", import.meta.url),
      ),
    },
  },
}));
