import assert from "node:assert/strict";
import test from "node:test";

import { brandingOverride } from "./mock-stores.ts";

function withSearch(search, run) {
  globalThis.location = { search };
  try {
    return run();
  } finally {
    delete globalThis.location;
  }
}

// The docs' screenshots and their live links pass branding in the URL; a
// mistyped link must still show the store rather than break the demo.
test("?branding= overrides look and layout only, and ignores anything but a JSON object", () => {
  // The demo is public, so a link cannot bring its own words or link URL.
  const wanted = { bannerAlign: "center", title: "Not Acme", helpUrl: "https://example.com/", sections: ["X"] };
  const search = "?branding=" + encodeURIComponent(JSON.stringify(wanted));
  assert.deepEqual(withSearch(search, brandingOverride), { bannerAlign: "center" });
  for (const bad of ["", "?branding=", "?branding=%7B", "?branding=%5B1%5D", "?branding=null", "?branding=3"]) {
    assert.deepEqual(withSearch(bad, brandingOverride), {}, bad);
  }
});
