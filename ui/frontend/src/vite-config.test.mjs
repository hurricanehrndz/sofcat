import assert from "node:assert/strict";
import test from "node:test";

import config from "../vite.config.ts";

function apiImpl(mode) {
  return config({ mode, command: "build" }).resolve.alias["sofcat-api-impl"];
}

// The mock must never reach the bundle Wails embeds, and the public demo and
// `npm run dev` must never need a service.
test("only development and demo modes resolve the mock", () => {
  assert.match(apiImpl("development"), /mock-api\.ts$/);
  assert.match(apiImpl("demo"), /mock-api\.ts$/);
  assert.match(apiImpl("production"), /wails-api\.ts$/);
  assert.match(apiImpl("staging"), /wails-api\.ts$/);
});
