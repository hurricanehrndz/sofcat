import type {
  ActionMethod,
  ActiveOperations,
  ActivityRecord,
  BrandingView,
  CachedList,
  ItemAction,
  ListView,
  OperationOutcome,
  OperationStatus,
  OperationView,
  OptionalInstallItem,
  OptionalInstallPlacement,
} from "./types.ts";

export const ALL_CATEGORIES = "all";
// Empty, which no name is, so a placement that applies to "all" stays a name.
export const ALL_APPLIES_TO = "";

const RESTART_LABELS: Record<string, string> = {
  requirerestart: "Restart required",
  recommendrestart: "Restart recommended",
  requirelogout: "Sign out required",
  requireshutdown: "Shut down required",
};

// Category glyphs are generated locally. An item's own icon comes from the
// service (GetItemIcon); the UI never fetches a catalog icon itself.
const CATEGORY_GLYPHS: Record<string, string> = {
  browsers: "🌐",
  communication: "💬",
  development: "⌨",
  media: "🎬",
  productivity: "📄",
  security: "🛡",
  utilities: "🔧",
};

const GENERIC_GLYPH = "▪";

function fold(value: string): string {
  return value.trim().toLowerCase();
}

function matchesSearch(item: OptionalInstallItem, query: string): boolean {
  const needle = fold(query);
  if (!needle) {
    return true;
  }
  return [item.displayName, item.itemName, item.developer ?? "", item.category ?? "", ...appliesToNames(item)].some(
    (field) => field.toLowerCase().includes(needle),
  );
}

/** appliesToNames is the names the item's placements list. A cached list is untrusted shape-wise, so keep only real names. */
function appliesToNames(item: Pick<OptionalInstallItem, "appliesTo">): string[] {
  return Array.isArray(item.appliesTo)
    ? item.appliesTo.filter((name): name is string => typeof name === "string" && name.trim() !== "")
    : [];
}

function matchesCategory(item: OptionalInstallItem, category: string): boolean {
  return category === ALL_CATEGORIES || fold(item.category ?? "") === fold(category);
}

// An item that lists no names matches only "all": picking a name asks for
// what works there.
function matchesAppliesTo(item: OptionalInstallItem, name: string): boolean {
  return name === ALL_APPLIES_TO || appliesToNames(item).some((own) => fold(own) === fold(name));
}

/** compareItems gives a stable display order: display name, then item name. */
export function compareItems(a: OptionalInstallItem, b: OptionalInstallItem): number {
  return (
    a.displayName.localeCompare(b.displayName, "en", { sensitivity: "base" }) ||
    a.itemName.localeCompare(b.itemName, "en", { sensitivity: "base" })
  );
}

export function visibleItems(
  items: OptionalInstallItem[],
  query: string,
  category: string,
  appliesTo: string = ALL_APPLIES_TO,
): OptionalInstallItem[] {
  return items
    .filter(
      (item) => matchesSearch(item, query) && matchesCategory(item, category) && matchesAppliesTo(item, appliesTo),
    )
    .sort(compareItems);
}

/**
 * enteringItems names the cards that ease in when the grid is rebuilt: those
 * it did not show before. Status updates and refreshes rebuild it too, and the
 * cards already on screen must hold still through them; a page that is itself
 * easing in (pageEntering) carries its cards, so none of them moves twice.
 */
export function enteringItems(
  shownBefore: Iterable<string | undefined>,
  shown: OptionalInstallItem[],
  pageEntering: boolean,
): Set<string> {
  if (pageEntering) {
    return new Set();
  }
  const before = new Set(shownBefore);
  return new Set(shown.map((item) => item.itemName).filter((name) => !before.has(name)));
}

/** myItems is the "My items" view: what is installed or managed from here. */
export function myItems(items: OptionalInstallItem[]): OptionalInstallItem[] {
  return items.filter((item) => item.isInstalled || item.isManaged);
}

export function categories(items: OptionalInstallItem[]): string[] {
  const seen = new Map<string, string>();
  for (const item of items) {
    const category = (item.category ?? "").trim();
    if (category && !seen.has(fold(category))) {
      seen.set(fold(category), category);
    }
  }
  return [...seen.values()].sort((a, b) => a.localeCompare(b, "en", { sensitivity: "base" }));
}

/**
 * appliesToChoices is what the applies-to filter offers: the names the items'
 * placements list, or none when they list fewer than two, since one name
 * filters nothing worth a control.
 */
export function appliesToChoices(items: OptionalInstallItem[]): string[] {
  const seen = new Map<string, string>();
  for (const name of items.flatMap(appliesToNames)) {
    if (!seen.has(fold(name))) {
      seen.set(fold(name), name.trim());
    }
  }
  return seen.size < 2 ? [] : [...seen.values()].sort((a, b) => a.localeCompare(b, "en", { sensitivity: "base" }));
}

/** ListTab is a storefront tab: a catalog section, or key "" for items without one. */
export type ListTab = { key: string; label: string };

/** UNSECTIONED_LABEL names the tab of items with no section. */
export const UNSECTIONED_LABEL = "Software";

function sectionKey(item: OptionalInstallItem): string {
  // A cached list is untrusted shape-wise, so a non-string section is none.
  return typeof item.section === "string" ? fold(item.section) : "";
}

/**
 * tabs is the storefront's list tabs: the sections the admin listed, in that
 * order, then the rest alphabetically, then "Software" for items without a
 * section. With no sections at all "Software" is the only tab, so a catalog
 * that uses none (or a list still loading) looks as it always has.
 */
export function tabs(items: OptionalInstallItem[], order: string[]): ListTab[] {
  const seen = new Map<string, string>();
  let unsectioned = false;
  for (const item of items) {
    const key = sectionKey(item);
    if (!key) {
      unsectioned = true;
    } else if (!seen.has(key)) {
      seen.set(key, (item.section ?? "").trim());
    }
  }
  const listed = new Map<string, string>();
  for (const name of order) {
    const key = fold(name);
    if (seen.has(key) && !listed.has(key)) {
      listed.set(key, name.trim());
    }
  }
  const rest = [...seen].filter(([key]) => !listed.has(key));
  rest.sort(([, a], [, b]) => a.localeCompare(b, "en", { sensitivity: "base" }));
  const result = [...listed, ...rest].map(([key, label]) => ({ key, label }));
  if (unsectioned || result.length === 0) {
    result.push({ key: "", label: UNSECTIONED_LABEL });
  }
  return result;
}

/** resolveTab is key when it is still one of list, else the first tab's key. */
export function resolveTab(list: ListTab[], key: string | null): string {
  const found = list.find((tab) => tab.key === key) ?? list[0];
  return found?.key ?? "";
}

/** tabItems is what a list tab shows: the items whose section folds to key. */
export function tabItems(items: OptionalInstallItem[], key: string): OptionalInstallItem[] {
  return items.filter((item) => sectionKey(item) === key);
}

export function categoryGlyph(category: string | undefined): string {
  return CATEGORY_GLYPHS[fold(category ?? "")] ?? GENERIC_GLYPH;
}

/** monogram returns up to two initials, falling back to a category glyph. */
export function monogram(item: OptionalInstallItem): string {
  const words = (item.displayName || item.itemName).split(/[^\p{L}\p{N}]+/u).filter(Boolean);
  const initials = words
    .slice(0, 2)
    .map((word) => word[0]?.toUpperCase() ?? "")
    .join("");
  return initials || categoryGlyph(item.category);
}

/**
 * iconKey names the icon the service holds for an item, or "" when it holds
 * none: the hash changes with the image, so a replaced icon is asked for again.
 */
