import assert from "node:assert/strict";
import test from "node:test";

import {
  ALL_CATEGORIES,
  REQUIRED_LABEL,
  REQUESTED_STATE,
  ERROR_STATE,
  STREAM_ENDED_STATE,
  NOT_AUTHORIZED_TEXT,
  activityLine,
  addActivity,
  bannerMessage,
  connectionLabel,
  brandingView,
  canCancel,
  cancelTitle,
  cardProgress,
  categories,
  categoryGlyph,
  compareItems,
  deriveAction,
  fromCache,
  glyphTone,
  httpUrl,
  iconKey,
  iconSource,
  insertRecord,
  isHexColor,
  isItemActive,
  isRefreshKey,
  isItemPhase,
  isProgressTick,
  isTerminalState,
  localErrorState,
  localRecord,
  repeatsOutcome,
  requestText,
  monogram,
  myItems,
  actionText,
  busyAction,
  endedOperations,
  needsAttention,
  onAccent,
  progressLabel,
  progressText,
  refusalText,
  releaseOperation,
  resolveTab,
  requestErrorText,
  restartBadge,
  shouldAcceptRecord,
  showLoading,
  showRetry,
  pageSplashMs,
  splashLeft,
  splashMs,
  splashShownBefore,
  stateLabel,
  statusLabel,
  statusRecord,
  stripLine,
  stripView,
  actionProgress,
  announcement,
  queueView,
  tabItems,
  tabs,
  trackOperation,
  visibleItems,
  appliesToChoices,
  ALL_APPLIES_TO,
  withFailure,
  withLive,
} from "./state.ts";

function item(overrides) {
  return {
    itemName: "Item",
    displayName: "Item",
    version: "1.0.0",
    catalog: "catalog",
    isManaged: false,
    isInstalled: false,
    status: "NotInstalled",
    statusUpdatedAtUtc: "2026-07-21T12:00:00Z",
    ...overrides,
  };
}

const catalogItems = [
  item({ itemName: "zed", displayName: "Zed Editor", category: "Development", developer: "Zed" }),
  item({ itemName: "acme", displayName: "acme reader", category: "Productivity" }),
  item({ itemName: "brave", displayName: "Brave", category: "browsers", developer: "Acme Corp" }),
];

test("search is case-insensitive across name, item, developer and category", () => {
  assert.deepEqual(
    visibleItems(catalogItems, "ACME", ALL_CATEGORIES).map((i) => i.itemName),
    ["acme", "brave"],
  );
  assert.deepEqual(
    visibleItems(catalogItems, "development", ALL_CATEGORIES).map((i) => i.itemName),
    ["zed"],
  );
  assert.equal(visibleItems(catalogItems, "   ", ALL_CATEGORIES).length, 3);
  assert.equal(visibleItems(catalogItems, "nothing-here", ALL_CATEGORIES).length, 0);
});

test("category filter is case-insensitive and 'all' keeps everything", () => {
  assert.deepEqual(
    visibleItems(catalogItems, "", "BROWSERS").map((i) => i.itemName),
    ["brave"],
  );
  assert.equal(visibleItems(catalogItems, "", ALL_CATEGORIES).length, 3);
});

test("the applies-to filter keeps the items that list the name, and search finds them by it", () => {
  const skills = [
    item({ itemName: "both", appliesTo: ["Claude Code", "Codex"] }),
    item({ itemName: "codex", appliesTo: ["codex ", "", 3] }),
    item({ itemName: "plain" }),
  ];
  assert.deepEqual(appliesToChoices(skills), ["Claude Code", "Codex"]);
  assert.deepEqual(visibleItems(skills, "", ALL_CATEGORIES, "Claude Code").map((i) => i.itemName), ["both"]);
  assert.deepEqual(visibleItems(skills, "", ALL_CATEGORIES, "CODEX").map((i) => i.itemName), ["both", "codex"]);
  assert.equal(visibleItems(skills, "", ALL_CATEGORIES, ALL_APPLIES_TO).length, 3);
  assert.deepEqual(visibleItems(skills, "claude", ALL_CATEGORIES).map((i) => i.itemName), ["both"]);
  // One name, or none, is nothing to choose between.
  assert.deepEqual(appliesToChoices(skills.slice(1)), []);
  assert.deepEqual(appliesToChoices(catalogItems), []);
});

test("card order is stable, case-insensitive by display name then item name", () => {
  assert.deepEqual(
    visibleItems(catalogItems, "", ALL_CATEGORIES).map((i) => i.displayName),
    ["acme reader", "Brave", "Zed Editor"],
  );
  const tie = [item({ itemName: "b", displayName: "Same" }), item({ itemName: "a", displayName: "same" })];
  assert.deepEqual(tie.slice().sort(compareItems).map((i) => i.itemName), ["a", "b"]);
});

test("filtering does not mutate the source list", () => {
  const source = catalogItems.slice();
  visibleItems(source, "", ALL_CATEGORIES);
  assert.deepEqual(source.map((i) => i.itemName), ["zed", "acme", "brave"]);
});

test("categories are unique, non-empty and sorted", () => {
  const withDuplicates = [...catalogItems, item({ itemName: "x", category: "development" }), item({ itemName: "y" })];
  assert.deepEqual(categories(withDuplicates), ["browsers", "Development", "Productivity"]);
});

const sectioned = [
  item({ itemName: "s1", section: "skills", category: "Writing" }),
  item({ itemName: "p1", section: "Plugins", category: "Git" }),
  item({ itemName: "s2", section: " Skills ", category: "Review" }),
  item({ itemName: "a1", section: "Agents" }),
  item({ itemName: "u1", category: "Browsers" }),
];

test("tabs put the admin's sections first, in order, then the rest alphabetically", () => {
  assert.deepEqual(
    tabs(sectioned, ["Skills"]).map((tab) => tab.label),
    ["Skills", "Agents", "Plugins", "Software"],
  );
  // A listed section no item uses has no tab: an empty tab would be a dead end.
  assert.deepEqual(
    tabs(sectioned, ["Tools", "plugins", "PLUGINS"]).map((tab) => tab.label),
    ["plugins", "Agents", "skills", "Software"],
  );
});

test("a tab is labelled as the admin wrote it, else as its first item wrote it", () => {
  assert.deepEqual(tabs(sectioned, []), [
    { key: "agents", label: "Agents" },
    { key: "plugins", label: "Plugins" },
    { key: "skills", label: "skills" },
    { key: "", label: "Software" },
  ]);
  assert.equal(tabs(sectioned, ["SKILLS"])[0].label, "SKILLS");
});

test("the Software tab is there for unsectioned items, or when nothing has a section", () => {
  const allSectioned = sectioned.filter((i) => i.section);
  assert.deepEqual(tabs(allSectioned, []).map((tab) => tab.key), ["agents", "plugins", "skills"]);
  // A catalog with no sections, or no list yet, looks as it always has.
  assert.deepEqual(tabs([], ["Skills"]), [{ key: "", label: "Software" }]);
  assert.deepEqual(tabs(catalogItems, []), [{ key: "", label: "Software" }]);
  // A blank or malformed cached section counts as none.
  assert.deepEqual(tabs([item({ section: "  " }), item({ section: 3 })], []), [{ key: "", label: "Software" }]);
});

test("a stale tab falls back to the first tab", () => {
  const list = tabs(sectioned, []);
  assert.equal(resolveTab(list, "plugins"), "plugins");
  assert.equal(resolveTab(list, ""), "", "the Software tab is a real tab");
  // A section that vanished after a refresh, or nothing chosen yet.
  assert.equal(resolveTab(list, "removed"), "agents");
  assert.equal(resolveTab(list, null), "agents");
  assert.equal(resolveTab(tabs([], []), "skills"), "");
});

test("a tab lists only its own items, so search and categories stay within it", () => {
  assert.deepEqual(tabItems(sectioned, "skills").map((i) => i.itemName), ["s1", "s2"]);
  assert.deepEqual(tabItems(sectioned, "").map((i) => i.itemName), ["u1"]);
  assert.deepEqual(categories(tabItems(sectioned, "skills")), ["Review", "Writing"]);
  assert.deepEqual(visibleItems(tabItems(sectioned, "plugins"), "", "Writing"), []);
});

