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