export function iconKey(item: Pick<OptionalInstallItem, "iconName" | "iconHash">): string {
  const name = typeof item.iconName === "string" ? item.iconName.trim() : "";
  const hash = typeof item.iconHash === "string" ? item.iconHash.trim() : "";
  return name && hash ? `${hash}:${name}` : "";
}

/** iconSource is a GetItemIcon payload as an <img> source, or "" unless it is a PNG or JPEG. */
export function iconSource(icon: { mime?: unknown; base64?: unknown } | null | undefined): string {
  const { mime, base64 } = icon ?? {};
  if ((mime !== "image/png" && mime !== "image/jpeg") || typeof base64 !== "string" || !/^[A-Za-z0-9+/]+=*$/.test(base64)) {
    return "";
  }
  return `data:${mime};base64,${base64}`;
}

// Glyph tones are a fixed palette in style.css (.tone-0 … .tone-4).
const GLYPH_TONES = 5;

/** glyphTone picks a stable palette index from the item name, so a glyph keeps its colour. */
export function glyphTone(item: Pick<OptionalInstallItem, "itemName">): number {
  let hash = 0;
  for (const char of item.itemName) {
    hash = (hash * 31 + (char.codePointAt(0) ?? 0)) >>> 0;
  }
  return hash % GLYPH_TONES;
}

/** restartBadge returns "" unless restartAction is meaningful. */
export function restartBadge(item: OptionalInstallItem): string {
  const raw = (item.restartAction ?? "").trim();
  if (!raw || fold(raw) === "none") {
    return "";
  }
  return RESTART_LABELS[fold(raw)] ?? raw;
}

/**
 * BUILT_IN_LABELS are the storefront's own words for each custom_action_labels
 * key. The service sends only the keys an item or its placements override.
 */
export const BUILT_IN_LABELS = {
  install: "Install",
  installing: "Installing…",
  installed: "Installed",
  remove: "Remove",
  removing: "Removing…",
  update: "Update",
  cancel: "Cancel",
  pending_install: "Will be installed",
  pending_remove: "Will be removed",
  applies_to: "Applies to",
} as const;

export type LabelKey = keyof typeof BUILT_IN_LABELS;

/**
 * actionLabel is the item's label for key: the service's resolved label, used
 * verbatim (it is only ever set as textContent), else the built-in one.
 */
export function actionLabel(item: Pick<OptionalInstallItem, "actionLabels">, key: LabelKey): string {
  const custom: unknown = item.actionLabels?.[key];
  return typeof custom === "string" && custom.trim() ? custom : BUILT_IN_LABELS[key];
}

/**
 * appliesToLine is the item's applies_to label, "Applies to" unless the item
 * or a placement sets one, followed by the names its placements list, or ""
 * when they list none. It is display text, not scoping.
 */
export function appliesToLine(item: Pick<OptionalInstallItem, "actionLabels" | "appliesTo">): string {
  const names = appliesToNames(item);
  return names.length ? `${actionLabel(item, "applies_to")}: ${names.join(", ")}` : "";
}

/** REQUIRED_LABEL is the status of an item an admin manifest requires. */
export const REQUIRED_LABEL = "Managed by your organisation";

/**
 * statusLabel is the catalog status in sentence case: "Will be installed". An
 * item the organisation requires says so instead; it has no action to take.
 * An installed or pending item uses its installed or pending label.
 */
export function statusLabel(item: OptionalInstallItem): string {
  if (item.isRequired) {
    return REQUIRED_LABEL;
  }
  switch (item.status) {
    case "WillBeInstalled":
      return actionLabel(item, "pending_install");
    case "WillBeRemoved":
      return actionLabel(item, "pending_remove");
  }
  const partial = partialPlacements(item);
  if (partial.length) {
    return `Installed in ${partial.map((placement) => placement.label).join(", ")}`;
  }
  if (item.status === "Installed") {
    return actionLabel(item, "installed");
  }
  const label = stateLabel(item.status);
  return label[0] + label.slice(1).toLowerCase();
}

/**
 * cardStatus is a Home card's idle line: only what its button cannot say. A
 * pending item's button reads only "Cancel", a required item has none, and a
 * partly installed item's Install does not say where it already is; otherwise
 * the button already names the state, so the line is "".
 */
export function cardStatus(item: OptionalInstallItem): string {
  const pending = item.status === "WillBeInstalled" || item.status === "WillBeRemoved";
  return item.isRequired || pending || partialPlacements(item).length > 0 ? statusLabel(item) : "";
}

/**
 * opensDetails says whether a click inside a card opens its detail page: a
 * click anywhere but on a control, so a card's buttons keep their own action,
 * and not one that ends a drag selecting the card's text (selecting is false
 * when nothing is selected).
 */
export function opensDetails(target: Pick<Element, "closest"> | null, selecting: boolean): boolean {
  return !selecting && target !== null && target.closest("button, a, input, select, textarea, label") === null;
}

/**
 * openControl is the class of a card's button that opens its item, which
 * keyboard and screen-reader users reach it by and which gets focus back when
 * the item closes: the Details button for item_view page, the item's name for
 * dialog.
 */
export function openControl(itemView: BrandingView["itemView"]): "card-details" | "card-open" {
  return itemView === "dialog" ? "card-open" : "card-details";
}

/** The parts of a DOMRect morphFrom reads. */
export type Box = { left: number; top: number; width: number; height: number };

/**
 * morphFrom is the transform that lays the item dialog, at dialog, over its
 * card, at card: what the dialog grows out of and shrinks back into. Null
 * when the card is not wholly inside the viewport (scrolled out of view, even
 * partly) or either box is empty; the dialog then just appears and
 * disappears rather than flying in from off screen.
 */
export function morphFrom(card: Box, dialog: Box, viewport: { width: number; height: number }): string | null {
  if (card.width <= 0 || card.height <= 0 || dialog.width <= 0 || dialog.height <= 0) {
    return null;
  }
  if (card.left < 0 || card.top < 0 || card.left + card.width > viewport.width || card.top + card.height > viewport.height) {
    return null;
  }
  const scale = `scale(${card.width / dialog.width}, ${card.height / dialog.height})`;
  return `translate(${card.left - dialog.left}px, ${card.top - dialog.top}px) ${scale}`;
}

/**
 * itemPlacements is the item's placements the detail page lists one row each
 * for, in the service's order; [] for an item it sends none for. A cached list
 * is untrusted shape-wise, so only well-formed entries count.
 */
export function itemPlacements(item: Pick<OptionalInstallItem, "placements">): OptionalInstallPlacement[] {
  return Array.isArray(item.placements)
    ? item.placements.filter(
        (placement) =>
          typeof placement?.name === "string" &&
          placement.name !== "" &&
          typeof placement.label === "string" &&
          typeof placement.status === "string",
      )
    : [];
}

/**
 * partialPlacements is where a partly installed item is installed: its
 * installed placements when some but not all of them are, else [].
 */
export function partialPlacements(item: Pick<OptionalInstallItem, "placements">): OptionalInstallPlacement[] {
  const placements = itemPlacements(item);
  const installed = placements.filter((placement) => placement.isInstalled === true);
  return installed.length < placements.length ? installed : [];
}

/**
 * placementStatus is one placement row's status, worded by the item's labels
 * as the item's own status would be.
 */
export function placementStatus(item: OptionalInstallItem, placement: OptionalInstallPlacement): string {
  return statusLabel({ ...item, isRequired: false, placements: undefined, status: placement.status });
}