test("action derivation covers every status and managed combination", () => {
  const cases = [
    ["WillBeInstalled", false, "Cancel", "RemoveItem"],
    ["WillBeInstalled", true, "Cancel", "RemoveItem"],
    ["WillBeRemoved", false, "Cancel", "InstallItem"],
    ["WillBeRemoved", true, "Cancel", "InstallItem"],
    ["Installed", true, "Remove", "RemoveItem"],
    ["Installed", false, "Install", "InstallItem"],
    ["NotInstalled", true, "Remove", "RemoveItem"],
    ["NotInstalled", false, "Install", "InstallItem"],
    ["Unknown", true, "Remove", "RemoveItem"],
    ["Unknown", false, "Install", "InstallItem"],
    ["", true, "Remove", "RemoveItem"],
    ["", false, "Install", "InstallItem"],
  ];
  const kinds = { Cancel: "cancel", Remove: "undo", Install: "do" };
  for (const [status, isManaged, label, method] of cases) {
    const kind = kinds[label];
    assert.deepEqual(
      deriveAction(item({ status, isManaged })),
      { kind, label, method },
      `${status || "(blank)"} isManaged=${isManaged}`,
    );
  }
});

// The service refuses to remove an item an admin manifest requires, so the UI
// must not offer a button that can only fail; it says who manages it instead.
test("a required item has no action and says the organisation manages it", () => {
  for (const status of ["Installed", "NotInstalled", "WillBeInstalled", "WillBeRemoved", "Unknown"]) {
    for (const isManaged of [false, true]) {
      const required = item({ status, isManaged, isRequired: true });
      assert.equal(deriveAction(required), null, `${status} isManaged=${isManaged}`);
      assert.equal(statusLabel(required), "Managed by your organisation");
    }
  }
  assert.equal(REQUIRED_LABEL, "Managed by your organisation");
  assert.deepEqual(deriveAction(item({ status: "Installed", isManaged: true, isRequired: false })), {
    kind: "undo",
    label: "Remove",
    method: "RemoveItem",
  });
});

test("an item's icon is asked for only when the service holds one, and again when it changes", () => {
  assert.equal(iconKey(item({})), "");
  assert.equal(iconKey(item({ iconName: "tool.png" })), "", "a name without a hash is an icon the service does not hold");
  assert.equal(iconKey(item({ iconHash: "abc" })), "");
  assert.equal(iconKey(item({ iconName: "tool.png", iconHash: "abc" })), "abc:tool.png");
  assert.notEqual(
    iconKey(item({ iconName: "tool.png", iconHash: "abc" })),
    iconKey(item({ iconName: "tool.png", iconHash: "def" })),
  );
  // A cached list is untrusted shape-wise.
  assert.equal(iconKey(item({ iconName: 7, iconHash: {} })), "");
});

test("only a PNG or JPEG from the service becomes an image source", () => {
  assert.equal(iconSource({ mime: "image/png", base64: "iVBORw0KGgo=" }), "data:image/png;base64,iVBORw0KGgo=");
  assert.equal(iconSource({ mime: "image/jpeg", base64: "/9j/4AAQ" }), "data:image/jpeg;base64,/9j/4AAQ");
  assert.equal(iconSource({ mime: "", base64: "" }), "", "the service holds no icon");
  assert.equal(iconSource({ mime: "image/svg+xml", base64: "PHN2Zy8+" }), "");
  assert.equal(iconSource({ mime: "text/html", base64: "PGh0bWw+" }), "");
  assert.equal(iconSource({ mime: "image/png", base64: 'x" onerror="alert(1)' }), "");
  assert.equal(iconSource({ mime: "image/png" }), "");
  assert.equal(iconSource(null), "");
});

test("monogram uses initials and falls back to a category glyph", () => {
  assert.equal(monogram(item({ displayName: "Demo Optional Package" })), "DO");
  assert.equal(monogram(item({ displayName: "Brave" })), "B");
  assert.equal(monogram(item({ displayName: "7-Zip" })), "7Z");
  assert.equal(monogram(item({ displayName: "", itemName: "fallback" })), "F");
  assert.equal(monogram(item({ displayName: "***", itemName: "***", category: "Security" })), "🛡");
  assert.equal(monogram(item({ displayName: "***", itemName: "***" })), "▪");
  assert.equal(categoryGlyph(undefined), "▪");
});

test("restart badge is only shown when restartAction is meaningful", () => {
  // "" means the badge element stays hidden; a stray empty pill is a rendering bug.
  assert.equal(restartBadge(item({})), "");
  assert.equal(restartBadge(item({ restartAction: undefined })), "");
  assert.equal(restartBadge(item({ restartAction: "" })), "");
  assert.equal(restartBadge(item({ restartAction: "  " })), "");
  assert.equal(restartBadge(item({ restartAction: "none" })), "");
  assert.equal(restartBadge(item({ restartAction: "None" })), "");
  assert.equal(restartBadge(item({ restartAction: "RequireRestart" })), "Restart required");
  assert.equal(restartBadge(item({ restartAction: "RecommendRestart" })), "Restart recommended");
  assert.equal(restartBadge(item({ restartAction: "SomethingElse" })), "SomethingElse");
});

test("status label stays readable for blank and camel-case values", () => {
  assert.equal(statusLabel(item({ status: "WillBeInstalled" })), "Will be installed");
  assert.equal(statusLabel(item({ status: "NotInstalled" })), "Not installed");
  assert.equal(statusLabel(item({ status: "Installed" })), "Installed");
  assert.equal(statusLabel(item({ status: "" })), "Unknown");
});

test("list view transitions from cache to live and back to stale", () => {
  const empty = fromCache(null);
  assert.equal(empty.source, "loading");
  assert.equal(showRetry(empty), false);
  assert.equal(showLoading(empty), true, "nothing to show before the first answer");

  const cached = fromCache({ savedAtUtc: "2026-07-21T12:00:00Z", items: catalogItems });
  assert.equal(cached.source, "cache");
  assert.equal(cached.items.length, 3);
  assert.match(bannerMessage(cached, ""), /cached/i);
  assert.equal(showRetry(cached), false);
  assert.equal(showLoading(cached), false, "a saved list stands in while connecting");

  const live = withLive([catalogItems[0]], "2026-07-22T00:00:00Z");
  assert.deepEqual(live, {
    items: [catalogItems[0]],
    source: "live",
    savedAtUtc: "2026-07-22T00:00:00Z",
  });
  assert.equal(showRetry(live), false);

  const stale = withFailure(live);
  assert.equal(stale.source, "stale");
  assert.deepEqual(stale.items, live.items, "a failed refresh keeps the previous items");
  assert.equal(stale.savedAtUtc, live.savedAtUtc);
  assert.equal(showRetry(stale), true);
  assert.match(
    bannerMessage(stale, "pipe unavailable"),
    /^Service unavailable — showing cached software from .*\. Reason: pipe unavailable$/,
  );
  assert.equal(
    bannerMessage(stale, ""),
    bannerMessage(stale, "pipe unavailable").replace(" Reason: pipe unavailable", ""),
    "a blank error leaves no dangling label",
  );

  const staleEmpty = withFailure(empty);
  assert.equal(
    bannerMessage(staleEmpty, "pipe unavailable"),
    "Service unavailable and no cached software is stored. Reason: pipe unavailable",
  );
  assert.equal(showRetry(staleEmpty), true);
  assert.equal(showLoading(staleEmpty), false, "a failed first try shows the page and its Retry");
});

