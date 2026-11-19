import { fileURLToPath } from "node:url";

import { type Plugin, defineConfig } from "vite";

// Only development and demo modes get the browser mock; every other mode
// (including the production bundle Wails embeds) resolves the real generated
// bindings. Demo is the public, service-free build (`npm run build:demo`). A
// rebranded build points SOFCAT_PUBLIC_DIR at a copy of public/ with the
// brand's icon (`just brand=… build`).
const MOCK_MODES = new Set(["development", "demo"]);

// Demo mode alone starts at src/demo-main.ts, which draws the demo's desktop
// or, in its window (`?embed`), loads main.ts. Every other mode keeps main.ts
// as the entry, so the desktop never reaches the bundle Wails embeds.
const demoEntry: Plugin = {
  name: "sofcat-demo-entry",
  transformIndexHtml: {
    order: "pre",
    handler: (html) => html.replace('src="/src/main.ts"', 'src="/src/demo-main.ts"'),
  },
};

export default defineConfig(({ mode }) => ({
  publicDir: process.env.SOFCAT_PUBLIC_DIR || "public",
  plugins: mode === "demo" ? [demoEntry] : [],
  resolve: {
    alias: {
      "sofcat-api-impl": fileURLToPath(
        new URL(MOCK_MODES.has(mode) ? "./src/mock-api.ts" : "./src/wails-api.ts", import.meta.url),
      ),
    },
  },
}));