/**
 * placementAction is a placement row's button: the item's own action rules,
 * applied to the one placement. Cancelling a pending placement reuses the
 * opposite mutation for that placement, as deriveAction does for the item. An
 * item the organisation requires goes everywhere, so its rows have none.
 */
export function placementAction(item: OptionalInstallItem, placement: OptionalInstallPlacement): ItemAction | null {
  if (item.isRequired) {
    return null;
  }
  const name = placement.name;
  switch (placement.status) {
    case "WillBeInstalled":
      return { kind: "cancel", label: actionLabel(item, "cancel"), method: "RemoveItem", placement: name };
    case "WillBeRemoved":
      return { kind: "cancel", label: actionLabel(item, "cancel"), method: "InstallItem", placement: name };
    case "Installed":
      // As for the item: only a selected item offers Remove. Removing one
      // placement of an unselected item would remove it everywhere.
      if (item.isManaged) {
        return { kind: "undo", label: actionLabel(item, "remove"), method: "RemoveItem", placement: name };
      }
      return { kind: "do", label: actionLabel(item, "install"), method: "InstallItem", placement: name };
    default:
      return { kind: "do", label: actionLabel(item, "install"), method: "InstallItem", placement: name };
  }
}

/** REQUESTED_STATE is local: the service accepted the request, no event yet. */
export const REQUESTED_STATE = "Requested";
/** STREAM_ENDED_STATE is local: the stream ended before any terminal record. */
export const STREAM_ENDED_STATE = "stream_ended";
/** ERROR_STATE is local: the request or watch call itself failed. */
export const ERROR_STATE = "error";

const LOCAL_STATE_LABELS: Record<string, string> = {
  [STREAM_ENDED_STATE]: "Stream ended before a result",
  [ERROR_STATE]: "Request failed",
};

export function stateLabel(state: string): string {
  const raw = state.trim();
  if (!raw) {
    return "Unknown";
  }
  return LOCAL_STATE_LABELS[raw] ?? raw.replace(/([a-z])([A-Z])/g, "$1 $2");
}

// Only these four states end an operation. ItemCompleted and ItemFailed are
// per-item records: a dependency can fail while the operation keeps running.
const TERMINAL_STATES = new Set(["Succeeded", "Failed", "Deferred", "Canceled"]);

// Only these states describe work on the event's own item, so only they carry a
// percentage worth showing on a determinate bar.
const ITEM_PHASE_STATES = new Set(["Downloading", "Installing", "Removing"]);

export function isTerminalState(state: string): boolean {
  return TERMINAL_STATES.has(state.trim());
}

export function isItemPhase(state: string): boolean {
  return ITEM_PHASE_STATES.has(state.trim());
}

/** localErrorState keeps a premature stream end distinguishable from any other failure. */
export function localErrorState(error: string): string {
  return /stream ended before a terminal event/i.test(error) ? STREAM_ENDED_STATE : ERROR_STATE;
}

/** trackOperation records an accepted operation so only its item is disabled. */
export function trackOperation(
  active: ActiveOperations,
  operationId: string,
  itemName: string,
): ActiveOperations {
  return new Map(active).set(operationId, itemName);
}

export function releaseOperation(
  active: ActiveOperations,
  operationId: string,
): ActiveOperations {
  const next = new Map(active);
  next.delete(operationId);
  return next;
}

export function isItemActive(active: ActiveOperations, itemName: string): boolean {
  for (const name of active.values()) {
    if (name === itemName) {
      return true;
    }
  }
  return false;
}

/**
 * busyAction is the action an item's buttons are busy with, or undefined when
 * they are free: a request the service has not answered yet (requesting, by
 * item), else the action of an operation still tracked in active. An ended
 * operation stays tracked until the list fetched after its end arrives, so a
 * button goes from its action to busy to the opposite action with no
 * clickable stale label between.
 */
export function busyAction(
  itemName: string,
  requesting: ReadonlyMap<string, ItemAction>,
  active: ActiveOperations,
  operations: ReadonlyMap<string, OperationView>,
): ItemAction | undefined {
  const asked = requesting.get(itemName);
  if (asked) {
    return asked;
  }
  for (const [operationId, name] of active) {
    if (name === itemName) {
      return operations.get(operationId)?.action;
    }
  }
  return undefined;
}

/**
 * endedOperations are the tracked operations that have ended; a refresh
 * started after their end releases them once its list arrives.
 */
export function endedOperations(
  active: ActiveOperations,
  operations: ReadonlyMap<string, OperationView>,
): string[] {
  return [...active.keys()].filter((operationId) => operations.get(operationId)?.outcome !== "active");
}

/** ownsBusy is whether a button (a placement row's when row is set) is the one the busy action works for. */
function ownsBusy(action: ItemAction, busy: ItemAction, row?: string): boolean {
  return row === undefined || busy.placement === row || (busy.placement === undefined && busy.method === action.method);
}

/**
 * actionText is an action button's label: the action's own, or while its item
 * is busy, the item's installing or removing label for the busy action (a
 * busy Cancel keeps its own label). A
 * placement row (row set) reads busy only for its own placement, or for an
 * action on the whole item that does what the row would.
 */
export function actionText(
  item: Pick<OptionalInstallItem, "actionLabels">,
  action: ItemAction,
  busy: ItemAction | undefined,
  row?: string,
): string {
  if (!busy || !ownsBusy(action, busy, row)) {
    return action.label;
  }
  // A Cancel sends the opposite mutation, so "Removing…" would misread it.
  if (busy.kind === "cancel") {
    return busy.label;
  }
  return actionLabel(item, busy.method === "RemoveItem" ? "removing" : "installing");
}

/**
 * shouldAcceptRecord decides whether a newly arrived status record still
 * belongs to an operation. A finished operation still takes a late record,
 * which insertRecord files before the terminal one; an operation that ended in
 * a local error does not, because its last record is the local one.
 */
export function shouldAcceptRecord(outcome: OperationOutcome): boolean {
  return outcome !== "error";
}

/**
 * insertRecord files a record in its operation's timeline. Wails emits every
 * event on its own goroutine, so records can arrive in any order; a wire
 * record goes where its seq puts it, so the last record is always the latest
 * the service recorded. A local record (no seq) is appended. It returns false
 * for a seq the timeline already holds.
 */
export function insertRecord(records: ActivityRecord[], record: ActivityRecord): boolean {
  const seq = record.seq;
  if (seq === undefined) {
    records.push(record);
    return true;
  }
  if (records.some((existing) => existing.seq === seq)) {
    return false;
  }
  let at = records.length;
  while (at > 0 && (records[at - 1].seq ?? 0) > seq) {
    at -= 1;
  }
  records.splice(at, 0, record);
  return true;
}

/**
 * isProgressTick is true for a record, already filed in records, that only
 * moves its phase's percentage: the record before it is the same item in the
 * same state. Activity keeps phases, not ticks, so a download that reports
 * every percent does not push the rest of the history out.
 */
export function isProgressTick(records: ActivityRecord[], record: ActivityRecord): boolean {
  const previous = records[records.indexOf(record) - 1];
  return previous !== undefined && previous.itemName === record.itemName && previous.state === record.state;
}

/**
 * repeatsOutcome is true for the ItemCompleted or ItemFailed record of the
 * operation's own item. The terminal record that follows says the same, so
 * Activity keeps these two states only for a dependency or an updater.
 */
export function repeatsOutcome(item: Pick<OptionalInstallItem, "itemName">, record: ActivityRecord): boolean {
  return (record.state === "ItemCompleted" || record.state === "ItemFailed") && record.itemName === item.itemName;
}