test("a splash keeps the loading screen up for its least time, once per launch", () => {
  const empty = fromCache(null);
  const cached = fromCache({ savedAtUtc: "2026-07-21T12:00:00Z", items: catalogItems });
  const live = withLive(catalogItems, "2026-07-22T00:00:00Z");
  const failed = withFailure(empty);

  // Off: elapsed time changes nothing.
  for (const elapsed of [0, 50, 10_000]) {
    assert.equal(showLoading(empty, 0, elapsed), true);
    assert.equal(showLoading(cached, 0, elapsed), false, "no splash: a saved list shows at once");
    assert.equal(showLoading(live, 0, elapsed), false);
    assert.equal(showLoading(failed, 0, elapsed), false);
  }

  const splash = 3000;
  assert.equal(showLoading(live, splash, 100), true, "an instant answer waits out the splash");
  assert.equal(showLoading(live, splash, 3000), false, "and then shows the list");
  assert.equal(showLoading(cached, splash, 2999), true, "a saved list waits out the splash too");
  assert.equal(showLoading(cached, splash, 3000), false);
  assert.equal(showLoading(empty, splash, 2000), true);
  assert.equal(showLoading(empty, splash, 9000), true, "a slow service keeps the screen up past the splash");
  assert.equal(showLoading(failed, splash, 1000), true, "a failed first try shows Retry only after the splash");
  assert.equal(showLoading(failed, splash, 3000), false);
  // Later refreshes, Retry and reconnects only ever see more elapsed time:
  // the splash never comes back, and only a list-less wait raises the screen.
  for (const later of [cached, live, withFailure(live), failed]) {
    assert.equal(showLoading(later, splash, 3001), false);
    assert.equal(showLoading(later, splash, 600_000), false);
  }

  assert.equal(splashMs(undefined, undefined), 0, "off without the setting");
  assert.equal(splashMs("false", "3"), 0);
  assert.equal(splashMs("true", "3"), 3000);
  assert.equal(splashMs("true", "2.5"), 2500);
  assert.equal(splashMs("true", undefined), 3000, "seconds default to 3");
  assert.equal(splashMs("true", "abc"), 3000);
  assert.equal(splashMs("true", ""), 3000);
  assert.equal(splashMs("true", "0"), 100, "clamped up to 0.1 s");
  assert.equal(splashMs("true", "99999999"), 30_000, "clamped down to 30 s, so no timer overflows");
});

test("the splash timer is due when the splash has no time left", () => {
  assert.equal(splashLeft(0, 0), 0, "no splash: nothing to wait for");
  assert.equal(splashLeft(3000, 1200), 1800);
  assert.equal(splashLeft(3000, 3000), 0);
  assert.equal(splashLeft(3000, 9000), 0, "never negative");
  // The screen comes down exactly when the timer is due, for an instant answer.
  const live = withLive(catalogItems, "2026-07-22T00:00:00Z");
  for (const elapsed of [0, 1500, 2999, 3000, 3001]) {
    assert.equal(showLoading(live, 3000, elapsed), splashLeft(3000, elapsed) > 0);
  }
});

test("a reload within one launch gets no splash", () => {
  const session = () => {
    const items = new Map();
    return { getItem: (key) => items.get(key) ?? null, setItem: (key, value) => void items.set(key, String(value)) };
  };
  const store = session();
  const first = splashShownBefore(() => store);
  assert.equal(first, false, "a new launch's page has not had it");
  assert.equal(pageSplashMs(3000, first), 3000);
  const reloaded = splashShownBefore(() => store);
  assert.equal(reloaded, true, "the same session's next page has");
  const splash = pageSplashMs(3000, reloaded);
  assert.equal(splash, 0);
  // A reload behaves as a build without the option: a saved list shows at
  // once, and the waiting screen keeps its 200 ms (no data-always).
  assert.equal(showLoading(fromCache({ savedAtUtc: "2026-07-21T12:00:00Z", items: catalogItems }), splash, 0), false);
  assert.equal(showLoading(fromCache(null), splash, 0), true);
  assert.equal(splashLeft(splash, 0) > 0, false);
  assert.equal(splashLeft(pageSplashMs(3000, first), 0) > 0, true, "a new page's splash shows its content at once");

  assert.equal(splashShownBefore(() => session()), false, "a new session (a new launch) shows it again");
  const throwing = () => {
    throw new Error("SecurityError");
  };
  assert.equal(splashShownBefore(throwing), false, "storage that cannot be reached counts as unmarked");
  assert.equal(splashShownBefore(() => ({ getItem: throwing, setItem: throwing })), false);
  assert.equal(
    splashShownBefore(() => ({ getItem: () => "1", setItem: throwing })),
    true,
    "a full store still remembers the mark it read",
  );
});

test("only Succeeded, Failed, Deferred and Canceled end an operation", () => {
  for (const state of ["Succeeded", "Failed", "Deferred", "Canceled"]) {
    assert.equal(isTerminalState(state), true, state);
  }
  // A dependency failing or finishing must never be read as the operation result.
  for (const state of ["Queued", "Downloading", "Installing", "Removing", "ItemCompleted", "ItemFailed", "Requested", "", "succeeded"]) {
    assert.equal(isTerminalState(state), false, state || "(blank)");
  }
});

test("a determinate bar is only offered for item-phase records", () => {
  for (const state of ["Downloading", "Installing", "Removing"]) {
    assert.equal(isItemPhase(state), true, state);
  }
  for (const state of ["Queued", "ItemCompleted", "ItemFailed", "Succeeded", "Failed", "Deferred", "Canceled", ""]) {
    assert.equal(isItemPhase(state), false, state || "(blank)");
  }
});

test("active operations disable only their own item and route independently", () => {
  let active = new Map();
  active = trackOperation(active, "op-1", "DemoOptional");
  active = trackOperation(active, "op-2", "DemoFailing");

  assert.equal(isItemActive(active, "DemoOptional"), true);
  assert.equal(isItemActive(active, "DemoFailing"), true);
  assert.equal(isItemActive(active, "DemoBlocked"), false);

  const afterFirst = releaseOperation(active, "op-1");
  assert.equal(isItemActive(afterFirst, "DemoOptional"), false);
  assert.equal(isItemActive(afterFirst, "DemoFailing"), true, "a concurrent operation keeps its own item busy");
  assert.equal(isItemActive(active, "DemoOptional"), true, "release does not mutate the previous map");
  assert.equal(afterFirst.get("op-2"), "DemoFailing");
  assert.equal(releaseOperation(afterFirst, "op-unknown").size, 1);
});

test("an item is busy from the click until the list after its end arrives", () => {
  const install = { kind: "do", label: "Make available", method: "InstallItem" };
  const requesting = new Map([["Skill", install]]);
  const operations = new Map();

  // Before the service answers: a second click must find the item busy.
  assert.equal(busyAction("Skill", requesting, new Map(), operations), install);
  assert.equal(busyAction("Other", requesting, new Map(), operations), undefined);

  requesting.delete("Skill");
  const active = trackOperation(new Map(), "op-1", "Skill");
  const operation = { operationId: "op-1", action: install, records: [], outcome: "active" };
  operations.set("op-1", operation);
  assert.equal(busyAction("Skill", requesting, active, operations), install);
  assert.deepEqual(endedOperations(active, operations), []);

  // Ended, but the refreshed list is not in yet: still busy, so the old
  // "Make available" cannot be clicked again before "Remove" shows.
  operation.outcome = "terminal";
  assert.equal(busyAction("Skill", requesting, active, operations), install);
  assert.deepEqual(endedOperations(active, operations), ["op-1"]);
  assert.equal(busyAction("Skill", requesting, releaseOperation(active, "op-1"), operations), undefined);
});

test("a busy button reads the item's own installing or removing label", () => {
  const custom = { actionLabels: { installing: "Making available…", removing: "Withdrawing…" } };
  const install = { kind: "do", label: "Make available", method: "InstallItem" };
  const remove = { kind: "undo", label: "Withdraw", method: "RemoveItem" };

  assert.equal(actionText(custom, install, undefined), "Make available");
  assert.equal(actionText(custom, install, install), "Making available…");
  assert.equal(actionText(custom, remove, remove), "Withdrawing…");
  assert.equal(actionText({}, install, install), "Installing…", "built-in without a custom label");

  // Placement rows: only the row the work is for reads busy.
  const rowA = { ...install, placement: "a" };
  const rowB = { ...install, placement: "b" };
  assert.equal(actionText(custom, rowA, rowA, "a"), "Making available…");
  assert.equal(actionText(custom, rowB, rowA, "b"), "Make available");
  assert.equal(actionText(custom, rowB, install, "b"), "Making available…", "a whole-item install covers each row");
  assert.equal(actionText(custom, { ...remove, placement: "b" }, install, "b"), "Withdraw");
  assert.equal(actionText(custom, install, rowA), "Making available…", "the item's own button follows any work");

  // Cancelling a pending install sends RemoveItem; it must not read "Withdrawing…".
  const cancel = { kind: "cancel", label: "Cancel", method: "RemoveItem" };
  assert.equal(actionText(custom, cancel, cancel), "Cancel");
});

