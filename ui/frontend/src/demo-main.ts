// The demo build's entry (vite.config.ts swaps it in for main.ts in demo mode
// only). The page itself is the desktop; its window loads the same page with
// `?embed`, which runs the storefront as the app does, with nothing added.
import { mountDesktop } from "./demo.ts";

if (new URLSearchParams(location.search).has("embed")) {
  void import("./main.ts");
} else {
  mountDesktop();
}