/**
 * requestText is Activity's line for an accepted request: the button's label,
 * and for a placement row the placement it was for ("Install requested for
 * Claude Code").
 */
export function requestText(item: Pick<OptionalInstallItem, "placements">, action: ItemAction): string {
  const where = itemPlacements(item).find((placement) => placement.name === action.placement)?.label;
  return `${action.label} requested${where ? ` for ${where}` : ""}`;
}

/** isNewer is whether a belongs above b in newest-first Activity. */
function isNewer(a: ActivityRecord, b: ActivityRecord): boolean {
  const at = Date.parse(a.timestampUtc);
  const bt = Date.parse(b.timestampUtc);
  if (at !== bt) {
    return at > bt;
  }
  return a.operationId === b.operationId && (a.seq ?? 0) > (b.seq ?? 0);
}

/**
 * addActivity files a record in newest-first Activity by its timestamp, then
 * by seq within one operation, and keeps the newest `limit` records. A record
 * with an unreadable timestamp, or a tie between operations, goes on top.
 */
export function addActivity(
  activity: ActivityRecord[],
  record: ActivityRecord,
  limit: number,
): ActivityRecord[] {
  let at = 0;
  while (at < activity.length && isNewer(activity[at], record)) {
    at += 1;
  }
  return [...activity.slice(0, at), record, ...activity.slice(at)].slice(0, limit);
}

/**
 * statusRecord narrows a wire status event to a local display record. The
 * record is display history only: installed state comes from a later
 * authoritative list, never from a progress event.
 */
export function statusRecord(status: OperationStatus): ActivityRecord {
  const message = status.message.trim();
  const detail = (status.errorMessage ?? "").trim();
  return {
    operationId: status.operationId,
    itemName: status.itemName,
    displayName: status.displayName || status.itemName,
    state: status.state,
    message: [message, detail && detail !== message ? `(${detail})` : ""].filter(Boolean).join(" "),
    timestampUtc: status.timestampUtc,
    ...(typeof status.seq === "number" ? { seq: status.seq } : {}),
    ...(typeof status.progressPercent === "number" ? { progressPercent: status.progressPercent } : {}),
    ...(detail ? { detail } : {}),
    ...(status.state === "Canceled" && status.canceledBy ? { canceledBy: status.canceledBy } : {}),
    ...(status.errorCode ? { errorCode: status.errorCode } : {}),
  };
}

/** localRecord is a display-only entry for work the service never reported. */
export function localRecord(
  operationId: string,
  item: Pick<OptionalInstallItem, "itemName" | "displayName">,
  state: string,
  message: string,
  timestampUtc: string,
): ActivityRecord {
  return {
    operationId,
    itemName: item.itemName,
    displayName: item.displayName || item.itemName,
    state,
    message,
    timestampUtc,
  };
}

// How Activity names who ended an operation; the service sends "user" or "service".
const CANCELED_BY: Record<string, string> = { user: "by you", service: "by the service" };

// A service error's leading code, such as "command_failed: ".
const ERROR_CODE_PREFIX = /^[a-z]+(?:_[a-z]+)+:\s*/;

/**
 * activityText is what a record says happened, in plain words. The state
 * carries the meaning, so the service's stock message ("Operation queued") and
 * a message that only repeats the item's name are left out; a message is
 * quoted only where it is the reason for a failure or a wait.
 */
function activityText(record: ActivityRecord): string {
  const message = record.message.trim();
  const because = (what: string, why: string) => (why ? `${what} — ${why}` : what);
  switch (record.state) {
    case REQUESTED_STATE:
      return message || "Requested";
    case "Queued":
      return "Waiting to start";
    case "ItemCompleted":
      return "Done";
    case "Succeeded":
      return "Finished";
    case "ItemFailed":
      return because("Failed", message);
    case "Failed":
      return because("Failed", record.detail || (/^operation failed$/i.test(message) ? "" : message));
    case "Deferred": {
      const apps = blockingApps(record);
      return apps ? `Waiting — close ${apps} to continue` : because("Postponed", message);
    }
    case "Canceled": {
      // A cancel says who did it, and that replaces the service's message.
      const who = record.canceledBy ? (CANCELED_BY[record.canceledBy] ?? `by ${record.canceledBy}`) : "";
      return who ? `Canceled ${who}` : "Canceled";
    }
    case STREAM_ENDED_STATE:
      // The message is the raw stream error, which says the same less plainly.
      return "Lost contact with the service before it finished";
    case ERROR_STATE:
      return because("Request failed", message.replace(ERROR_CODE_PREFIX, ""));
    default: {
      const named = message === record.displayName || message === record.itemName;
      return because(stateLabel(record.state), named ? "" : message);
    }
  }
}

export function activityLine(record: ActivityRecord): string {
  const when = new Date(record.timestampUtc);
  const stamp = Number.isNaN(when.getTime())
    ? record.timestampUtc
    : when.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
  return `${stamp} — ${record.displayName || record.itemName}: ${activityText(record)}`;
}

/** progressLabel names the item a determinate bar belongs to, never the operation. */
export function progressLabel(record: ActivityRecord): string {
  return `${record.displayName || record.itemName} — ${stateLabel(record.state)}`;
}

/**
 * CardProgress is what an item's own card shows for the operation the user
 * started on it: one short line beside a spinner while it runs, then only an
 * outcome worth acting on (Failed, Deferred, a request error). The full
 * timeline is in Activity, the way Managed Software Center keeps its log off
 * the main view.
 */
export type CardProgress = {
  label: string;
  /** The record's own-item percentage, when the engine measured this phase. */
  percent?: number;
  outcome: OperationOutcome;
  /** A finished outcome that needs no attention, shown as plain text. */
  plain?: boolean;
};

// Records that arrive before the service has started work on anything.
const WAITING_STATES = new Set([REQUESTED_STATE, "Queued"]);

// The installer's deferral reason when a blocking application is running. An
// older service sends no errorCode, so the message is matched as well.
const BLOCKING_APPS = /^blocking application\(s\) running: (.+)$/i;

/** blockingApps names the running applications a Deferred record waits on, when that is its reason. */
function blockingApps(record: ActivityRecord): string | undefined {
  // missing_software's message already says what to install (Needs …).
  return record.errorCode === "missing_software" ? undefined : BLOCKING_APPS.exec(record.message.trim())?.[1];
}

/**
 * phaseLabel is a running phase's line. The item's own install or removal uses
 * its installing or removing label; another item's phase (who) is named.
 */
function phaseLabel(item: Pick<OptionalInstallItem, "actionLabels">, state: string, who: string): string {
  if (!who && state === "Installing") {
    return actionLabel(item, "installing");
  }
  if (!who && state === "Removing") {
    return actionLabel(item, "removing");
  }
  return `${stateLabel(state)}${who}…`;
}

/** outcomeLabel words a finished operation for its card: what happened, then why. */
function outcomeLabel(record: ActivityRecord, method: ActionMethod): string {
  const message = record.message.trim();
  switch (record.state) {
    case "Deferred": {
      const apps = blockingApps(record);
      return apps ? `Waiting: close ${apps} to continue` : message || "Deferred";
    }
    case "Canceled":
      return "Canceled";
    case "Failed": {
      const verb = method === "RemoveItem" ? "Removal" : "Install";
      const why = record.detail || message;
      return why ? `${verb} failed · ${why}` : `${verb} failed`;
    }
    default:
      return message ? `${stateLabel(record.state)} · ${message}` : stateLabel(record.state);
  }
}