test("records are filed by seq whatever order they arrive in", () => {
  // Mirrors the onOperationStatus handler: Wails can deliver records out of
  // order, even after the terminal one, and a finished operation must not
  // read "Installing 55%" as its newest line.
  const operation = { records: [], outcome: "active" };
  const apply = (status) => {
    if (!shouldAcceptRecord(operation.outcome)) {
      return false;
    }
    const record = statusRecord(status);
    if (!insertRecord(operation.records, record)) {
      return false;
    }
    if (isTerminalState(record.state)) {
      operation.outcome = "terminal";
    }
    return true;
  };

  const event = (seq, state, progressPercent) => ({
    operationId: "op-1",
    seq,
    timestampUtc: "2026-07-21T12:00:00.020Z",
    itemName: "DemoOptional",
    displayName: "Demo Optional",
    state,
    progressPercent,
    message: state,
  });

  insertRecord(operation.records, localRecord("op-1", event(0, "", 0), REQUESTED_STATE, "accepted", "2026-07-21T12:00:00.000Z"));
  apply(event(1, "Queued", 0));
  apply(event(3, "Installing", 55));
  apply(event(4, "Succeeded", 100));
  apply(event(2, "Downloading", 0));
  assert.equal(apply(event(3, "Installing", 55)), false, "a repeated seq is dropped");

  assert.deepEqual(
    operation.records.map((r) => r.state),
    [REQUESTED_STATE, "Queued", "Downloading", "Installing", "Succeeded"],
  );
  assert.equal(operation.outcome, "terminal");
  assert.equal(shouldAcceptRecord("active"), true);
  assert.equal(shouldAcceptRecord("terminal"), true, "a late record still belongs in the finished timeline");
  assert.equal(shouldAcceptRecord("error"), false, "after a local error its own record stays the latest");
});

test("Activity is newest first by time, then by seq within an operation", () => {
  const record = (operationId, seq, timestampUtc, state) => ({
    operationId,
    itemName: "DemoOptional",
    displayName: "Demo Optional",
    state,
    message: "",
    timestampUtc,
    ...(seq === undefined ? {} : { seq }),
  });
  let activity = [];
  // Arrival order, as Wails might deliver it: a batch inverted, then a
  // record of another operation, then a late one.
  activity = addActivity(activity, record("op-1", undefined, "2026-07-21T12:00:00.000Z", REQUESTED_STATE), 100);
  activity = addActivity(activity, record("op-1", 2, "2026-07-21T12:00:01.500Z", "Downloading"), 100);
  activity = addActivity(activity, record("op-1", 1, "2026-07-21T12:00:01.500Z", "Queued"), 100);
  activity = addActivity(activity, record("op-2", 1, "2026-07-21T12:00:02.000Z", "Queued"), 100);
  activity = addActivity(activity, record("op-1", 3, "2026-07-21T12:00:03.000Z", "Succeeded"), 100);

  assert.deepEqual(
    activity.map((r) => `${r.operationId}:${r.state}`),
    ["op-1:Succeeded", "op-2:Queued", "op-1:Downloading", "op-1:Queued", `op-1:${REQUESTED_STATE}`],
  );
  assert.equal(addActivity(activity, record("op-3", 1, "2026-07-21T12:00:04.000Z", "Queued"), 2).length, 2);
});

test("status records keep the event's own item identity and percentage", () => {
  const dependency = statusRecord({
    operationId: "op-1",
    seq: 5,
    timestampUtc: "2026-07-21T12:00:05Z",
    itemName: "DemoUpdater",
    displayName: "Demo Updater",
    state: "Installing",
    progressPercent: 10,
    message: "Installing DemoUpdater",
  });
  assert.deepEqual(dependency, {
    operationId: "op-1",
    itemName: "DemoUpdater",
    displayName: "Demo Updater",
    state: "Installing",
    message: "Installing DemoUpdater",
    timestampUtc: "2026-07-21T12:00:05Z",
    seq: 5,
    progressPercent: 10,
  });
  assert.equal(progressLabel(dependency), "Demo Updater — Installing");

  const blankName = statusRecord({
    operationId: "op-1",
    timestampUtc: "2026-07-21T12:00:06Z",
    itemName: "DemoOptional",
    displayName: "",
    state: "ItemFailed",
    progressPercent: 0,
    message: "install failed",
    errorMessage: "installer exited with code 1",
  });
  assert.equal(blankName.displayName, "DemoOptional");
  assert.equal(blankName.message, "install failed (installer exited with code 1)");
  assert.equal(blankName.detail, "installer exited with code 1", "the bare error is kept for outcome lines");
  assert.equal(isTerminalState(blankName.state), false, "ItemFailed stays non-terminal");

  const duplicated = statusRecord({
    operationId: "op-1",
    timestampUtc: "2026-07-21T12:00:07Z",
    itemName: "DemoOptional",
    displayName: "Demo Optional",
    state: "Failed",
    progressPercent: 100,
    message: "boom",
    errorMessage: "boom",
  });
  assert.equal(duplicated.message, "boom", "an identical errorMessage is not repeated");

  // The service leaves the percent off a phase it did not measure. The record
  // must stay unmeasured, so the strip's bar is indeterminate and its line
  // reads "Installing…" rather than a false "Installing… 0%".
  const unmeasured = statusRecord({
    operationId: "op-1",
    seq: 8,
    timestampUtc: "2026-07-21T12:00:08Z",
    itemName: "DemoOptional",
    displayName: "Demo Optional",
    state: "Installing",
    message: "",
  });
  assert.equal("progressPercent" in unmeasured, false);
  const progress = cardProgress({ itemName: "DemoOptional" }, [unmeasured], "active");
  assert.equal(progress.percent, undefined);
  assert.equal(progressText(progress), "Installing…");
});

test("local records label request failures and premature stream ends", () => {
  assert.equal(localErrorState("operation stream ended before a terminal event"), STREAM_ENDED_STATE);
  assert.equal(localErrorState("pipe unavailable"), ERROR_STATE);

  const record = localRecord(
    "local-1",
    { itemName: "DemoOptional", displayName: "" },
    ERROR_STATE,
    "pipe unavailable",
    "2026-07-21T12:00:00Z",
  );
  assert.deepEqual(record, {
    operationId: "local-1",
    itemName: "DemoOptional",
    displayName: "DemoOptional",
    state: ERROR_STATE,
    message: "pipe unavailable",
    timestampUtc: "2026-07-21T12:00:00Z",
  });
  assert.equal(record.progressPercent, undefined, "a local record carries no service percentage");
});

test("state labels stay readable for wire and local states", () => {
  assert.equal(stateLabel("ItemCompleted"), "Item Completed");
  assert.equal(stateLabel(STREAM_ENDED_STATE), "Stream ended before a result");
  assert.equal(stateLabel(ERROR_STATE), "Request failed");
  assert.equal(stateLabel(""), "Unknown");
  assert.equal(statusLabel(item({ status: "WillBeRemoved" })), "Will be removed");
});

test("activity lines say what happened in plain words, with a reason only where there is one", () => {
  // The messages are the ones the service and main.ts really send: a person
  // reads Activity, so a stock message or the item's name again is noise.
  const text = (state, message, extra = {}) =>
    activityLine({
      operationId: "op-1",
      itemName: "DemoOptional",
      displayName: "Demo Optional",
      state,
      message,
      timestampUtc: "not-a-date",
      ...extra,
    }).replace("not-a-date — Demo Optional: ", "");

  assert.equal(text(REQUESTED_STATE, "Install requested"), "Install requested");
  assert.equal(text("Queued", "Operation queued"), "Waiting to start");
  assert.equal(text("Downloading", "Demo Optional"), "Downloading");
  assert.equal(text("Installing", "DemoOptional"), "Installing");
  assert.equal(text("Removing", ""), "Removing");
  assert.equal(text("ItemCompleted", ""), "Done");
  assert.equal(text("ItemFailed", "exit status 1603"), "Failed — exit status 1603");
  assert.equal(text("Succeeded", "Operation completed"), "Finished");
  assert.equal(
    text("Failed", "Operation failed (exit status 1603)", { detail: "exit status 1603" }),
    "Failed — exit status 1603",
  );
  assert.equal(text("Failed", "Operation failed"), "Failed");
  assert.equal(
    text("Deferred", "blocking application(s) running: demo.exe", { errorCode: "blocked_by_running_app" }),
    "Waiting — close demo.exe to continue",
  );
  assert.equal(
    text("Deferred", "Needs Python. Install it first, then try again.", { errorCode: "missing_software" }),
    "Postponed — Needs Python. Install it first, then try again.",
  );
  assert.equal(
    text(STREAM_ENDED_STATE, "operation stream ended before a terminal event"),
    "Lost contact with the service before it finished",
  );
  assert.equal(
    text(ERROR_STATE, "command_failed: the service could not complete the request; see the SofCat service log"),
    "Request failed — the service could not complete the request; see the SofCat service log",
  );
  assert.equal(text(ERROR_STATE, "pipe unavailable"), "Request failed — pipe unavailable");
  // A state this build does not know still reads, with whatever the service said.
  assert.equal(text("VerifyingSignature", "checking"), "Verifying Signature — checking");

  assert.equal(
    activityLine({
      operationId: "op-1",
      itemName: "DemoUpdater",
      displayName: "",
      state: "Queued",
      message: "  ",
      timestampUtc: "not-a-date",
    }),
    "not-a-date — DemoUpdater: Waiting to start",
  );
});

