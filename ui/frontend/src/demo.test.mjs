import assert from "node:assert/strict";
import test from "node:test";

import { embedUrl } from "./demo.ts";

// The desktop's window must show the store and settings the link asked for,
// and must add `embed`, or the window would draw another desktop inside it.
test("embedUrl keeps every parameter and adds embed", () => {
  assert.equal(
    embedUrl("https://sofcat.app/demo/?store=apps&nav=left&categories=list"),
    "https://sofcat.app/demo/?store=apps&nav=left&categories=list&embed",
  );
  assert.equal(embedUrl("https://sofcat.app/demo/"), "https://sofcat.app/demo/?embed");
  assert.equal(embedUrl("https://sofcat.app/demo/?"), "https://sofcat.app/demo/?embed");
  assert.equal(embedUrl("https://sofcat.app/demo/?unbranded#x"), "https://sofcat.app/demo/?unbranded&embed#x");
});