export function cardProgress(
  item: Pick<OptionalInstallItem, "itemName" | "actionLabels">,
  records: ActivityRecord[],
  outcome: OperationOutcome,
  method: ActionMethod = "InstallItem",
  notice = "",
): CardProgress | null {
  const latest = records[records.length - 1];
  if (!latest) {
    return null;
  }
  if (outcome === "active" && notice) {
    // A refused cancel: the service's reason, while the work carries on.
    return { label: notice, outcome };
  }
  if (outcome === "active") {
    const own = latest.itemName === item.itemName;
    if (isItemPhase(latest.state)) {
      // A dependency or updater can take over mid-run; name it so the
      // percentage (which is scoped to that item) is not read as this item's.
      const who = own ? "" : ` ${latest.displayName || latest.itemName}`;
      return {
        label: phaseLabel(item, latest.state, who),
        percent: typeof latest.progressPercent === "number" ? latest.progressPercent : undefined,
        outcome,
      };
    }
    if (WAITING_STATES.has(latest.state)) {
      return { label: "Waiting…", outcome };
    }
    return { label: own ? `${stateLabel(latest.state)}…` : "In progress…", outcome };
  }
  // A success needs no epilogue: the refreshed list status is authoritative.
  if (outcome === "terminal" && latest.state === "Succeeded") {
    return null;
  }
  // The user asked for a cancel, so it is not a problem to flag.
  return { label: outcomeLabel(latest, method), outcome, ...(latest.state === "Canceled" ? { plain: true } : {}) };
}

/**
 * needsAttention is true for a finished outcome the user has to act on: a
 * failure or a deferral. Only these appear on a Home card; the button already
 * shows the item's state and the bottom strip shows running work.
 */
export function needsAttention(progress: CardProgress | null): boolean {
  return progress !== null && progress.outcome !== "active" && !progress.plain;
}

/** progressText is a progress line as it reads: the label, then any percentage. */
export function progressText(progress: CardProgress): string {
  return typeof progress.percent === "number" ? `${progress.label} ${progress.percent}%` : progress.label;
}

/**
 * StripView is what the bottom "current operation" strip shows: one running
 * operation, its progress, and where it sits among everything still running.
 */
export type StripView = {
  operation: OperationView;
  progress: CardProgress;
  position: number;
  total: number;
};

/**
 * stripView picks the operation the strip reports on: the earliest running one
 * the service has started work on, else the earliest running one. The others
 * read "Waiting…" on their own cards. Null hides the strip.
 */
export function stripView(operations: Iterable<OperationView>): StripView | null {
  const running = [...operations].filter((operation) => operation.outcome === "active");
  if (running.length === 0) {
    return null;
  }
  const working = running.findIndex((operation) =>
    operation.records.some((record) => !WAITING_STATES.has(record.state)),
  );
  const index = Math.max(working, 0);
  const operation = running[index];
  return {
    operation,
    // A refused cancel's reason replaces the phase: the strip's Cancel asked.
    progress: cardProgress(operation.item, operation.records, "active", operation.action.method, operation.notice) ?? {
      label: "Waiting…",
      outcome: "active",
    },
    position: index + 1,
    total: running.length,
  };
}

/** stripLine is the strip's text: the phase, then "n of N". */
export function stripLine(strip: StripView): string {
  return `${progressText(strip.progress)} · ${strip.position} of ${strip.total}`;
}

/**
 * QueueView is the strip under branding progress: button, where each item's
 * button carries its own progress: a count of the running operations, and a
 * row each for the list it opens.
 */
export type QueueView = {
  /** "Installing 1 of 3": the working operation's verb and place, no phase. */
  line: string;
  /** The running operations, the working one first, then in service order. */
  rows: { operation: OperationView; text: string }[];
};

// The strip's verb for the working operation: Waiting until the service
// starts on it, else the item's own installing or removing label without its
// ellipsis ("Adding" for a skill that says "Adding…").
function queueVerb(operation: OperationView): string {
  if (!operation.records.some((record) => !WAITING_STATES.has(record.state))) {
    return "Waiting";
  }
  if (operation.action.kind === "cancel") {
    return "Canceling";
  }
  const label = actionLabel(operation.item, operation.action.method === "RemoveItem" ? "removing" : "installing");
  return label.replace(/\s*(…|\.\.\.)\s*$/, "");
}

/**
 * queueView is what the strip shows when progress is in the buttons: null
 * (the strip hides) while fewer than two operations run, since the one
 * running shows on its own button. Its place is stripView's.
 */
export function queueView(operations: Iterable<OperationView>): QueueView | null {
  // operations may be a one-shot iterator (a Map's values()).
  const running = [...operations].filter((operation) => operation.outcome === "active");
  const shown = stripView(running);
  if (!shown || shown.total < 2) {
    return null;
  }
  const ordered = [shown.operation, ...running.filter((operation) => operation !== shown.operation)];
  return {
    line: `${queueVerb(shown.operation)} ${shown.position} of ${shown.total}`,
    rows: ordered.map((operation) => ({
      operation,
      // The phase or a refused cancel's reason, without a percentage: the
      // item's button shows that.
      text:
        cardProgress(operation.item, operation.records, "active", operation.action.method, operation.notice)
          ?.label ?? "Waiting…",
    })),
  };
}

/** WAITING_LABEL is what a button reads while its request is with the service or queued. */
export const WAITING_LABEL = "Waiting…";

/**
 * ActionProgress is what a busy action button shows under branding progress:
 * button. waiting: a spinner, before the service starts the work. working: a
 * bar with the phase, filled to percent when the service measured the phase,
 * indeterminate otherwise (never a made-up percentage). done: a full bar, for
 * a success whose new state the next list brings. ended: no bar, for an
 * operation that failed, was canceled or deferred; its status line says why.
 */
export type ActionProgress = {
  label: string;
  phase: "waiting" | "working" | "done" | "ended";
  percent?: number;
};

/**
 * actionProgress is the progress an item's action button carries: null while
 * the button is free or busy with another placement's action, so it keeps its
 * label. operation is the item's latest, undefined while the request is not
 * answered yet. A placement row (row set) carries an action on the whole item
 * that does what the row would, as actionText reads.
 */
export function actionProgress(
  item: Pick<OptionalInstallItem, "itemName" | "actionLabels">,
  action: ItemAction,
  busy: ItemAction | undefined,
  operation: Pick<OperationView, "records" | "outcome" | "action"> | undefined,
  row?: string,
): ActionProgress | null {
  if (!busy || !ownsBusy(action, busy, row)) {
    return null;
  }
  if (!operation) {
    return { label: WAITING_LABEL, phase: "waiting" };
  }
  const latest = operation.records.at(-1);
  if (operation.outcome !== "active") {
    // The item stays busy until the list after the end arrives, as under
    // strip, which on a busy service can take a while. Meanwhile only a
    // success reads full; a failed, canceled or deferred operation shows its
    // action again, with no bar or spinner that says work is still going.
    return operation.outcome === "terminal" && latest?.state === "Succeeded"
      ? { label: actionText(item, action, busy, row), phase: "done", percent: 100 }
      : { label: action.label, phase: "ended" };
  }
  if (!latest || WAITING_STATES.has(latest.state)) {
    return { label: WAITING_LABEL, phase: "waiting" };
  }
  const label = actionText(item, action, busy, row);
  // A Cancel sends the opposite mutation, so its own label says more than the
  // phase. Between phases (a dependency just finished) the button keeps its
  // installing or removing label rather than naming a record.
  if (busy.kind === "cancel" || !isItemPhase(latest.state)) {
    return { label, phase: "working" };
  }
  const progress = cardProgress(item, operation.records, "active", operation.action.method);
  const percent = progress?.percent;
  return {
    label: progress?.label ?? label,
    phase: "working",
    ...(typeof percent === "number" && Number.isFinite(percent)
      ? { percent: Math.min(100, Math.max(0, percent)) }
      : {}),
  };
}