test("Activity leaves out the own item's completion, which the terminal record repeats", () => {
  const own = { itemName: "DemoOptional" };
  const record = (itemName, state) => ({ itemName, state });
  assert.equal(repeatsOutcome(own, record("DemoOptional", "ItemCompleted")), true);
  assert.equal(repeatsOutcome(own, record("DemoOptional", "ItemFailed")), true);
  assert.equal(repeatsOutcome(own, record("DemoUpdater", "ItemFailed")), false, "a dependency's result is news");
  assert.equal(repeatsOutcome(own, record("DemoOptional", "Failed")), false, "the terminal record stays");

  // A zip item whose user placement the helper finishes: the service sends a
  // second Installing after the item's ItemCompleted. With that record left
  // out, the second Installing must not show as a new line (pushRecord's rule).
  const records = ["Downloading", "Installing", "ItemCompleted", "Installing"].map((state) =>
    record("DemoOptional", state),
  );
  const shown = records.filter((filed) => !repeatsOutcome(own, filed));
  assert.equal(isProgressTick(records, records[3]), false);
  assert.equal(isProgressTick(shown, records[3]), true);
});

test("a request line names the placement a row's button was for", () => {
  const skill = { placements: [{ name: "claude-user", label: "Claude Code", status: "NotInstalled" }] };
  const install = { kind: "do", label: "Install", method: "InstallItem" };
  assert.equal(requestText(skill, install), "Install requested");
  assert.equal(requestText(skill, { ...install, placement: "claude-user" }), "Install requested for Claude Code");
  assert.equal(requestText({}, { ...install, placement: "gone" }), "Install requested");
});

test("card progress shows one line and a bar while active, then only outcomes worth acting on", () => {
  const chrome = { itemName: "GoogleChrome" };
  const record = (overrides) => ({
    operationId: "op-1",
    itemName: "GoogleChrome",
    displayName: "Google Chrome",
    state: "Installing",
    message: "",
    timestampUtc: "2026-10-03T22:22:44Z",
    ...overrides,
  });

  assert.equal(cardProgress(chrome, [], "active"), null);
  // Requested/Queued: the service has not started on it yet.
  assert.deepEqual(cardProgress(chrome, [record({ state: "Requested" })], "active"), {
    label: "Waiting…",
    outcome: "active",
  });
  // Own item phase carries its percentage.
  assert.deepEqual(cardProgress(chrome, [record({ progressPercent: 50 })], "active"), {
    label: "Installing…",
    percent: 50,
    outcome: "active",
  });
  // Another item's phase (updater, dependency, or an unrelated item in the
  // same run) is named so its percentage is not read as this item's.
  assert.deepEqual(
    cardProgress(
      chrome,
      [record({ itemName: "DemoFailing", displayName: "Demo Failing", progressPercent: 50 })],
      "active",
    ),
    { label: "Installing Demo Failing…", percent: 50, outcome: "active" },
  );
  assert.deepEqual(
    cardProgress(chrome, [record({ itemName: "DemoFailing", displayName: "Demo Failing", state: "ItemFailed" })], "active"),
    { label: "In progress…", outcome: "active" },
  );
  // Success leaves the card to the refreshed list status.
  assert.equal(
    cardProgress(chrome, [record({ state: "Succeeded", message: "Operation completed" })], "terminal"),
    null,
  );
  // Deferred/Failed keep their reason on the card until the next action.
  assert.deepEqual(
    cardProgress(chrome, [record({ state: "Deferred", message: "blocking application(s) running: chrome" })], "terminal"),
    { label: "Waiting: close chrome to continue", outcome: "terminal" },
  );
  assert.deepEqual(cardProgress(chrome, [record({ state: ERROR_STATE, message: "pipe closed" })], "error"), {
    label: "Request failed · pipe closed",
    outcome: "error",
  });
});

test("outcome lines say what failed and quote the service's reason", () => {
  const failed = {
    operationId: "op-1",
    itemName: "DemoFailing",
    displayName: "Demo Failing",
    state: "Failed",
    message: "Operation failed (installer exited with code 1)",
    detail: "installer exited with code 1",
    timestampUtc: "2026-10-03T22:22:44Z",
  };
  const item = { itemName: "DemoFailing" };
  assert.equal(
    cardProgress(item, [failed], "terminal", "InstallItem").label,
    "Install failed · installer exited with code 1",
  );
  assert.equal(
    cardProgress(item, [failed], "terminal", "RemoveItem").label,
    "Removal failed · installer exited with code 1",
  );
  // Without a wire errorMessage the message itself is the reason.
  assert.equal(
    cardProgress(item, [{ ...failed, detail: undefined, message: "boom" }], "terminal").label,
    "Install failed · boom",
  );
  // A deferral that is not about a running app reads as the service's reason.
  assert.equal(
    cardProgress(item, [{ ...failed, state: "Deferred", detail: undefined, message: "dependency DemoUpdater deferred" }], "terminal").label,
    "dependency DemoUpdater deferred",
  );
  assert.equal(
    cardProgress(item, [{ ...failed, state: "Canceled", detail: undefined, message: "Operation canceled" }], "terminal").label,
    "Canceled",
  );
});

test("My items keeps only installed or managed items", () => {
  const items = [
    item({ itemName: "a", isInstalled: true }),
    item({ itemName: "b", isManaged: true, status: "WillBeInstalled" }),
    item({ itemName: "c" }),
  ];
  assert.deepEqual(myItems(items).map((i) => i.itemName), ["a", "b"]);
});

test("glyph tone is stable per item and inside the palette", () => {
  assert.equal(glyphTone({ itemName: "GoogleChrome" }), glyphTone({ itemName: "GoogleChrome" }));
  for (const name of ["", "a", "GoogleChrome", "DemoOptional", "7-Zip"]) {
    const tone = glyphTone({ itemName: name });
    assert.ok(Number.isInteger(tone) && tone >= 0 && tone < 5, `${name} -> ${tone}`);
  }
});

