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
  forwardedScroll,
  statusLabel,
  statusRecord,
  appliesToLine,
  itemPlacements,
  itemCancel,
  morphFrom,
  openControl,
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

test("the applies-to filter's label is the admin's when set, else Works in", () => {
  assert.equal(brandingView({ appliesToFilterLabel: "Harness" }).appliesToFilterLabel, "Harness");
  for (const payload of [null, {}, { appliesToFilterLabel: " " }, { appliesToFilterLabel: 3 }]) {
    assert.equal(brandingView(payload).appliesToFilterLabel, "Works in", JSON.stringify(payload));
  }
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

// The bar moves and the categories become a list only when asked: no key, an
// older service or a typo in a cached payload must keep the look from before
// the keys, the bar at the top and the Category select.
test("the nav placement is top or left and the category filter select or list; anything else is the first", () => {
  assert.equal(brandingView({ navPlacement: "left" }).navPlacement, "left");
  for (const navPlacement of [undefined, "", "top", "LEFT", "sidebar", 1, "constructor"]) {
    assert.equal(brandingView({ navPlacement }).navPlacement, "top", String(navPlacement));
  }
  assert.equal(brandingView({ categoryFilter: "list" }).categoryFilter, "list");
  for (const categoryFilter of [undefined, "", "select", "LIST", "chips", 1, "constructor"]) {
    assert.equal(brandingView({ categoryFilter }).categoryFilter, "select", String(categoryFilter));
  }
});

// style.css moves the filters only for left; anything else (no key, an older
// service, a typo in a cached payload) must keep the default, right.
test("the filter placement is right or left; anything else is right", () => {
  assert.equal(brandingView({ filterPlacement: "left" }).filterPlacement, "left");
  assert.equal(brandingView({ filterPlacement: "right" }).filterPlacement, "right");
  for (const filterPlacement of [undefined, "", "center", "LEFT", 1, "constructor"]) {
    assert.equal(brandingView({ filterPlacement }).filterPlacement, "right", String(filterPlacement));
  }
});

// Running work moves into the buttons only for button; anything else (no key,
// an older service, a cached payload from before the key) keeps the strip.
test("progress is strip or button; anything else is strip", () => {
  assert.equal(brandingView({ progress: "button" }).progress, "button");
  for (const progress of [undefined, "", "strip", "BUTTON", "both", 1, "constructor"]) {
    assert.equal(brandingView({ progress }).progress, "strip", String(progress));
  }
});

// The detail page is the view from before item_view, so no key, an older
// service, a cached payload or a typo must all keep it.
test("the item view is page or dialog; anything else is page", () => {
  assert.equal(brandingView({ itemView: "dialog" }).itemView, "dialog");
  assert.equal(brandingView({ itemView: "page" }).itemView, "page");
  for (const itemView of [undefined, "", "modal", "DIALOG", 1, "constructor"]) {
    assert.equal(brandingView({ itemView }).itemView, "page", String(itemView));
  }
});

// The window's own scrollbar is the look from before the key, so no key, an
// older service, a cached payload or a typo must all keep it.
test("the scrollbar is system or slim; anything else is system", () => {
  assert.equal(brandingView({ scrollbar: "slim" }).scrollbar, "slim");
  assert.equal(brandingView({ scrollbar: "system" }).scrollbar, "system");
  for (const scrollbar of [undefined, "", "thin", "SLIM", 1, "constructor"]) {
    assert.equal(brandingView({ scrollbar }).scrollbar, "system", String(scrollbar));
  }
});

// Under slim the root does not scroll, so progress: button's stable gutter on
// it would be a blank strip beside the banner; the slim root drops it.
test("a slim root that does not scroll keeps no gutter of its own", () => {
  const css = readFileSync(new URL("./style.css", import.meta.url), "utf8");
  assert.match(css, /:root\[data-progress="button"\] \{[^}]*scrollbar-gutter: stable;/);
  assert.match(css, /@media \(min-height: 30rem\) \{\s*:root\[data-scrollbar="slim"\] \{[^}]*overflow: hidden;[^}]*scrollbar-gutter: auto;/);
});

// Under slim only the content scrolls, and the web view scrolls only an
// ancestor of the focused element. A keyboard user must still scroll the list
// from launch (focus on the body) and from a tab in the app bar, while Space
// on a button still presses it and a text field keeps its own keys.
test("scroll keys reach the content from the body and the bar, and only there", () => {
  const key = (k, overrides) => ({ key: k, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...overrides });
  for (const target of ["body", "control"]) {
    assert.equal(forwardedScroll(key("PageDown"), target, 400), 350, target);
    assert.equal(forwardedScroll(key("PageUp"), target, 400), -350, target);
    assert.equal(forwardedScroll(key("End"), target, 400), "end", target);
    assert.equal(forwardedScroll(key("Home"), target, 400), "start", target);
  }
  assert.equal(forwardedScroll(key(" "), "body", 400), 350);
  assert.equal(forwardedScroll(key(" ", { shiftKey: true }), "body", 400), -350);
  assert.equal(forwardedScroll(key("ArrowDown"), "body", 400), 40);
  assert.equal(forwardedScroll(key("ArrowUp"), "body", 400), -40);
  for (const k of [" ", "ArrowDown", "ArrowUp"]) {
    assert.equal(forwardedScroll(key(k), "control", 400), null, `${JSON.stringify(k)} on a button is the button's`);
  }
  for (const k of ["PageDown", "End", " ", "ArrowDown"]) {
    assert.equal(forwardedScroll(key(k), "own", 400), null, `${JSON.stringify(k)} in a field or a scroller`);
    assert.equal(forwardedScroll(key(k, { altKey: true }), "body", 400), null, `Alt+${k}`);
  }
  assert.equal(forwardedScroll(key("a"), "body", 400), null);
});

// The chords people use to reach the top and the bottom must work as when the
// window scrolls; chords the window ignores, such as Shift+End, must not scroll.
test("only the window's own top and bottom chords reach the content", () => {
  const key = (k, overrides) => ({ key: k, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...overrides });
  for (const target of ["body", "control"]) {
    assert.equal(forwardedScroll(key("End", { ctrlKey: true }), target, 400), "end", target);
    assert.equal(forwardedScroll(key("Home", { ctrlKey: true }), target, 400), "start", target);
  }
  assert.equal(forwardedScroll(key("ArrowDown", { metaKey: true }), "body", 400), "end");
  assert.equal(forwardedScroll(key("ArrowUp", { metaKey: true }), "body", 400), "start");
  assert.equal(forwardedScroll(key("ArrowDown", { metaKey: true }), "control", 400), null);
  for (const k of ["End", "Home", "PageDown", "ArrowDown"]) {
    assert.equal(forwardedScroll(key(k, { shiftKey: true }), "body", 400), null, `Shift+${k}`);
    assert.equal(forwardedScroll(key(k, { metaKey: true, ctrlKey: true }), "body", 400), null, `Ctrl+Meta+${k}`);
  }
  assert.equal(forwardedScroll(key("End", { ctrlKey: true, shiftKey: true }), "body", 400), null, "Ctrl+Shift+End");
});

// Keyboard and screen-reader users open an item with one button on its card,
// and closing the item gives that button focus back: Details on a page, the
// item's name in dialog mode, where the card has no Details button.
test("the card's control that opens an item follows the item view", () => {
  assert.equal(openControl("page"), "card-details");
  assert.equal(openControl("dialog"), "card-open");
});

// The modal dialog makes the strip's Cancel unreachable, so the dialog shows
// its own for the item's running operation; the detail page keeps main's
// layout, where the strip has the only Cancel. A cancel's own busy button is
// already the way out, and a finished operation has nothing to cancel.
test("the item's own Cancel shows in the dialog or under progress button while its operation runs", () => {
  const operation = (outcome, kind = "do") => ({
    operationId: "op",
    item: item({}),
    action: { kind, label: "Install", method: "InstallItem" },
    records: [],
    outcome,
  });
  const running = operation("active");
  // Every combination of item_view and progress: only a page under strip,
  // where the strip's Cancel is in reach and the button shows no progress,
  // has no Cancel of its own.
  for (const [itemView, progress, shown] of [
    ["page", "strip", false],
    ["page", "button", true],
    ["dialog", "strip", true],
    ["dialog", "button", true],
  ]) {
    assert.equal(itemCancel(itemView, progress, running), shown ? running : undefined, `${itemView} ${progress}`);
    assert.equal(itemCancel(itemView, progress, operation("active", "cancel")), undefined, "a cancel");
    for (const outcome of ["terminal", "error"]) {
      assert.equal(itemCancel(itemView, progress, operation(outcome)), undefined, outcome);
    }
    assert.equal(itemCancel(itemView, progress, undefined), undefined, "no operation");
  }
  assert.equal(itemCancel("dialog", "strip", operation("active", "undo")).action.kind, "undo");
});

// The dialog grows out of its card only when the user can see the card; a
// card scrolled out of view, even partly, would make it fly in from off screen.
test("the dialog morphs from its card only when the card is wholly on screen", () => {
  const viewport = { width: 1000, height: 800 };
  const dialog = { left: 200, top: 100, width: 600, height: 400 };
  assert.equal(
    morphFrom({ left: 20, top: 300, width: 300, height: 100 }, dialog, viewport),
    "translate(-180px, 200px) scale(0.5, 0.25)",
  );
  for (const [card, why] of [
    [{ left: 20, top: -150, width: 300, height: 100 }, "scrolled above the window"],
    [{ left: 20, top: -50, width: 300, height: 100 }, "partly above the window"],
    [{ left: 20, top: 750, width: 300, height: 100 }, "partly below the window"],
    [{ left: 20, top: 900, width: 300, height: 100 }, "below the window"],
    [{ left: 900, top: 300, width: 300, height: 100 }, "past the right edge"],
    [{ left: 20, top: 300, width: 0, height: 0 }, "not laid out"],
  ]) {
    assert.equal(morphFrom(card, dialog, viewport), null, why);
  }
  assert.equal(morphFrom({ left: 20, top: 300, width: 300, height: 100 }, { ...dialog, width: 0 }, viewport), null);
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
test("the banner height is one of five names with a rule each; anything else is medium", () => {
  const css = readFileSync(new URL("./style.css", import.meta.url), "utf8");
  for (const bannerHeight of ["small", "medium", "large", "x-large", "fit"]) {
    assert.equal(brandingView({ bannerHeight }).bannerHeight, bannerHeight);
    if (bannerHeight !== "medium") {
      assert.ok(css.includes(`#banner[data-height="${bannerHeight}"]`), bannerHeight);
    }
  }
  for (const bannerHeight of [undefined, "", "Large", "Fit", "xx-large", "300px", 2, "constructor"]) {
    assert.equal(brandingView({ bannerHeight }).bannerHeight, "medium", String(bannerHeight));
  }
});

// The banner, the app bar and the heading row are pinned one under another by
// heights main.ts measures; a height the stylesheet reads and nothing sets
// would pin a row over the one above it.
test("main.ts sets every height the pinned rows are placed by", () => {
  const css = readFileSync(new URL("./style.css", import.meta.url), "utf8");
  const shell = readFileSync(new URL("./main.ts", import.meta.url), "utf8");
  const read = new Set([...css.matchAll(/var\((--[a-z-]+-height)\b/g)].map(([, name]) => name));
  assert.deepEqual([...read].sort(), ["--app-bar-height", "--banner-height", "--head-height"]);
  for (const name of read) {
    assert.ok(shell.includes(`setProperty("${name}"`), name);
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

// The applies-to filter is the short one: ahead of the categories it stays in
// view in the left bar however many categories a list has.
test("the applies-to filter comes before the categories, in the bar and beside the search field", () => {
  const html = readFileSync(new URL("../index.html", import.meta.url), "utf8");
  const at = (id) => {
    const index = html.indexOf(`id="${id}"`);
    assert.notEqual(index, -1, `no #${id}`);
    return index;
  };
  assert.ok(at("applies-to-nav") < at("category-nav"));
  assert.ok(at("search") < at("applies-to-field"));
  assert.ok(at("applies-to-field") < at("category-field"));
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

// Every refresh has the service fetch the repository, so only a negative
// value from the service turns the timer off, and nothing a payload holds
// makes it run faster than once a minute.
test("the refresh interval is ten minutes unless the service says otherwise", () => {
  for (const refreshIntervalSeconds of [undefined, 0, "", "60", null, NaN, Infinity]) {
    assert.equal(brandingView({ refreshIntervalSeconds }).refreshIntervalMs, 600_000, String(refreshIntervalSeconds));
  }
  assert.equal(brandingView(null).refreshIntervalMs, 600_000, "no payload, as from an empty cache");
  assert.equal(brandingView({ refreshIntervalSeconds: 3600 }).refreshIntervalMs, 3_600_000);
  assert.equal(brandingView({ refreshIntervalSeconds: 1 }).refreshIntervalMs, 60_000, "never under a minute");
  assert.equal(brandingView({ refreshIntervalSeconds: -1 }).refreshIntervalMs, 0, "off");
  assert.equal(brandingView({ refreshIntervalSeconds: 1e12 }).refreshIntervalMs, 2_147_483_000, "what a timer can hold");
});