/**
 * announcement is what the announcer says once for an operation's latest
 * record under branding progress: button, where the buttons are not live
 * regions: the item and its phase, never a percentage, or "" for nothing.
 * Main keeps the last line said per operation, so two items whose ticks
 * alternate are not re-read each time.
 */
export function announcement(operation: Pick<OperationView, "item" | "records" | "outcome" | "action">): string {
  if (operation.outcome !== "active") {
    return "";
  }
  const progress = cardProgress(operation.item, operation.records, "active", operation.action.method);
  return progress ? `${operation.item.displayName || operation.item.itemName}: ${progress.label}` : "";
}

/**
 * deriveAction maps an item to its single primary action. Cancelling a pending
 * install or removal reuses the opposite mutation; it is not a new protocol
 * operation. An item the organisation requires has no action: the service
 * refuses to remove it, and every managed run installs it anyway.
 */
export function deriveAction(item: OptionalInstallItem): ItemAction | null {
  if (item.isRequired) {
    return null;
  }
  switch (item.status) {
    case "WillBeInstalled":
    case "WillBeRemoved": {
      const method = item.status === "WillBeInstalled" ? "RemoveItem" : "InstallItem";
      const cancel: ItemAction = { kind: "cancel", label: actionLabel(item, "cancel"), method };
      // The opposite mutation for the whole item would also undo, or add,
      // the placements that are not pending. One pending placement is
      // cancelled alone; several of them, short of all, only on their rows.
      const placements = itemPlacements(item);
      const pending = placements.filter((placement) => placement.status === item.status);
      if (pending.length === 0 || pending.length === placements.length) {
        return cancel;
      }
      return pending.length === 1 ? { ...cancel, placement: pending[0].name } : null;
    }
    default:
      // A partly installed item keeps Install, which adds the rest; its
      // placements are removed one at a time on the detail page.
      return item.isManaged && partialPlacements(item).length === 0
        ? { kind: "undo", label: actionLabel(item, "remove"), method: "RemoveItem" }
        : { kind: "do", label: actionLabel(item, "install"), method: "InstallItem" };
  }
}

export function fromCache(cached: CachedList | null): ListView {
  if (!cached) {
    return { items: [], source: "loading", savedAtUtc: "" };
  }
  return { items: cached.items, source: "cache", savedAtUtc: cached.savedAtUtc };
}

export function withLive(items: OptionalInstallItem[], nowUtc: string): ListView {
  return { items, source: "live", savedAtUtc: nowUtc };
}

/** withFailure keeps whatever is already rendered and marks it stale. */
export function withFailure(view: ListView): ListView {
  return { ...view, source: "stale" };
}

export function bannerMessage(view: ListView, error: string): string {
  switch (view.source) {
    case "loading":
      return `Connecting to the ${DEFAULT_PRODUCT} service…`;
    case "cache":
      return "Showing cached software. Refreshing…";
    case "live":
      return "Service connected";
    case "stale":
      return (
        (view.items.length
          ? `Service unavailable — showing cached software${formatSavedAt(view.savedAtUtc)}.`
          : "Service unavailable and no cached software is stored.") + formatReason(error)
      );
  }
}

/**
 * connectionLabel is the short app-bar text; bannerMessage is the full sentence
 * with the reason, shown on hover and read by assistive technology.
 */
export function connectionLabel(view: ListView): string {
  switch (view.source) {
    case "loading":
      return "Connecting…";
    case "cache":
      return "Showing cached software";
    case "live":
      return "Service connected";
    case "stale":
      return "Service unavailable";
  }
}

/**
 * showLoading is true until the service first answers, when no saved list
 * stands in: the loading screen is up. A failed first try takes it down for
 * the page and its Retry, which puts it back up.
 *
 * A build with LOADING_ALWAYS also keeps it up until splashMs have passed
 * since the page started (elapsedMs), whatever the view: a splash on every
 * launch. Elapsed time only grows, so once down the splash never returns.
 */
export function showLoading(view: ListView, splashMs = 0, elapsedMs = 0): boolean {
  return view.source === "loading" || splashLeft(splashMs, elapsedMs) > 0;
}

/**
 * splashLeft is how many ms of the splash remain, or 0 once it is over or
 * there is none. While it is above 0 the screen's content shows without the
 * 200 ms wait (data-always), and a timer must take the splash down at 0.
 */
export function splashLeft(splashMs: number, elapsedMs: number): number {
  return Math.max(0, splashMs - elapsedMs);
}

/**
 * splashMs is how long the loading screen stays up at least on every launch,
 * from the build's VITE_LOADING_ALWAYS and VITE_LOADING_SECONDS, or 0 when the
 * build has no splash. brand-check validated both; a missing or bad seconds
 * value falls back to the default 3, and one out of range is clamped to
 * 0.1-30, so a direct vite build cannot overflow a timer.
 */
export function splashMs(always: string | undefined, seconds: string | undefined): number {
  if (always !== "true") {
    return 0;
  }
  const value = Number(seconds || NaN);
  const clamped = Number.isFinite(value) ? Math.min(30, Math.max(0.1, value)) : 3;
  return Math.round(clamped * 1000);
}

const SPLASH_SHOWN_KEY = "sofcat.splashShown";

/**
 * splashShownBefore says whether this page is a reload within one launch
 * that already had the splash, and marks it shown for the next one. The mark
 * lives in sessionStorage, which a reload keeps and a new launch (a new
 * WebView) does not. Storage that is missing or throws counts as unmarked,
 * so the splash shows.
 */
export function splashShownBefore(session: () => Pick<Storage, "getItem" | "setItem">): boolean {
  let shown = false;
  try {
    const store = session();
    shown = store.getItem(SPLASH_SHOWN_KEY) === "1";
    store.setItem(SPLASH_SHOWN_KEY, "1");
  } catch {
    // A store that cannot be read is unmarked; one that cannot be written
    // keeps what it read.
  }
  return shown;
}

/** pageSplashMs is the build's splash for a fresh page, and none for a reload that already had it. */
export function pageSplashMs(buildSplashMs: number, shownBefore: boolean): number {
  return shownBefore ? 0 : buildSplashMs;
}

export function showRetry(view: ListView): boolean {
  return view.source === "stale";
}

/** formatReason labels the underlying error so it does not read as a sentence fragment. */
function formatReason(error: string): string {
  const raw = error.trim();
  return raw ? ` Reason: ${raw}` : "";
}

function formatSavedAt(savedAtUtc: string): string {
  const saved = new Date(savedAtUtc);
  return Number.isNaN(saved.getTime()) ? "" : ` from ${saved.toLocaleString()}`;
}

// Branding is admin configuration the service resolved from policy or
// config.yaml; no catalog data is involved. The service already validated
// every field; these checks repeat the ones that guard what the WebView does
// with a value (open a URL, build a data: URL, set a CSS colour).

