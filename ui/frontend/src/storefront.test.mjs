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
  enteringItems,
  statusLabel,
  statusRecord,
  appliesToLine,
  itemPlacements,
  opensDetails,
  placementAction,
  placementStatus,
  withoutLogo,
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

// style.css only has rules for these three names, and the root attribute is
// set from this value, so anything else (no key, an older service, a typo
// that reached a cached payload) must land on the default, wide.
test("the content width is compact, wide or full; anything else is wide", () => {
  for (const contentWidth of ["compact", "wide", "full"]) {
    assert.equal(brandingView({ contentWidth }).contentWidth, contentWidth);
  }
  for (const contentWidth of [undefined, "", "narrow", "100%", 2, "constructor"]) {
    assert.equal(brandingView({ contentWidth }).contentWidth, "wide", String(contentWidth));
  }
});

// style.css moves the filters only for left; anything else (no key, an older
// service, a typo in a cached payload) must keep the default, right.
test("the filter placement is right or left; anything else is right", () => {
  assert.equal(brandingView({ navigation: "tabs" }).navigation, "tabs");
  for (const navigation of [undefined, "", "sidebar", "TABS", "top", 1]) {
    assert.equal(brandingView({ navigation }).navigation, "sidebar", String(navigation));
  }
  assert.equal(brandingView({ filterPlacement: "left" }).filterPlacement, "left");
  assert.equal(brandingView({ filterPlacement: "right" }).filterPlacement, "right");
  for (const filterPlacement of [undefined, "", "center", "LEFT", 1, "constructor"]) {
    assert.equal(brandingView({ filterPlacement }).filterPlacement, "right", String(filterPlacement));
  }
});

const LOGO = { logoMime: "image/png", logoBase64: "iVBORw0KGgo=" };

// A logo banner without a usable logo would show nothing in the title's place,
// so it falls back to text; an unknown style is text too.
test("the banner style is logo only with a usable logo; anything else is text", () => {
  assert.equal(brandingView({ ...LOGO, bannerStyle: "logo" }).bannerStyle, "logo");
  assert.equal(brandingView({ bannerStyle: "logo", title: "Acme" }).bannerStyle, "text");
  assert.equal(brandingView({ bannerStyle: "logo", logoMime: "image/gif", logoBase64: "R0lG" }).bannerStyle, "text");
  for (const bannerStyle of [undefined, "", "text", "LOGO", "image", 1]) {
    assert.equal(brandingView({ ...LOGO, bannerStyle }).bannerStyle, "text", String(bannerStyle));
  }
});

test("the banner aligns left, center or right; anything else is left", () => {
  for (const bannerAlign of ["left", "center", "right"]) {
    assert.equal(brandingView({ bannerAlign }).bannerAlign, bannerAlign);
  }
  for (const bannerAlign of [undefined, "", "centre", "middle", 2, "constructor"]) {
    assert.equal(brandingView({ bannerAlign }).bannerAlign, "left", String(bannerAlign));
  }
});

// The heights live in style.css, one rule per name but medium, the banner from
// before the key; an unknown name must land on medium, not on no rule.
test("the banner height is one of four names with a rule each; anything else is medium", () => {
  const css = readFileSync(new URL("./style.css", import.meta.url), "utf8");
  for (const bannerHeight of ["small", "medium", "large", "x-large"]) {
    assert.equal(brandingView({ bannerHeight }).bannerHeight, bannerHeight);
    if (bannerHeight !== "medium") {
      assert.ok(css.includes(`#banner[data-height="${bannerHeight}"]`), bannerHeight);
    }
  }
  for (const bannerHeight of [undefined, "", "Large", "xx-large", "300px", 2, "constructor"]) {
    assert.equal(brandingView({ bannerHeight }).bannerHeight, "medium", String(bannerHeight));
  }
});

// Without a banner the app bar is the only place the product is named, so it
// keeps the name whatever app_bar_brand says.
test("the app bar drops the mark or name only under a banner", () => {
  for (const appBarBrand of ["full", "mark", "none"]) {
    assert.equal(brandingView({ title: "Acme", appBarBrand }).appBarBrand, appBarBrand);
    assert.equal(brandingView({ appBarBrand }).appBarBrand, "full", appBarBrand);
  }
  for (const appBarBrand of [undefined, "", "hidden", "NONE", 0]) {
    assert.equal(brandingView({ title: "Acme", appBarBrand }).appBarBrand, "full", String(appBarBrand));
  }
});