test("the strip shows the running operation the service is working on, as n of N", () => {
  const record = (operationId, itemName, state, progressPercent) => ({
    operationId,
    itemName,
    displayName: itemName,
    state,
    message: "",
    timestampUtc: "2026-10-03T22:22:44Z",
    ...(progressPercent === undefined ? {} : { progressPercent }),
  });
  const operation = (operationId, itemName, outcome, records) => ({
    operationId,
    item: item({ itemName, displayName: itemName }),
    action: { kind: "do", label: "Install", method: "InstallItem" },
    records,
    outcome,
  });

  assert.equal(stripView([]), null, "nothing running hides the strip");
  assert.equal(
    stripView([operation("op-0", "Done", "terminal", [record("op-0", "Done", "Succeeded")])]),
    null,
    "finished operations never hold the strip open",
  );

  // Both just accepted: the earliest is shown, with no percentage (indeterminate).
  const waiting = stripView([
    operation("op-1", "Chrome", "active", [record("op-1", "Chrome", REQUESTED_STATE)]),
    operation("op-2", "Zed", "active", [record("op-2", "Zed", "Queued")]),
  ]);
  assert.equal(waiting.operation.operationId, "op-1");
  assert.deepEqual([waiting.position, waiting.total], [1, 2]);
  assert.equal(waiting.progress.percent, undefined);
  assert.equal(stripLine(waiting), "Waiting… · 1 of 2");

  // The second one is the one actually reporting: it is shown as 2 of 2 with
  // its percentage.
  const working = stripView([
    operation("op-0", "Done", "terminal", [record("op-0", "Done", "Failed")]),
    operation("op-1", "Chrome", "active", [record("op-1", "Chrome", REQUESTED_STATE), record("op-1", "Chrome", "Queued")]),
    operation("op-2", "Zed", "active", [record("op-2", "Zed", REQUESTED_STATE), record("op-2", "Zed", "Downloading", 42)]),
  ]);
  assert.equal(working.operation.operationId, "op-2");
  assert.deepEqual([working.position, working.total], [2, 2]);
  assert.equal(working.progress.percent, 42);
  assert.equal(stripLine(working), "Downloading… 42% · 2 of 2");

  // A phase without a percentage on the wire stays indeterminate.
  const unmeasured = stripView([
    operation("op-3", "Chrome", "active", [record("op-3", "Chrome", "ItemCompleted")]),
  ]);
  assert.equal(unmeasured.progress.percent, undefined);
  assert.equal(progressText(unmeasured.progress), "Item Completed…");

  // A refused cancel came from the strip's own button, and Home cards show no
  // running state, so the strip is where its reason must appear.
  const refused = stripView([
    { ...operation("op-4", "Chrome", "active", [record("op-4", "Chrome", "Installing")]), notice: NOT_AUTHORIZED_TEXT },
  ]);
  assert.equal(stripLine(refused), `${NOT_AUTHORIZED_TEXT} · 1 of 1`);
});

const progressRecord = (state, extra = {}) => ({
  operationId: "op",
  itemName: "Chrome",
  displayName: "Chrome",
  state,
  message: "",
  timestampUtc: "2026-10-03T22:22:44Z",
  ...extra,
});
const INSTALL = { kind: "do", label: "Install", method: "InstallItem" };
const chrome = item({ itemName: "Chrome", displayName: "Chrome" });
const running = (states, outcome = "active", action = INSTALL) => ({
  action,
  outcome,
  records: states.map((state) => (typeof state === "string" ? progressRecord(state) : state)),
});

test("a button waits with a spinner and reads Waiting… until the service starts the work", () => {
  // Not answered yet: there is no operation.
  assert.deepEqual(actionProgress(chrome, INSTALL, INSTALL, undefined), { label: "Waiting…", phase: "waiting" });
  for (const states of [[], [REQUESTED_STATE], [REQUESTED_STATE, "Queued"]]) {
    assert.deepEqual(actionProgress(chrome, INSTALL, INSTALL, running(states)), { label: "Waiting…", phase: "waiting" });
  }
  // A free button, or another placement's work, keeps the button's label.
  assert.equal(actionProgress(chrome, INSTALL, undefined, running(["Queued"])), null);
  const row = { ...INSTALL, placement: "codex" };
  assert.equal(actionProgress(chrome, row, { ...INSTALL, placement: "claude" }, running(["Queued"]), "codex"), null);
});

test("a working button is filled only when the service measured the phase", () => {
  const measured = actionProgress(chrome, INSTALL, INSTALL, running([progressRecord("Downloading", { progressPercent: 42 })]));
  assert.deepEqual(measured, { label: "Downloading…", phase: "working", percent: 42 });
  // The item's own installing label, indeterminate: the engine measures only downloads.
  assert.deepEqual(actionProgress(chrome, INSTALL, INSTALL, running(["Downloading", "Installing"])), {
    label: "Installing…",
    phase: "working",
  });
  // A dependency's phase is named, so its percentage is not read as the item's.
  const dependency = progressRecord("Downloading", { itemName: "Runtime", displayName: "Runtime", progressPercent: 10 });
  assert.deepEqual(actionProgress(chrome, INSTALL, INSTALL, running([dependency])), {
    label: "Downloading Runtime…",
    phase: "working",
    percent: 10,
  });
  // Between phases the button keeps its busy label, never a record's name.
  assert.deepEqual(actionProgress(chrome, INSTALL, INSTALL, running(["Downloading", "ItemCompleted"])), {
    label: "Installing…",
    phase: "working",
  });
  // A busy Cancel says Cancel, not the opposite mutation's phase.
  const cancel = { kind: "cancel", label: "Cancel", method: "RemoveItem" };
  assert.deepEqual(actionProgress(chrome, cancel, cancel, running(["Removing"], "active", cancel)), {
    label: "Cancel",
    phase: "working",
  });
  // An out-of-range percentage is held to the bar.
  assert.equal(
    actionProgress(chrome, INSTALL, INSTALL, running([progressRecord("Downloading", { progressPercent: 140 })])).percent,
    100,
  );
});

test("only a success reads full; a failed, canceled or errored operation shows no bar", () => {
  assert.deepEqual(actionProgress(chrome, INSTALL, INSTALL, running(["Installing", "Succeeded"], "terminal")), {
    label: "Installing…",
    phase: "done",
    percent: 100,
  });
  for (const [states, outcome] of [
    [["Downloading", "Failed"], "terminal"],
    [["Downloading", "Canceled"], "terminal"],
    [["Downloading", "Deferred"], "terminal"],
    [["Downloading", ERROR_STATE], "error"],
  ]) {
    const ended = actionProgress(chrome, INSTALL, INSTALL, running(states, outcome));
    assert.deepEqual(ended, { label: "Install", phase: "ended" }, states.join(" "));
  }
});

test("in both modes an ended item stays busy until the list after its end; only the button's look differs", () => {
  // The service queues the list behind running installs, so this can last a
  // while. The release is main's rule in both modes: an ended operation is
  // released only by the refresh that follows it (endedOperations).
  const active = trackOperation(new Map(), "op", "Chrome");
  for (const [state, phase, label] of [
    ["Succeeded", "done", "Installing…"],
    ["Failed", "ended", "Install"],
    ["Canceled", "ended", "Install"],
    ["Deferred", "ended", "Install"],
  ]) {
    const operation = { operationId: "op", item: chrome, ...running(["Downloading", state], "terminal") };
    const operations = new Map([["op", operation]]);
    const busy = busyAction("Chrome", new Map(), active, operations);
    assert.equal(busy, INSTALL, state);
    assert.deepEqual(endedOperations(active, operations), ["op"], state);
    // While held, the strip-mode label stays the busy one, as on main; the
    // button-mode look never implies work still going after a non-success.
    assert.equal(actionText(chrome, INSTALL, busy), "Installing…", state);
    const shown = actionProgress(chrome, INSTALL, busy, operation);
    assert.equal(shown.phase, phase, state);
    assert.equal(shown.label, label, state);
    assert.equal(busyAction("Chrome", new Map(), releaseOperation(active, "op"), operations), undefined, state);
  }
});

test("an action on the whole item carries its progress onto the placement rows that do the same", () => {
  const add = { kind: "do", label: "Install", method: "InstallItem", placement: "codex" };
  const work = running([progressRecord("Downloading", { progressPercent: 30 })]);
  assert.deepEqual(actionProgress(chrome, add, INSTALL, work, "codex"), {
    label: "Downloading…",
    phase: "working",
    percent: 30,
  });
  // A Remove row does not do what the whole-item install does.
  const remove = { kind: "undo", label: "Remove", method: "RemoveItem", placement: "claude" };
  assert.equal(actionProgress(chrome, remove, INSTALL, work, "claude"), null);
  // The row's own action carries it; a sibling row does not.
  assert.equal(actionProgress(chrome, add, add, work, "codex").percent, 30);
  assert.equal(actionProgress(chrome, { ...add, placement: "claude" }, add, work, "claude"), null);
});