// The build-time product name (VITE_PRODUCT_NAME, set by `just brand=…`). Node
// tests have no import.meta.env and get the stock name.
const DEFAULT_PRODUCT: string = import.meta.env?.VITE_PRODUCT_NAME ?? "SofCat";
const DEFAULT_HELP_LABEL = "Get help";
const DEFAULT_LIST_HEADING = "Available software";
const DEFAULT_APPLIES_TO_FILTER_LABEL = "Works in";
const IMAGE_MIMES = new Set(["image/png", "image/jpeg", "image/svg+xml"]);
// The title_size and tagline_size names, as factors on the default size.
const FONT_SCALES = new Map([
  ["small", 0.85],
  ["medium", 1],
  ["large", 1.25],
  ["x-large", 1.5],
]);

// The content_width names: compact keeps the layout from before the key, wide
// (the default) widens the column and lets the cards fill a sparse row, and
// full drops the column's cap. style.css draws each from :root[data-content-width].
const CONTENT_WIDTHS = new Set(["compact", "wide", "full"]);

// The filter_placement names: right (the default) sets the filters on the
// heading's row, left keeps them above the heading as before the key.
const FILTER_PLACEMENTS = new Set(["right", "left"]);

// The nav_placement names: top (the default) keeps the app bar across the
// window, as before the key, and left makes it a column down the window's
// side. style.css draws each from :root[data-nav-placement].
const NAV_PLACEMENTS = new Set(["top", "left"]);

// The category_filter names: select (the default) keeps the Category select
// beside the search field, list shows the categories as buttons in a left
// bar. style.css shows the list only there, so with the bar at the top list
// is select.
const CATEGORY_FILTERS = new Set(["select", "list"]);

// The app_bar_brand names: full (the default) shows the mark and the name,
// mark the mark only, none neither; style.css hides them only visually.
const APP_BAR_BRANDS = new Set(["full", "mark", "none"]);

// The banner_align names; left is the default.
const BANNER_ALIGNS = new Set(["left", "center", "right"]);

// The banner_height names; medium, the default, is the banner's height from
// before the key. style.css holds the heights.
const BANNER_HEIGHTS = new Set(["small", "medium", "large", "x-large"]);

// The link_placement names: banner, the default and the place from before
// the key, or app_bar, at the app bar's end before the connection status.
const LINK_PLACEMENTS = new Set(["banner", "app_bar"]);

// The link_position names: auto, the default, lays the link out with the
// content as banner_align says; a corner name puts it in that corner of the
// banner. style.css holds the layouts.
const LINK_POSITIONS = new Set(["auto", "top_left", "top_right", "bottom_left", "bottom_right"]);

// The progress names: strip, the default and the look from before the key,
// shows running work in the bottom strip; button shows it in each item's
// action button, the strip only counting a queue of two or more.
const PROGRESSES = new Set(["strip", "button"]);
// The item_view names: page, the default and the view from before the key,
// replaces the list with the item's detail page; dialog opens the same
// details in a dialog over the list.
const ITEM_VIEWS = new Set(["page", "dialog"]);
// The scrollbar names: system, the default and the look from before the key,
// is the window's own scrollbar; slim is a thin one on the content alone.
const SCROLLBARS = new Set(["system", "slim"]);

/** oneOf returns payload[key] when it is one of allowed, else fallback. */
function oneOf<T extends string>(payload: Record<string, unknown>, key: string, allowed: Set<string>, fallback: T): T {
  const value = text(payload, key);
  return allowed.has(value) ? (value as T) : fallback;
}

export function isHexColor(value: string): boolean {
  return /^#[0-9a-fA-F]{6}$/.test(value);
}

/** httpUrl returns value when it is an absolute http(s) URL, else "". */
export function httpUrl(value: string): string {
  try {
    const url = new URL(value);
    return url.protocol === "http:" || url.protocol === "https:" ? url.href : "";
  } catch {
    return "";
  }
}

function channel(hex: string, at: number): number {
  const c = parseInt(hex.slice(at, at + 2), 16) / 255;
  return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
}

/** onAccent picks white or near-black text, whichever contrasts more with hex. */
export function onAccent(hex: string): string {
  const luminance = 0.2126 * channel(hex, 1) + 0.7152 * channel(hex, 3) + 0.0722 * channel(hex, 5);
  const ink = 0.0116; // relative luminance of #1b1b1f
  return 1.05 / (luminance + 0.05) >= (luminance + 0.05) / (ink + 0.05) ? "#ffffff" : "#1b1b1f";
}

function text(payload: Record<string, unknown>, key: string): string {
  const value = payload[key];
  return typeof value === "string" ? value.trim() : "";
}

/** imageSrc builds a data: URL from an image type the shell accepts and clean base64, else "". */
function imageSrc(mime: string, base64: string): string {
  return IMAGE_MIMES.has(mime) && /^[A-Za-z0-9+/]+={0,2}$/.test(base64) ? `data:${mime};base64,${base64}` : "";
}

// refresh_interval as the service sends it, in seconds: its default when the
// payload has none (0, or an older service), and at least a minute, since
// every refresh has the service fetch the repository. The longest is what a
// timer can hold.
const DEFAULT_REFRESH_SECONDS = 600;
const MIN_REFRESH_SECONDS = 60;
const MAX_REFRESH_SECONDS = 2_147_483;

/** refreshIntervalMs is the wait between list refreshes, 0 when a negative value turns them off. */
function refreshIntervalMs(seconds: unknown): number {
  if (typeof seconds !== "number" || !Number.isFinite(seconds) || seconds === 0) {
    return DEFAULT_REFRESH_SECONDS * 1000;
  }
  if (seconds < 0) {
    return 0;
  }
  return Math.min(Math.max(seconds, MIN_REFRESH_SECONDS), MAX_REFRESH_SECONDS) * 1000;
}

/** brandingView turns a GetBranding payload (or a cached copy) into what the shell shows. */
export function brandingView(payload: unknown): BrandingView {
  const raw = typeof payload === "object" && payload !== null ? (payload as Record<string, unknown>) : {};
  const title = text(raw, "title");
  const tagline = text(raw, "tagline");
  const logoSrc = imageSrc(text(raw, "logoMime"), text(raw, "logoBase64"));
  const backgroundSrc = imageSrc(text(raw, "backgroundMime"), text(raw, "backgroundBase64"));
  const helpUrl = httpUrl(text(raw, "helpUrl"));
  const accent = isHexColor(text(raw, "accent")) ? text(raw, "accent").toLowerCase() : "";
  // A payload from before link_placement (a cached one too) has none: banner.
  const linkPlacement = oneOf(raw, "linkPlacement", LINK_PLACEMENTS, "banner");
  // A link in the app bar does not bring up a banner of its own.
  const showBanner = Boolean(title || tagline || logoSrc || backgroundSrc || (helpUrl && linkPlacement === "banner"));
  return {
    title,
    tagline,
    logoSrc,
    backgroundSrc,
    backgroundText: text(raw, "backgroundText") === "dark" ? "dark" : "light",
    helpUrl,
    helpLabel: helpUrl ? text(raw, "helpLabel") || DEFAULT_HELP_LABEL : "",
    linkPlacement,
    linkPosition: oneOf(raw, "linkPosition", LINK_POSITIONS, "auto"),
    accent,
    onAccent: accent ? onAccent(accent) : "",
    productName: title || DEFAULT_PRODUCT,
    productMark: (Array.from(title)[0] ?? "").toUpperCase(),
    showBanner,
    listHeading: text(raw, "listHeading") || DEFAULT_LIST_HEADING,
    appliesToFilterLabel: text(raw, "appliesToFilterLabel") || DEFAULT_APPLIES_TO_FILTER_LABEL,
    titleScale: FONT_SCALES.get(text(raw, "titleSize")) ?? 1,
    taglineScale: FONT_SCALES.get(text(raw, "taglineSize")) ?? 1,
    contentWidth: oneOf(raw, "contentWidth", CONTENT_WIDTHS, "wide"),
    filterPlacement: oneOf(raw, "filterPlacement", FILTER_PLACEMENTS, "right"),
    navPlacement: oneOf(raw, "navPlacement", NAV_PLACEMENTS, "top"),
    categoryFilter: oneOf(raw, "categoryFilter", CATEGORY_FILTERS, "select"),
    // Hiding the product's name only makes sense under a banner that shows
    // the organisation; without one the app bar keeps it.
    appBarBrand: showBanner ? oneOf(raw, "appBarBrand", APP_BAR_BRANDS, "full") : "full",
    // A logo banner needs a logo; without one the title shows as text.
    bannerStyle: logoSrc && text(raw, "bannerStyle") === "logo" ? "logo" : "text",
    bannerAlign: oneOf(raw, "bannerAlign", BANNER_ALIGNS, "left"),
    bannerHeight: oneOf(raw, "bannerHeight", BANNER_HEIGHTS, "medium"),
    progress: oneOf(raw, "progress", PROGRESSES, "strip"),
    itemView: oneOf(raw, "itemView", ITEM_VIEWS, "page"),
    scrollbar: oneOf(raw, "scrollbar", SCROLLBARS, "system"),
    refreshIntervalMs: refreshIntervalMs(raw.refreshIntervalSeconds),
    sections: Array.isArray(raw.sections)
      ? raw.sections
          .filter((name): name is string => typeof name === "string" && name.trim() !== "")
          .map((name) => name.trim())
      : [],
  };
}

