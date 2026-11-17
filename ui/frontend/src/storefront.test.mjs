import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import {
  BUILT_IN_LABELS,
  actionLabel,
  brandingView,
  cardProgress,
  cardStatus,
  deriveAction,
  statusLabel,
  statusRecord,
  appliesToLine,
  itemPlacements,
  opensDetails,
  placementAction,
  placementStatus,
} from "./state.ts";

function item(overrides) {
  return {
    itemName: "Skill",
    displayName: "Skill",
    version: "1.0.0",
    catalog: "catalog",
    isManaged: false,
    isRequired: false,
    isInstalled: false,
    status: "NotInstalled",
    statusUpdatedAtUtc: "2026-11-03T00:00:00Z",
    ...overrides,
  };
}

function wire(overrides) {
  return {
    operationId: "op-1",
    seq: 1,
    timestampUtc: "2026-11-03T00:00:00Z",
    itemName: "Skill",
    displayName: "Skill",
    state: "Deferred",
    progressPercent: 100,
    message: "",
    requestedBy: "",
    ...overrides,
  };
}

// Labels are catalog and placement-table text. Escaping them here would show
// "&lt;b&gt;" on a button, and the shell must never parse them as markup, so
// they pass through untouched and the shell only ever assigns text.
test("a label containing markup is kept literally and the shell never parses HTML", () => {
  const labelled = item({ actionLabels: { install: "<b>Add</b>" } });
  assert.equal(actionLabel(labelled, "install"), "<b>Add</b>");
  assert.equal(deriveAction(labelled).label, "<b>Add</b>");
  const shell = readFileSync(new URL("./main.ts", import.meta.url), "utf8");
  for (const sink of ["innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "createContextualFragment"]) {
    assert.equal(shell.includes(sink), false, `main.ts uses ${sink}`);
  }
});

// The service already resolved item, then placement; the UI's only fallback is
// its built-in label for a key the service left out.
test("a resolved label is used verbatim and a missing key falls back to the built-in", () => {
  const labelled = item({
    actionLabels: { install: "Make available", installing: "Making available…", installed: "Available", cancel: "Stop" },
  });
  assert.deepEqual(deriveAction(labelled), { kind: "do", label: "Make available", method: "InstallItem" });
  assert.equal(statusLabel({ ...labelled, status: "Installed" }), "Available");
  // No remove key: the undo label stays "Remove".
  assert.equal(deriveAction({ ...labelled, isManaged: true, status: "Installed" }).label, "Remove");
  assert.equal(deriveAction({ ...labelled, isManaged: true, status: "WillBeInstalled" }).label, "Stop");

  const plain = item({});
  assert.equal(deriveAction(plain).label, BUILT_IN_LABELS.install);
  assert.equal(statusLabel({ ...plain, status: "Installed" }), "Installed");
  // A blank or non-string value (a tampered cache) is no label at all.
  assert.equal(actionLabel(item({ actionLabels: { install: "  ", remove: 7 } }), "install"), "Install");
  assert.equal(actionLabel(item({ actionLabels: { install: "  ", remove: 7 } }), "remove"), "Remove");
  assert.deepEqual(Object.values(BUILT_IN_LABELS), [
    "Install", "Installing…", "Installed", "Remove", "Removing…", "Update", "Cancel",
    "Will be installed", "Will be removed", "Applies to",
  ]);
});

test("the item's own install and removal phases use its in-progress labels", () => {
  const labelled = item({ actionLabels: { installing: "Making available…", removing: "Withdrawing…" } });
  const phase = (state, overrides = {}) =>
    cardProgress(labelled, [statusRecord(wire({ state, progressPercent: 40, ...overrides }))], "active");
  assert.equal(phase("Installing").label, "Making available…");
  assert.equal(phase("Removing").label, "Withdrawing…");
  assert.equal(phase("Downloading").label, "Downloading…");
  // A dependency's phase names the dependency, not this item's label.
  assert.equal(phase("Installing", { itemName: "Dep", displayName: "Dep" }).label, "Installing Dep…");
  assert.equal(cardProgress(item({}), [statusRecord(wire({ state: "Installing" }))], "active").label, "Installing…");
});

test("the list heading is the admin's when set, else Available software", () => {
  assert.equal(brandingView({ listHeading: "Skills and plugins" }).listHeading, "Skills and plugins");
  assert.equal(brandingView({ listHeading: "<i>x</i>" }).listHeading, "<i>x</i>");
  for (const payload of [null, {}, { listHeading: "   " }, { listHeading: 3 }]) {
    assert.equal(brandingView(payload).listHeading, "Available software", JSON.stringify(payload));
  }
  // A heading alone is not banner content.
  assert.equal(brandingView({ listHeading: "Skills" }).showBanner, false);
});

// The sizes scale today's rems, so no key, or one the shell does not know
// (a stale cache, a prototype key), must keep the factor at exactly 1.
test("the title and tagline sizes are a small named scale; anything else keeps the default", () => {
  const view = brandingView({ titleSize: "x-large", taglineSize: "small" });
  assert.equal(view.titleScale, 1.5);
  assert.equal(view.taglineScale, 0.85);
  assert.equal(brandingView({ titleSize: "large" }).titleScale, 1.25);
  for (const titleSize of [undefined, "", "medium", "huge", "24px", 2, "constructor"]) {
    assert.equal(brandingView({ titleSize }).titleScale, 1, String(titleSize));
  }
  assert.equal(brandingView({ titleSize: "large" }).showBanner, false);
});

test("the applies-to line lists the names in the order the service sent, after its label", () => {
  assert.equal(appliesToLine(item({ appliesTo: ["Claude Code", "Codex"] })), "Applies to: Claude Code, Codex");
  assert.equal(
    appliesToLine(item({ appliesTo: ["Claude Code"], actionLabels: { applies_to: "Works in" } })),
    "Works in: Claude Code",
  );
  assert.equal(appliesToLine(item({ appliesTo: [] })), "");
  assert.equal(appliesToLine(item({})), "");
  assert.equal(appliesToLine(item({ appliesTo: "Codex" })), "", "a non-array cache value shows nothing");
  // An old offline cache entry carries worksIn, which no longer shows.
  assert.equal(appliesToLine(item({ worksIn: ["Codex"] })), "");
});

test("a deferral reads as the missing software or the blocking app", () => {
  const deferred = (overrides) => cardProgress(item({}), [statusRecord(wire(overrides))], "terminal").label;
  const needs = "Needs Git for Windows. Install it first, then try again.";
  assert.equal(deferred({ message: needs, errorMessage: needs, errorCode: "missing_software" }), needs);

  const blocked = "blocking application(s) running: chrome";
  assert.equal(
    deferred({ message: blocked, errorMessage: blocked, errorCode: "blocked_by_running_app" }),
    "Waiting: close chrome to continue",
  );
  // An older service sends the blocking-app message without a code.
  assert.equal(deferred({ message: blocked }), "Waiting: close chrome to continue");
});

// A card shows only what its button cannot: a pending item's button reads just
// "Cancel" and a required item has none. The rest would repeat the button,
// and a built-in "Installed" beside a custom "Withdraw" reads as a mismatch.
test("a card's idle line is the pending or required status, worded by the item's labels", () => {
  const labelled = item({ actionLabels: { pending_install: "Will be enabled for you", pending_remove: "Will be withdrawn" } });
  assert.equal(cardStatus({ ...labelled, status: "WillBeInstalled" }), "Will be enabled for you");
  assert.equal(cardStatus({ ...labelled, isManaged: true, status: "WillBeRemoved" }), "Will be withdrawn");
  assert.equal(cardStatus(item({ status: "WillBeInstalled" })), "Will be installed");
  assert.equal(cardStatus(item({ status: "WillBeRemoved" })), "Will be removed");
  assert.equal(cardStatus(item({ isRequired: true, isManaged: true, status: "Installed" })), "Managed by your organisation");
  assert.equal(cardStatus(item({ status: "NotInstalled" })), "");
  assert.equal(cardStatus(item({ isManaged: true, status: "Installed", actionLabels: { installed: "Available" } })), "");
  // The detail page keeps the full status, with the same labels.
  assert.equal(statusLabel({ ...labelled, status: "WillBeInstalled" }), "Will be enabled for you");
});

const claude = (status, isInstalled = status === "Installed") => ({ name: "claude-skills", label: "Claude Code", isInstalled, status });
const codex = (status, isInstalled = status === "Installed") => ({ name: "codex-skills", label: "Codex", isInstalled, status });

// Only an item the service sends placements for gets rows; the rest keep the
// applies-to line and their single button exactly as before.
test("an item without placements has no rows and keeps its action and status", () => {
  const plain = item({ appliesTo: ["Claude Code", "Codex"], isManaged: true, isInstalled: true, status: "Installed" });
  assert.deepEqual(itemPlacements(plain), []);
  assert.deepEqual(itemPlacements(item({ placements: "claude-skills" })), [], "a non-array cache value is none");
  assert.deepEqual(deriveAction(plain), { kind: "undo", label: "Remove", method: "RemoveItem" });
  assert.equal(cardStatus(plain), "");
  assert.equal(statusLabel(plain), "Installed");
  assert.equal(appliesToLine(plain), "Applies to: Claude Code, Codex");
});

// Each row's button is the item's own action for that placement alone, so the
// request names the placement and a cancel takes back only that placement.
test("a placement row's button follows its status and names the placement", () => {
  const labelled = item({
    isManaged: true,
    actionLabels: { install: "Add", remove: "Withdraw", cancel: "Stop", pending_remove: "Will be withdrawn" },
    placements: [claude("Installed"), codex("NotInstalled")],
  });
  const row = (status) => placementAction(labelled, codex(status));
  assert.deepEqual(row("NotInstalled"), { kind: "do", label: "Add", method: "InstallItem", placement: "codex-skills" });
  assert.deepEqual(row("Installed"), { kind: "undo", label: "Withdraw", method: "RemoveItem", placement: "codex-skills" });
  assert.deepEqual(row("WillBeInstalled"), { kind: "cancel", label: "Stop", method: "RemoveItem", placement: "codex-skills" });
  assert.deepEqual(row("WillBeRemoved"), { kind: "cancel", label: "Stop", method: "InstallItem", placement: "codex-skills" });
  assert.equal(placementStatus(labelled, codex("WillBeRemoved")), "Will be withdrawn");
  assert.equal(placementStatus(labelled, codex("NotInstalled")), "Not installed");
  assert.equal(placementStatus(labelled, claude("Installed")), "Installed");
  // A required item goes everywhere: no row has a button.
  assert.equal(placementAction({ ...labelled, isRequired: true }, codex("Installed")), null);
});

// A partly installed item says where it is, and its card keeps Install, a
// plain install with no placement, which adds the rest.
test("a partly installed item reads Installed in its placements and keeps Install", () => {
  const narrowed = item({
    isManaged: true,
    isInstalled: true,
    status: "Installed",
    actionLabels: { install: "Add" },
    placements: [claude("Installed"), codex("NotInstalled")],
  });
  assert.equal(cardStatus(narrowed), "Installed in Claude Code");
  assert.equal(statusLabel(narrowed), "Installed in Claude Code");
  assert.deepEqual(deriveAction(narrowed), { kind: "do", label: "Add", method: "InstallItem" });
  // Not narrowed: the service reads the item NotInstalled, and the wording is the same.
  const partial = { ...narrowed, isInstalled: false, status: "NotInstalled" };
  assert.equal(cardStatus(partial), "Installed in Claude Code");

  // Everywhere, nowhere, or pending: the item's own wording and button.
  const everywhere = { ...narrowed, placements: [claude("Installed"), codex("Installed")] };
  assert.equal(cardStatus(everywhere), "");
  assert.equal(deriveAction(everywhere).method, "RemoveItem");
  assert.equal(cardStatus({ ...narrowed, isManaged: false, placements: [claude("NotInstalled"), codex("NotInstalled")] }), "");
  const pending = { ...narrowed, status: "WillBeInstalled", placements: [claude("Installed"), codex("WillBeInstalled")] };
  assert.equal(cardStatus(pending), "Will be installed");
  // Cancel undoes only the pending placement: a whole-item removal would
  // take the installed one with it.
  assert.deepEqual(deriveAction(pending), { kind: "cancel", label: "Cancel", method: "RemoveItem", placement: "codex-skills" });
  const removing = { ...narrowed, status: "WillBeRemoved", placements: [claude("WillBeRemoved", true), codex("NotInstalled")] };
  assert.deepEqual(deriveAction(removing), { kind: "cancel", label: "Cancel", method: "InstallItem", placement: "claude-skills" });
  // Every placement pending is the whole item's request, cancelled as one.
  const allPending = { ...narrowed, placements: [claude("WillBeInstalled"), codex("WillBeInstalled")], status: "WillBeInstalled" };
  assert.deepEqual(deriveAction(allPending), { kind: "cancel", label: "Cancel", method: "RemoveItem" });
  // Several pending beside one that is not: the rows cancel them, not the card.
  const third = { name: "other-skills", label: "Other", isInstalled: false, status: "WillBeInstalled" };
  assert.equal(deriveAction({ ...pending, placements: [...pending.placements, third] }), null);
  // An installed placement of an item nobody selected offers Install, as the item does.
  assert.equal(placementAction({ ...narrowed, isManaged: false }, claude("Installed")).method, "InstallItem");
});

// A click on a card's body opens its detail page, but a click on one of its
// buttons must only run that button: Install from the card must not also
// leave the list, and nor must selecting the card's text to copy it.
test("a click on a card opens details except on one of its controls or after a text drag", () => {
  // A minimal element tree whose closest walks up from the target the way the
  // DOM's does, matching the simple type selectors opensDetails uses.
  const node = (tag, parent = null) => ({
    tag,
    parent,
    closest(selector) {
      const types = selector.split(",").map((part) => part.trim());
      for (let at = this; at; at = at.parent) {
        if (types.includes(at.tag)) return at;
      }
      return null;
    },
  });
  const card = node("article");
  const title = node("h3", card);
  const button = node("button", node("p", card));
  const label = node("span", button);
  assert.equal(opensDetails(title, false), true, "the card's name, glyph or text");
  assert.equal(opensDetails(card, false), true, "the card's padding");
  assert.equal(opensDetails(button, false), false, "Install, Remove, Cancel or Details");
  assert.equal(opensDetails(label, false), false, "an element inside a button");
  assert.equal(opensDetails(title, true), false, "the release that ends selecting the card's text");
  assert.equal(opensDetails(null, false), false, "a target that is not an element");
});