test("with progress in the buttons the strip only counts two or more running operations", () => {
  const operation = (operationId, itemName, outcome, states, action = INSTALL) => ({
    operationId,
    item: item({ itemName, displayName: itemName }),
    action,
    records: states.map((state) => ({ ...progressRecord(state), operationId, itemName, displayName: itemName })),
    outcome,
  });
  assert.equal(queueView([]), null);
  // One running: its own button shows it.
  assert.equal(queueView([operation("op-1", "Chrome", "active", ["Downloading"])]), null);
  assert.equal(
    queueView([operation("op-0", "Done", "terminal", ["Succeeded"]), operation("op-1", "Chrome", "active", ["Queued"])]),
    null,
  );

  // main.ts passes a Map's values(), an iterator that can be read only once.
  const queue = queueView(
    new Map(
      [
        operation("op-1", "Chrome", "active", [REQUESTED_STATE, "Queued"]),
        operation("op-2", "Zed", "active", [REQUESTED_STATE, "Downloading"]),
        { ...operation("op-3", "Slack", "active", ["Queued"]), notice: NOT_AUTHORIZED_TEXT },
      ].map((operation) => [operation.operationId, operation]),
    ).values(),
  );
  // A count only: no phase and no percentage. The place is stripView's.
  assert.equal(queue.line, "Installing 2 of 3");
  // The working one first, then service order; each row's state without a percentage.
  assert.deepEqual(
    queue.rows.map(({ operation, text }) => [operation.operationId, text]),
    [
      ["op-2", "Downloading…"],
      ["op-1", "Waiting…"],
      ["op-3", NOT_AUTHORIZED_TEXT],
    ],
  );
  const removing = queueView([
    operation("op-1", "Chrome", "active", ["Removing"], { kind: "undo", label: "Remove", method: "RemoveItem" }),
    operation("op-2", "Zed", "active", ["Queued"]),
  ]);
  assert.equal(removing.line, "Removing 1 of 2");
  // Nothing started yet: the strip waits, as every row does.
  const waiting = queueView([
    operation("op-1", "Chrome", "active", [REQUESTED_STATE, "Queued"]),
    operation("op-2", "Zed", "active", ["Queued"]),
  ]);
  assert.equal(waiting.line, "Waiting 1 of 2");
  // The item's own installing label, as its button reads it.
  const adding = queueView([
    { ...operation("op-1", "Skill", "active", ["Downloading"]), item: item({ actionLabels: { installing: "Adding…" } }) },
    operation("op-2", "Zed", "active", ["Queued"]),
  ]);
  assert.equal(adding.line, "Adding 1 of 2");
});

test("the strip's own view is unchanged by where the buttons show progress", () => {
  // queueView is a separate view: stripView still reports the working
  // operation with its phase and percentage, and still shows a lone one.
  const lone = [
    {
      operationId: "op-1",
      item: chrome,
      action: INSTALL,
      records: [progressRecord("Downloading", { progressPercent: 42 })],
      outcome: "active",
    },
  ];
  assert.equal(stripLine(stripView(lone)), "Downloading… 42% · 1 of 1");
  assert.equal(queueView(lone), null);
});

test("the announcer says the item and its phase, never a percentage", () => {
  const operation = { item: chrome, action: INSTALL, outcome: "active", records: [progressRecord("Queued")] };
  assert.equal(announcement(operation), "Chrome: Waiting…");
  const ticks = [42, 43].map((progressPercent) =>
    announcement({ ...operation, records: [progressRecord("Downloading", { progressPercent })] }),
  );
  assert.deepEqual(ticks, ["Chrome: Downloading…", "Chrome: Downloading…"]);
  assert.equal(announcement({ ...operation, outcome: "terminal" }), "");
});

test("a Home card shows only outcomes the user has to act on", () => {
  // The button already names the item's state and the strip shows running
  // work, so anything else on the card is noise.
  assert.equal(needsAttention(null), false, "idle or succeeded");
  assert.equal(needsAttention({ label: "Installing…", percent: 50, outcome: "active" }), false);
  assert.equal(needsAttention({ label: "Canceled", outcome: "terminal", plain: true }), false);
  assert.equal(needsAttention({ label: "Waiting: close chrome to continue", outcome: "terminal" }), true);
  assert.equal(needsAttention({ label: "Request failed · pipe closed", outcome: "error" }), true);
});

// With nothing configured the shell must look exactly as it does unbranded.
test("brandingView of an empty or missing payload is the default shell", () => {
  for (const payload of [null, undefined, "junk", {}, { title: "  ", accent: "", helpUrl: "" }]) {
    const view = brandingView(payload);
    assert.equal(view.showBanner, false);
    assert.equal(view.productName, "SofCat");
    assert.equal(view.productMark, "", "an empty mark shows the SofCat icon");
    assert.equal(view.accent, "");
    assert.equal(view.logoSrc, "");
    assert.equal(view.helpUrl, "");
  }
});

test("brandingView keeps only real section names, so a cached payload cannot break the tabs", () => {
  assert.deepEqual(brandingView({ sections: [" Skills ", "", "  ", 3, null, "Plugins"] }).sections, ["Skills", "Plugins"]);
  for (const payload of [null, {}, { sections: "Skills" }, { sections: { 0: "Skills" } }]) {
    assert.deepEqual(brandingView(payload).sections, [], JSON.stringify(payload));
  }
  // Sections alone are not banner content.
  assert.equal(brandingView({ sections: ["Skills"] }).showBanner, false);
});

test("brandingView shows the banner for any of title, tagline, logo or help", () => {
  const png = { logoMime: "image/png", logoBase64: "iVBORw0KGgo=" };
  for (const payload of [{ title: "Acme" }, { tagline: "Call ext. 1234" }, png, { helpUrl: "https://example.invalid/help" }]) {
    assert.equal(brandingView(payload).showBanner, true, JSON.stringify(payload));
  }
  // An organisation title without a logo shows its initial, not the SofCat icon.
  assert.equal(brandingView({ title: "acme" }).productMark, "A");
  // An accent alone recolours the shell but is not banner content.
  assert.equal(brandingView({ accent: "#0b6e4f" }).showBanner, false);
  // A help label without a URL has nothing to open.
  assert.equal(brandingView({ helpLabel: "Get help" }).showBanner, false);
});

// link_placement: a payload without it (an older service, a cached copy) keeps
// the link in the banner, where a link alone still brings the banner up; in
// the app bar it does not, and anything else is banner.
test("brandingView places the link and only a banner link makes a banner", () => {
  const link = { helpUrl: "https://example.invalid/help" };
  const cached = brandingView(link);
  assert.equal(cached.linkPlacement, "banner");
  assert.equal(cached.linkPosition, "auto");
  assert.equal(cached.helpLabel, "Get help");
  const inBar = brandingView({ ...link, linkPlacement: "app_bar", linkPosition: "top_left" });
  assert.equal(inBar.linkPlacement, "app_bar");
  assert.equal(inBar.showBanner, false);
  assert.equal(inBar.helpUrl, "https://example.invalid/help", "the app bar still shows the link");
  // No banner names the organisation, so the app bar keeps the product's name.
  assert.equal(brandingView({ ...link, linkPlacement: "app_bar", appBarBrand: "none" }).appBarBrand, "full");
  assert.equal(brandingView({ ...link, title: "Acme", linkPlacement: "app_bar" }).showBanner, true);
  assert.equal(brandingView({ ...link, linkPlacement: "header" }).showBanner, true);
  assert.equal(brandingView({ ...link, linkPlacement: "header" }).linkPlacement, "banner");
  assert.equal(brandingView({ ...link, linkPosition: "bottom_right" }).linkPosition, "bottom_right");
  assert.equal(brandingView({ ...link, linkPosition: "middle" }).linkPosition, "auto");
});

test("brandingView maps the branded board", () => {
  const view = brandingView({
    title: "Acme Software Center",
    tagline: "Need help? Call the service desk at ext. 1234.",
    helpUrl: "https://example.invalid/help",
    helpLabel: "",
    accent: "#0B6E4F",
    logoMime: "image/png",
    logoBase64: "iVBORw0KGgo=",
  });
  assert.equal(view.productName, "Acme Software Center");
  assert.equal(view.productMark, "A");
  assert.equal(view.logoSrc, "data:image/png;base64,iVBORw0KGgo=");
  assert.equal(view.backgroundSrc, "", "no background image: the banner keeps the accent");
  assert.equal(view.helpLabel, "Get help");
  assert.equal(view.accent, "#0b6e4f");
  assert.equal(view.onAccent, "#ffffff");
});