/**
 * withoutLogo is the branding to show when the logo's data does not decode:
 * no logo anywhere, a text banner, and, with no title to name the
 * organisation there, the app bar's mark and name back.
 */
export function withoutLogo(view: BrandingView): BrandingView {
  return {
    ...view,
    logoSrc: "",
    bannerStyle: "text",
    appBarBrand: view.title ? view.appBarBrand : "full",
  };
}

// Cancel is offered only before the service starts the item's installer or
// uninstaller; the service refuses later and never interrupts a running one.
const CANCELABLE_STATES = new Set([REQUESTED_STATE, "Queued", "Downloading"]);

/** canCancel is whether an operation whose latest record has this state can still be canceled. */
export function canCancel(state: string): boolean {
  return CANCELABLE_STATES.has(state.trim());
}

/**
 * itemCancel is the operation an item's own Cancel, beside its action, is
 * for, while the item's latest operation runs: in the dialog (branding
 * item_view dialog), whose modal puts the strip's Cancel out of reach, and
 * under branding progress: button, where the item's button carries its
 * progress. A card is never a dialog, so it passes "page". Not for an
 * operation that is itself a cancel, whose busy action already is the way
 * out, and not on a page under strip, where the strip has it.
 */
export function itemCancel(
  itemView: BrandingView["itemView"],
  progress: BrandingView["progress"],
  operation: OperationView | undefined,
): OperationView | undefined {
  return (itemView === "dialog" || progress === "button") &&
    operation?.outcome === "active" &&
    operation.action.kind !== "cancel"
    ? operation
    : undefined;
}

/** cancelTitle explains the strip's Cancel button: what it does, or why it is off. */
export function cancelTitle(state: string, pending = false): string {
  if (pending) {
    return "Canceling…";
  }
  return canCancel(state)
    ? "Cancel before the installer starts"
    : "Can't cancel now: work on this item has started, and a running installer is never interrupted";
}

/** NOT_AUTHORIZED_TEXT is what a not_authorized refusal reads as on a card. */
export const NOT_AUTHORIZED_TEXT =
  "Only the person who added this, or an administrator, can change it.";

function isNotAuthorized(error: string): boolean {
  return /^\s*not_authorized:/i.test(error);
}

/**
 * requestErrorText is the line a refused InstallItem or RemoveItem shows: the
 * service's error, except not_authorized, which reads as a plain sentence.
 */
export function requestErrorText(error: string): string {
  return isNotAuthorized(error) ? NOT_AUTHORIZED_TEXT : error;
}

/**
 * refusalText turns a refused CancelOperation into the line the card shows: the
 * service's message without its error code, as a sentence.
 */
export function refusalText(error: string): string {
  if (isNotAuthorized(error)) {
    return NOT_AUTHORIZED_TEXT;
  }
  const message = error.replace(/^\s*operation_not_cancelable:\s*/i, "").trim();
  return message ? message[0].toUpperCase() + message.slice(1) : "The operation can no longer be canceled";
}

/**
 * isRefreshKey says whether a key press is a browser's reload shortcut: F5,
 * Ctrl+R or ⌘R, with Shift or without. The web view's own reload is off, so
 * the storefront answers these by fetching the list again.
 */
export function isRefreshKey(event: {
  key: string;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  repeat: boolean;
}): boolean {
  if (event.repeat || event.altKey) {
    return false;
  }
  if (event.key === "F5") {
    return !event.metaKey;
  }
  return event.key.toLowerCase() === "r" && event.ctrlKey !== event.metaKey;
}

/** The arrow keys' step, Chromium's own. */
const SCROLL_LINE = 40;

/**
 * scrollKeyTarget is how the focused element relates to a scroll key: the
 * body, a control that ignores scroll keys (a button, a link), or one that
 * handles them itself (a text field, a select, anything inside main or inside
 * another scroller, anything while a dialog is open).
 */
export type ScrollKeyTarget = "body" | "control" | "own";

/**
 * forwardedScroll says how far a key press scrolls the content when only the
 * content scrolls (branding scrollbar: slim): a distance in pixels, "start"
 * or "end", or null to leave the key to the browser. WebView2 and WebKit only
 * scroll an ancestor of the focused element, so without this the keys do
 * nothing from the body at launch or from a tab in the app bar. Arrows and
 * Space only from the body: on a control Space presses it, and an arrow may
 * mean something to it. Only the chords a window scrolls by count: Ctrl+Home
 * and Ctrl+End, ⌘↑ and ⌘↓ (WKWebView's top and bottom), Shift only with
 * Space. page is the content's visible height.
 */
export function forwardedScroll(
  event: { key: string; ctrlKey: boolean; metaKey: boolean; altKey: boolean; shiftKey: boolean },
  target: ScrollKeyTarget,
  page: number,
): number | "start" | "end" | null {
  if (target === "own" || event.altKey) {
    return null;
  }
  // Chromium's own page step: seven eighths of the view, so a line overlaps.
  const step = Math.max(page * 0.875, SCROLL_LINE);
  const chord = `${event.ctrlKey ? "Ctrl+" : ""}${event.metaKey ? "Meta+" : ""}${event.shiftKey ? "Shift+" : ""}${event.key}`;
  switch (chord) {
    case "PageDown":
      return step;
    case "PageUp":
      return -step;
    case "End":
    case "Ctrl+End":
      return "end";
    case "Home":
    case "Ctrl+Home":
      return "start";
  }
  if (target !== "body") {
    return null;
  }
  switch (chord) {
    case " ":
      return step;
    case "Shift+ ":
      return -step;
    case "ArrowDown":
      return SCROLL_LINE;
    case "ArrowUp":
      return -SCROLL_LINE;
    case "Meta+ArrowDown":
      return "end";
    case "Meta+ArrowUp":
      return "start";
  }
  return null;
}