// app_bar_brand must never take the page's only h1 out of the accessibility
// tree: style.css hides it with a clip, not display or visibility.
test("a hidden app-bar brand stays in the accessibility tree", () => {
  const html = readFileSync(new URL("../index.html", import.meta.url), "utf8");
  assert.equal(html.match(/<h1\b/g)?.length, 1);
  assert.match(html, /<h1 class="product">.*<span id="product-name">/);
  const css = readFileSync(new URL("./style.css", import.meta.url), "utf8");
  const rule = css.match(/:root\[data-app-bar-brand="mark"\] #product-name,\s*:root\[data-app-bar-brand="none"\] \.product \{[^}]*\}/);
  assert.ok(rule, "no app_bar_brand rule");
  assert.match(rule[0], /clip-path: inset\(50%\)/);
  assert.doesNotMatch(rule[0], /display|visibility|aria-hidden/);
});

// A logo that sniffed as an image but does not decode must not leave the page
// without a visible name: the banner falls back to text, and with no title
// to show there, the app bar shows the product again.
test("a logo that fails to load falls back to text and keeps a visible name", () => {
  const titled = withoutLogo(brandingView({ ...LOGO, title: "Acme", bannerStyle: "logo", appBarBrand: "none" }));
  assert.deepEqual([titled.logoSrc, titled.bannerStyle, titled.appBarBrand], ["", "text", "none"]);
  const untitled = withoutLogo(brandingView({ ...LOGO, tagline: "Hi", bannerStyle: "logo", appBarBrand: "none" }));
  assert.deepEqual([untitled.logoSrc, untitled.bannerStyle, untitled.appBarBrand], ["", "text", "full"]);
  assert.equal(untitled.showBanner, true);
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
  // The dialog keeps the full status, with the same labels.
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

// A click on a card's body opens its dialog, but a click on one of its
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
  assert.equal(opensDetails(title, false), true, "the card's glyph or text");
  assert.equal(opensDetails(card, false), true, "the card's padding");
  assert.equal(opensDetails(button, false), false, "Install, Remove, Cancel or the card's name, which opens it itself");
  assert.equal(opensDetails(label, false), false, "an element inside a button");
  assert.equal(opensDetails(title, true), false, "the release that ends selecting the card's text");
  assert.equal(opensDetails(null, false), false, "a target that is not an element");
});

// The grid is rebuilt on every status update and refresh. A card that keeps
// its animation on .card itself would replay it each time and the whole store
// would flicker through an install; only a card new to the list may animate.
// Reduced motion is the user's switch for everything else: one rule at the
// end of style.css stops every animation and transition.
test("only a card new to the list animates, and reduced motion stops all motion", () => {
  const css = readFileSync(new URL("./style.css", import.meta.url), "utf8");
  const cardRules = [...css.matchAll(/\n\.card \{[^}]*\}/g)].map(([rule]) => rule);
  assert.notEqual(cardRules.length, 0);
  for (const rule of cardRules) {
    assert.equal(/\banimation\b/.test(rule), false, ".card itself animates");
  }
  assert.match(css, /\.card\[data-entering\] \{\s*animation:/);

  const reduced = css.slice(css.lastIndexOf("@media (prefers-reduced-motion: reduce)"));
  assert.match(reduced, /^@media[^{]*\{\s*\*,\s*\*::before,\s*\*::after \{/);
  for (const property of ["animation-duration", "transition-duration"]) {
    assert.match(reduced, new RegExp(`${property}: 0\\.01ms !important`));
  }
  assert.match(reduced, /animation-iteration-count: 1 !important/);
});

// A status update or refresh rebuilds the grid with the same cards: none may
// ease in again, or the store flickers through every install. A new list (the
// grid emptied by a tab switch) eases all its cards in, a widened filter only
// the cards it brings back, and a page that is itself easing in (launch, Back
// from details, back from Activity) carries its cards, so none moves twice.
test("a rebuild eases in only the cards it did not show, and none while the page enters", () => {
  const items = ["a", "b", "c"].map((itemName) => item({ itemName }));
  const names = (set) => [...set].sort();
  assert.deepEqual(names(enteringItems([], items, false)), ["a", "b", "c"], "a new list");
  assert.deepEqual(names(enteringItems(["a", "b", "c"], items, false)), [], "the same cards again");
  assert.deepEqual(names(enteringItems(["b"], items, false)), ["a", "c"], "a widened filter");
  assert.deepEqual(names(enteringItems([], items, true)), [], "the page enters with them");
});