// The WebView only ever opens http(s), builds an image data: URL, or sets a
// #rrggbb colour, even if a cached or tampered payload says otherwise.
test("brandingView drops values the WebView must not use", () => {
  const view = brandingView({
    title: "Acme",
    helpUrl: "javascript:alert(1)",
    helpLabel: "Click",
    accent: "red; --x: url(evil)",
    logoMime: "text/html",
    logoBase64: "PHNjcmlwdD4=",
  });
  assert.equal(view.helpUrl, "");
  assert.equal(view.helpLabel, "");
  assert.equal(view.accent, "");
  assert.equal(view.logoSrc, "");
  assert.equal(brandingView({ logoMime: "image/svg+xml", logoBase64: '"><script>' }).logoSrc, "");
  // The background source goes into a CSS url(""), so the same checks guard it.
  assert.equal(brandingView({ backgroundMime: "text/html", backgroundBase64: "PHNjcmlwdD4=" }).backgroundSrc, "");
  assert.equal(brandingView({ backgroundMime: "image/jpeg", backgroundBase64: '"); x: url(evil' }).backgroundSrc, "");
});

test("isHexColor and httpUrl", () => {
  assert.equal(isHexColor("#0b6e4f"), true);
  assert.equal(isHexColor("#0b6e4"), false);
  assert.equal(isHexColor("0b6e4f"), false);
  assert.equal(httpUrl("https://example.invalid/help"), "https://example.invalid/help");
  assert.equal(httpUrl("HTTP://intranet/help"), "http://intranet/help");
  assert.equal(httpUrl("file:///C:/Windows"), "");
  assert.equal(httpUrl("/help"), "");
});

test("onAccent picks readable text", () => {
  assert.equal(onAccent("#0b6e4f"), "#ffffff");
  assert.equal(onAccent("#1a5fb4"), "#ffffff");
  assert.equal(onAccent("#ffd400"), "#1b1b1f");
  assert.equal(onAccent("#ffffff"), "#1b1b1f");
});

test("Cancel is offered only before the installer starts", () => {
  for (const state of [REQUESTED_STATE, "Queued", "Downloading"]) {
    assert.equal(canCancel(state), true, state);
    assert.equal(cancelTitle(state), "Cancel before the installer starts");
  }
  // A running installer is never interrupted, and finished work has nothing to cancel.
  for (const state of ["Installing", "Removing", "ItemCompleted", "ItemFailed", "Succeeded", "Canceled", ""]) {
    assert.equal(canCancel(state), false, state);
    assert.match(cancelTitle(state), /^Can't cancel now/);
  }
  assert.equal(cancelTitle("Queued", true), "Canceling…");
});

test("a refused cancel shows the service's reason without its error code", () => {
  assert.equal(
    refusalText("operation_not_cancelable: operation can no longer be canceled: work on Google Chrome has already started"),
    "Operation can no longer be canceled: work on Google Chrome has already started",
  );
  assert.equal(refusalText("pipe unavailable"), "Pipe unavailable");
  assert.equal(refusalText(""), "The operation can no longer be canceled");
});

// A refusal for another user's item reads as a sentence, not as the
// service's code and quoted item name.
test("a not_authorized refusal reads plainly", () => {
  const refused = 'not_authorized: only the user who asked for this, or an administrator, can change it: "Slack"';
  assert.equal(requestErrorText(refused), NOT_AUTHORIZED_TEXT);
  assert.equal(refusalText(refused), NOT_AUTHORIZED_TEXT);
  assert.equal(requestErrorText("pipe unavailable"), "pipe unavailable");
});

test("a user cancel reads plainly on the card and says who in Activity", () => {
  const chrome = { itemName: "GoogleChrome" };
  const canceled = statusRecord({
    operationId: "op-1",
    timestampUtc: "not-a-date",
    itemName: "GoogleChrome",
    displayName: "Google Chrome",
    state: "Canceled",
    progressPercent: 0,
    message: "Canceled by user",
    canceledBy: "user",
  });
  assert.equal(canceled.canceledBy, "user");
  assert.deepEqual(cardProgress(chrome, [canceled], "terminal"), {
    label: "Canceled",
    outcome: "terminal",
    plain: true,
  });
  assert.equal(activityLine(canceled), "not-a-date — Google Chrome: Canceled by you");
  assert.equal(
    activityLine({ ...canceled, canceledBy: "service", message: "Operation canceled" }),
    "not-a-date — Google Chrome: Canceled by the service",
  );

  // A refused cancel replaces the running line, not the item's state.
  const installing = { ...canceled, state: "Installing", canceledBy: undefined };
  assert.deepEqual(cardProgress(chrome, [installing], "active", "InstallItem", "Work has already started"), {
    label: "Work has already started",
    outcome: "active",
  });
  // Other outcomes keep the warning treatment.
  assert.equal(cardProgress(chrome, [{ ...installing, state: "Failed" }], "terminal").plain, undefined);
});

test("the connection label is short in every state while the message keeps the reason", () => {
  const stale = { items: [item({})], source: "stale", savedAtUtc: "2026-10-03T12:00:00Z" };
  assert.equal(connectionLabel(stale), "Service unavailable");
  assert.match(bannerMessage(stale, "pipe unavailable"), /pipe unavailable/);
  assert.equal(connectionLabel({ items: [], source: "live", savedAtUtc: "" }), "Service connected");
  assert.equal(connectionLabel({ items: [], source: "loading", savedAtUtc: "" }), "Connecting…");
  assert.equal(connectionLabel({ items: [], source: "cache", savedAtUtc: "" }), "Showing cached software");
});

// Activity keeps the newest ACTIVITY_LIMIT records. A measured download sends
// a record per percent, so filing each one would flush the user's history;
// only a change of item or state is a new Activity entry.
test("a download's percent updates are progress ticks, not new Activity entries", () => {
  const record = (seq, itemName, state, progressPercent) => ({
    operationId: "op-1",
    seq,
    itemName,
    displayName: itemName,
    state,
    message: "",
    timestampUtc: "2026-07-21T12:00:00Z",
    ...(progressPercent === undefined ? {} : { progressPercent }),
  });
  const records = [];
  const filed = [
    record(1, "Chrome", "Queued"),
    record(2, "Chrome", "Downloading"),
    record(3, "Chrome", "Downloading", 1),
    record(4, "Chrome", "Downloading", 2),
    record(5, "Updater", "Downloading", 1),
    record(6, "Chrome", "Installing"),
  ].filter((r) => insertRecord(records, r) && !isProgressTick(records, r));
  assert.deepEqual(
    filed.map((r) => r.seq),
    [1, 2, 5, 6],
  );
});

// An admin sets an accent, a background image or both. The image wins in the
// banner; the accent still colours the rest of the shell and sits under the
// image, so neither may drop the other.
test("brandingView keeps a background image beside the accent, and it alone shows the banner", () => {
  const image = { backgroundMime: "image/jpeg", backgroundBase64: "/9j/4AAQ" };
  const both = brandingView({ ...image, accent: "#d32323" });
  assert.equal(both.backgroundSrc, "data:image/jpeg;base64,/9j/4AAQ");
  assert.equal(both.accent, "#d32323");
  assert.equal(brandingView(image).showBanner, true);
  assert.equal(brandingView(image).accent, "");
  // The admin picks the text colour for their picture; anything else is light.
  assert.equal(brandingView(image).backgroundText, "light");
  assert.equal(brandingView({ ...image, backgroundText: "dark" }).backgroundText, "dark");
  assert.equal(brandingView({ ...image, backgroundText: "red; x" }).backgroundText, "light");
});

// The web view's own reload is off, so these keys are the only way to see a
// catalog change without restarting; a held key must not flood the service.
test("F5, Ctrl+R and Cmd+R ask for a refresh, once per press", () => {
  const key = (overrides) => ({
    key: "F5",
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    repeat: false,
    ...overrides,
  });
  assert.equal(isRefreshKey(key()), true);
  assert.equal(isRefreshKey(key({ ctrlKey: true })), true, "Ctrl+F5");
  assert.equal(isRefreshKey(key({ key: "r", ctrlKey: true })), true);
  assert.equal(isRefreshKey(key({ key: "R", metaKey: true })), true);
  assert.equal(isRefreshKey(key({ key: "r" })), false, "typing r in the search field");
  assert.equal(isRefreshKey(key({ key: "r", ctrlKey: true, altKey: true })), false, "AltGr+R");
  assert.equal(isRefreshKey(key({ altKey: true })), false);
  assert.equal(isRefreshKey(key({ repeat: true })), false);
  assert.equal(isRefreshKey(key({ key: "F6" })), false);
});
