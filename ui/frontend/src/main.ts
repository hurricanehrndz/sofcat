import { api } from "./api.ts";
import {
  ACTIVITY_LIMIT,
  type StorageLike,
  loadActivity,
  loadBranding,
  loadList,
  saveActivity,
  saveBranding,
  saveList,
} from "./cache.ts";
import { scopedStorage } from "./demo.ts";
import {
  ALL_APPLIES_TO,
  ALL_CATEGORIES,
  REQUESTED_STATE,
  actionLabel,
  activityLine,
  addActivity,
  bannerMessage,
  connectionLabel,
  brandingView,
  withoutLogo,
  canCancel,
  cancelTitle,
  cardProgress,
  cardStatus,
  appliesToChoices,
  categories,
  deriveAction,
  enteringItems,
  fromCache,
  glyphTone,
  httpUrl,
  insertRecord,
  actionText,
  actionProgress,
  announcement,
  busyAction,
  endedOperations,
  isProgressTick,
  isTerminalState,
  itemPlacements,
  localErrorState,
  localRecord,
  monogram,
  itemCancel,
  morphFrom,
  myItems,
  needsAttention,
  openControl,
  opensDetails,
  placementAction,
  placementStatus,
  progressText,
  queueView,
  refusalText,
  releaseOperation,
  requestErrorText,
  resolveTab,
  restartBadge,
  shouldAcceptRecord,
  showRetry,
  statusLabel,
  statusRecord,
  stripLine,
  stripView,
  type ListTab,
  tabItems,
  tabs,
  trackOperation,
  visibleItems,
  withFailure,
  withLive,
  appliesToLine,
} from "./state.ts";
import type {
  ActiveOperations,
  ActivityRecord,
  BrandingView,
  ItemAction,
  ListView,
  OperationView,
  OptionalInstallItem,
  OptionalInstallPlacement,
} from "./types.ts";

// When an origin has storage blocked, reading the `localStorage` property
// itself throws SecurityError, so the try/catch has to wrap the property
// access — cache.ts only guards the getItem/setItem calls.
const browserStorage: StorageLike = (() => {
  try {
    return localStorage;
  } catch {
    return { getItem: () => null, setItem: () => {} };
  }
})();
// The mock (dev and demo modes) keeps a cache per fixture store. Vite folds
// these checks to false in a production build, which drops ./demo.ts.
const MOCK_MODE = import.meta.env.DEV || import.meta.env.MODE === "demo";
const storage: StorageLike = MOCK_MODE ? scopedStorage(browserStorage) : browserStorage;

function need<E extends Element>(selector: string): E {
  const element = document.querySelector<E>(selector);
  if (!element) {
    throw new Error(`SofCat UI shell is missing ${selector}`);
  }
  return element;
}

const connection = need<HTMLParagraphElement>("#connection");
const connectionText = need<HTMLSpanElement>("#connection-text");
const retryButton = need<HTMLButtonElement>("#retry");
const navMine = need<HTMLButtonElement>("#nav-mine");
const navActivity = need<HTMLButtonElement>("#nav-activity");
const navBusy = need<SVGElement>("#nav-busy");
const viewHome = need<HTMLElement>("#view-home");
const homeHeading = need<HTMLHeadingElement>("#home-heading");
const viewActivity = need<HTMLElement>("#view-activity");
const searchInput = need<HTMLInputElement>("#search");
const categorySelect = need<HTMLSelectElement>("#category");
const filtersForm = need<HTMLFormElement>("#filters");
const primaryNav = need<HTMLElement>("#primary-nav");
const navMarker = need<HTMLSpanElement>("#nav-marker");
const categoryNav = need<HTMLElement>("#category-nav");
const categoryList = need<HTMLDivElement>("#category-list");
const appliesToSelect = need<HTMLSelectElement>("#applies-to");
const appliesToField = need<HTMLElement>("#applies-to-field");
const appliesToLabel = need<HTMLLabelElement>("#applies-to-field label");
const appliesToNav = need<HTMLElement>("#applies-to-nav");
const appliesToHeading = need<HTMLElement>("#applies-to-nav-heading");
const appliesToList = need<HTMLDivElement>("#applies-to-list");
const listHead = need<HTMLDivElement>(".list-head");
const resultStatus = need<HTMLParagraphElement>("#result-status");
const grid = need<HTMLDivElement>("#item-grid");
const activityList = need<HTMLOListElement>("#activity-list");
const activityEmpty = need<HTMLParagraphElement>("#activity-empty");
const viewDetail = need<HTMLElement>("#view-detail");
const detailBack = need<HTMLButtonElement>("#detail-back");
const detailAction = need<HTMLButtonElement>("#detail-action");
const detailPlacements = need<HTMLUListElement>("#detail-placements");
// An item's details: one subtree, mounted on the detail page or in the dialog
// as branding item_view says (placeItemView).
const detailContent = need<HTMLDivElement>("#detail-content");
const dialog = need<HTMLDialogElement>("#item-dialog");
const dialogBody = need<HTMLDivElement>("#item-dialog .dialog-body");
const detailClose = need<HTMLButtonElement>("#detail-close");
const detailCancel = need<HTMLButtonElement>("#detail-cancel");
const strip = need<HTMLElement>("#operation-strip");
const stripGlyph = need<HTMLSpanElement>("#strip-glyph");
const stripBar = need<HTMLProgressElement>("#strip-progress");
const stripCancel = need<HTMLButtonElement>("#strip-cancel");
const stripBody = need<HTMLDivElement>(".strip-body");
const queueToggle = need<HTMLButtonElement>("#queue-toggle");
const queueText = need<HTMLSpanElement>("#queue-text");
const queuePanel = need<HTMLDivElement>("#queue-panel");
const queueList = need<HTMLUListElement>("#queue-list");
const announcer = need<HTMLParagraphElement>("#announcer");
const spinnerTemplate = need<HTMLTemplateElement>("#spinner");

/**
 * View is a page the nav can show; "list" is one of the list tabs. The detail
 * page is drilled into from a list.
 */
type View = "list" | "mine" | "activity";

let view: ListView = { items: [], source: "loading", savedAtUtc: "" };
let lastError = "";
// The list the detail page was opened from, and which Back returns to.
let currentView: View = "list";
// The list tabs the nav shows, and the one last chosen (null for the first):
// "list" shows it, or the first tab once it is gone.
let listTabs: ListTab[] = [];
let lastListTab: string | null = null;
let navTabsDrawn = "";
// The item shown on the detail page or in the dialog, or null when neither is up.
let detailItemName: string | null = null;
let activity: ActivityRecord[] = loadActivity(storage);
let active: ActiveOperations = new Map();
// Operations started in this session, oldest first; Activity keeps the history.
const operations = new Map<string, OperationView>();
let localOperations = 0;
// The action asked for on each item whose request the service has not
// answered yet, so its buttons are busy from the click on.
const requesting = new Map<string, ItemAction>();
// Under branding progress: button, the line the announcer last said for each
// running operation, so each phase is said once however ticks interleave.
const announced = new Map<string, string>();
// The item data each card was built from: renderGrid keeps a card whose item
// is unchanged and repaints it in place, so its button's bar eases.
const cardSources = new WeakMap<Element, string>();

function busyOf(itemName: string): ItemAction | undefined {
  return busyAction(itemName, requesting, active, operations);
}

/**
 * markBusy marks an action button busy with aria-disabled rather than
 * disabled, so it keeps focus and a screen reader still reads its label;
 * runAction ignores a click on a busy item.
 */
function markBusy(button: HTMLButtonElement, busy: ItemAction | undefined): void {
  button.setAttribute("aria-disabled", String(busy !== undefined));
}

/**
 * paintAction writes an item's action into its button, in place; the caller
 * sets its classes. Under branding progress: strip it is the label, or while
 * the item is busy its installing or removing label. Under button the busy
 * button carries the progress (actionProgress): a spinner while it waits,
 * then a bar the stylesheet draws from data-progress and --progress. The
 * accessible name stays the busy label, never a percentage, and the
 * announcer says each phase. A placement row's button names its row.
 */
function paintAction(
  button: HTMLButtonElement,
  item: OptionalInstallItem,
  action: ItemAction,
  row?: OptionalInstallPlacement,
): void {
  const busy = busyOf(item.itemName);
  const label = actionText(item, action, busy, row?.name);
  markBusy(button, busy);
  button.setAttribute("aria-label", row ? `${label}: ${row.label}` : `${label} ${item.displayName}`);
  if (branding.progress === "strip") {
    button.classList.remove("action");
    button.removeAttribute("aria-busy");
    delete button.dataset.progress;
    button.style.removeProperty("--progress");
    button.textContent = label;
    return;
  }
  // A request not answered yet has no operation: the item's last one is over.
  const operation = requesting.has(item.itemName) ? undefined : latestOperation(item.itemName);
  const progress = actionProgress(item, action, busy, operation, row?.name);
  let text = button.querySelector<HTMLSpanElement>(".action-label");
  let percent = button.querySelector<HTMLSpanElement>(".action-percent");
  if (!text || !percent) {
    text = document.createElement("span");
    text.className = "action-label";
    // The percentage has its own span, so a long phase is cut short before it is.
    percent = document.createElement("span");
    percent.className = "action-percent";
    const spinner = spinnerTemplate.content.firstElementChild?.cloneNode(true);
    button.replaceChildren(...(spinner ? [spinner] : []), text, percent);
  }
  button.classList.add("action");
  const measured = progress?.phase === "working" && progress.percent !== undefined;
  // The percentage says it is still going, so the phase drops its ellipsis
  // ("Downloading 45%") and fits a narrow button whole.
  const shown = measured ? (progress?.label ?? "").replace(/\s*(…|\.\.\.)$/, "") : (progress?.label ?? label);
  if (text.textContent !== shown) {
    text.textContent = shown;
  }
  percent.textContent = measured ? `${progress?.percent}%` : "";
  percent.hidden = percent.textContent === "";
  if (progress?.phase === "ended") {
    // The work is over, failed or canceled: the name is the action again.
    button.setAttribute("aria-label", row ? `${progress.label}: ${row.label}` : `${progress.label} ${item.displayName}`);
  }
  if (progress) {
    button.dataset.progress =
      progress.phase === "working" ? (progress.percent === undefined ? "unmeasured" : "measured") : progress.phase;
    button.style.setProperty("--progress", `${progress.percent ?? 0}%`);
  } else {
    delete button.dataset.progress;
    button.style.removeProperty("--progress");
  }
  button.setAttribute("aria-busy", String(progress !== null && progress.phase !== "ended"));
}

/**
 * setCancel sets up a Cancel button for an operation: a real disabled button
 * while the cancel request is out or once the installer has started, its
 * title saying why. The strip, the queue list, a card's and the item's own
 * Cancel share it.
 */
function setCancel(button: HTMLButtonElement, operation: OperationView): void {
  const latest = operation.records.at(-1)?.state ?? "";
  const pending = Boolean(operation.cancelPending);
  button.disabled = pending || !canCancel(latest);
  button.title = cancelTitle(latest, pending);
  const what = operation.action.method === "RemoveItem" ? "removal" : "install";
  button.textContent = actionLabel(operation.item, "cancel");
  button.setAttribute("aria-label", `${button.textContent} ${what} of ${operation.item.displayName}`);
  button.dataset.operationId = operation.operationId;
}

/**
 * paintCancel shows an item's own Cancel beside its action while it has one
 * (itemCancel; a card passes itemView "page", since it is never a dialog).
 * A Cancel that goes off or away while it has focus hands it to the action
 * beside it, or to fallback when the item shows no action, not to the
 * dialog or the page.
 */
function paintCancel(
  button: HTMLButtonElement,
  item: OptionalInstallItem,
  action: HTMLElement,
  fallback: HTMLElement,
  itemView: BrandingView["itemView"] = branding.itemView,
): void {
  const running = itemCancel(itemView, branding.progress, latestOperation(item.itemName));
  const focused = document.activeElement === button;
  button.hidden = !running;
  if (running) {
    setCancel(button, running);
  } else {
    // The label sizes the place style.css keeps for it while hidden.
    button.textContent = actionLabel(item, "cancel");
  }
  if (focused && (button.hidden || button.disabled)) {
    (action.hidden ? fallback : action).focus();
  }
}

/** cancelFrom cancels the operation a Cancel button was set up for, while it runs. */
function cancelFrom(button: HTMLButtonElement): void {
  const operation = operations.get(button.dataset.operationId ?? "");
  if (operation && operation.outcome === "active") {
    void cancelOperation(operation);
  }
}

const brandBanner = need<HTMLElement>("#banner");
const bannerLogo = need<HTMLSpanElement>("#banner-logo");
const brandLink = need<HTMLButtonElement>("#brand-link");
const appBarEnd = need<HTMLDivElement>("#app-bar-end");
const productMark = need<HTMLSpanElement>("#product-mark");
let branding: BrandingView = brandingView(null);

function logoImage(src: string, alt = ""): HTMLImageElement {
  const img = document.createElement("img");
  img.alt = alt;
  img.src = src;
  return img;
}

/**
 * applyBranding shows the organisation branding: admin configuration from the
 * service (policy registry or config.yaml), never catalog data. With nothing
 * configured it leaves the shell exactly as the default markup draws it.
 */
function applyBranding(next: BrandingView): void {
  branding = next;
  brandBanner.hidden = !next.showBanner;
  brandBanner.dataset.align = next.bannerAlign;
  brandBanner.dataset.height = next.bannerHeight;
  // A text banner shows the logo as a decorative tile beside the title; a
  // logo banner shows it in the title's place, named by the title.
  const textStyle = next.bannerStyle === "text";
  bannerLogo.hidden = !textStyle || !next.logoSrc;
  bannerLogo.replaceChildren(...(textStyle && next.logoSrc ? [logoImage(next.logoSrc)] : []));
  // A background image takes the accent's place in the banner, with the text
  // colour the admin picked for it. The source is a data: URL brandingView
  // built, so it cannot break out of url("").
  if (next.backgroundSrc) {
    brandBanner.style.setProperty("--banner-image", `url("${next.backgroundSrc}")`);
    brandBanner.dataset.image = next.backgroundText;
  } else {
    brandBanner.style.removeProperty("--banner-image");
    delete brandBanner.dataset.image;
  }
  const bannerTitle = need<HTMLParagraphElement>("#banner-title");
  if (textStyle) {
    bannerTitle.textContent = next.title;
  } else {
    // Named by the title, or the product's name when there is none. A logo
    // that sniffed as an image but does not decode falls back to text.
    const wordmark = logoImage(next.logoSrc, next.productName);
    wordmark.addEventListener("error", () => {
      if (branding === next) {
        applyBranding(withoutLogo(next));
      }
    });
    bannerTitle.replaceChildren(wordmark);
  }
  need<HTMLParagraphElement>("#banner-tagline").textContent = next.tagline;
  brandLink.hidden = !next.helpUrl;
  brandLink.textContent = next.helpLabel;
  // One button, moved: last in the banner, or at the app bar's end before
  // the connection status, so the tab order follows what the eye sees. Only
  // a real move re-inserts it: re-inserting drops its focus.
  const linkParent = next.linkPlacement === "app_bar" ? appBarEnd : brandBanner;
  if (brandLink.parentElement !== linkParent) {
    if (linkParent === appBarEnd) {
      appBarEnd.prepend(brandLink);
    } else {
      brandBanner.append(brandLink);
    }
  }
  // A corner keeps the DOM order: the button stays after the text for screen
  // readers, and it is the banner's only stop in the tab order.
  if (next.helpUrl && next.linkPlacement === "banner" && next.linkPosition !== "auto") {
    brandBanner.dataset.linkPosition = next.linkPosition;
  } else {
    delete brandBanner.dataset.linkPosition;
  }
  need<HTMLSpanElement>("#product-name").textContent = next.productName;
  // A logo banner's logo is a wordmark, too wide for the square mark.
  if (next.logoSrc && textStyle) {
    productMark.replaceChildren(logoImage(next.logoSrc));
  } else if (next.productMark) {
    productMark.textContent = next.productMark;
  } else {
    productMark.replaceChildren(logoImage(`${import.meta.env.BASE_URL}sofcat-icon.png`));
  }
  const root = document.documentElement;
  if (next.accent) {
    root.style.setProperty("--brand", next.accent);
    root.style.setProperty("--on-brand", next.onAccent);
    root.dataset.accent = "";
  } else {
    root.style.removeProperty("--brand");
    root.style.removeProperty("--on-brand");
    delete root.dataset.accent;
  }
  root.style.setProperty("--title-scale", String(next.titleScale));
  root.style.setProperty("--tagline-scale", String(next.taglineScale));
  root.dataset.contentWidth = next.contentWidth;
  root.dataset.filterPlacement = next.filterPlacement;
  root.dataset.navPlacement = next.navPlacement;
  root.dataset.categoryFilter = next.categoryFilter;
  appliesToLabel.textContent = next.appliesToFilterLabel;
  appliesToHeading.textContent = next.appliesToFilterLabel;
  root.dataset.appBarBrand = next.appBarBrand;
  root.dataset.progress = next.progress;
  // The tab order and the Software tab's heading are branding too, and so
  // are how a card opens its item and where running work shows.
  renderNav();
  renderCategories();
  renderAppliesTo();
  renderGrid();
  placeItemView();
  renderDetail();
  renderStrip();
}

/**
 * placeItemView mounts the item's details where branding item_view shows
 * them: on the detail page or in the dialog. A change of mode, the service's
 * branding replacing a cached copy, first closes an item open the old way.
 */
function placeItemView(): void {
  const home = branding.itemView === "dialog" ? dialogBody : viewDetail;
  if (detailContent.parentElement === home) {
    return;
  }
  if (dialog.open) {
    dialog.close();
    dialogClosed();
  } else {
    closeDetail();
  }
  home.append(detailContent);
}

/** refreshBranding converges on the service's branding; a failure keeps the cached one. */
async function refreshBranding(): Promise<void> {
  try {
    const payload = await api.getBranding();
    saveBranding(storage, payload);
    applyBranding(brandingView(payload));
  } catch {
    // An older service or a stopped one: the list refresh reports the connection.
  }
}

function renderBanner(): void {
  const label = connectionLabel(view);
  const detail = bannerMessage(view, lastError);
  connectionText.textContent = label;
  // The indicator stays a short right-aligned label; the sentence with the
  // reason is the tooltip and the accessible name.
  connection.title = detail === label ? "" : detail;
  connection.setAttribute("aria-label", detail);
  connection.dataset.source = view.source;
  retryButton.hidden = !showRetry(view);
}

/** currentTab is the list tab "list" shows: the last chosen, while it still exists. */
function currentTab(): string {
  return resolveTab(listTabs, lastListTab);
}

/** listedItems is what the current page lists before search and category filter it. */
function listedItems(): OptionalInstallItem[] {
  return currentView === "mine" ? myItems(view.items) : tabItems(view.items, currentTab());
}

/**
 * renderNav draws a nav button per list tab before My items, then marks the
 * current page. The buttons are rebuilt only when the tabs change, so a render
 * does not take focus off them.
 */
function renderNav(): void {
  listTabs = tabs(view.items, branding.sections);
  // Adopt the fallback when the chosen tab is gone, so the tab does not jump
  // back if its section returns on a later refresh.
  if (lastListTab !== null) {
    lastListTab = currentTab();
  }
  const drawn = JSON.stringify(listTabs);
  if (drawn !== navTabsDrawn) {
    navTabsDrawn = drawn;
    for (const old of document.querySelectorAll('nav [data-view="list"]')) {
      old.remove();
    }
    navMine.before(
      ...listTabs.map((tab) => {
        const button = document.createElement("button");
        button.type = "button";
        button.className = "nav-link";
        button.dataset.view = "list";
        button.dataset.tab = tab.key;
        button.textContent = tab.label;
        button.addEventListener("click", () => showView("list", tab.key));
        return button;
      }),
    );
  }
  markNav();
}

/** markNav sets aria-current on the nav button of the page shown; none on the detail page. */
function markNav(): void {
  for (const link of document.querySelectorAll<HTMLButtonElement>("nav .nav-link")) {
    const current =
      viewDetail.hidden &&
      link.dataset.view === currentView &&
      (currentView !== "list" || link.dataset.tab === currentTab());
    link.setAttribute("aria-current", current ? "page" : "false");
  }
  placeMarker();
}

/**
 * placeMarker moves the nav's marker under the current page's button; the
 * stylesheet eases it there, down a bar at the left or along one at the
 * top. It is placed again whenever the nav changes size or shape.
 */
function placeMarker(): void {
  const current = primaryNav.querySelector<HTMLElement>('.nav-link[aria-current="page"]');
  navMarker.hidden = !current;
  if (current) {
    navMarker.style.transform = `translate(${current.offsetLeft}px, ${current.offsetTop}px)`;
    navMarker.style.width = `${current.offsetWidth}px`;
    navMarker.style.height = `${current.offsetHeight}px`;
  }
}
new ResizeObserver(placeMarker).observe(primaryNav);
// A late stylesheet or font changes the buttons' sizes without resizing the nav.
window.addEventListener("load", placeMarker);
window.addEventListener("resize", placeMarker);

function renderCategories(): void {
  const selected = categorySelect.value;
  categorySelect.replaceChildren();
  const all = document.createElement("option");
  all.value = ALL_CATEGORIES;
  all.textContent = "All categories";
  categorySelect.append(all);
  for (const category of categories(listedItems())) {
    const option = document.createElement("option");
    option.value = category;
    option.textContent = category;
    categorySelect.append(option);
  }
  // Assigning an option value that no longer exists clears the select, which is
  // exactly the fallback we want.
  categorySelect.value = selected;
  if (!categorySelect.value) {
    categorySelect.value = ALL_CATEGORIES;
  }
  renderCategoryNav();
}

/**
 * renderAppliesTo rebuilds the select of names the list's items apply to.
 * Like the category, a choice the new list also offers is kept, in whatever
 * case that list spells it.
 */
function renderAppliesTo(): void {
  const selected = appliesToSelect.value;
  const names = appliesToChoices(listedItems());
  appliesToField.hidden = names.length === 0;
  appliesToSelect.replaceChildren(
    ...[ALL_APPLIES_TO, ...names].map((name) => {
      const option = document.createElement("option");
      option.value = name;
      option.textContent = name === ALL_APPLIES_TO ? "All" : name;
      return option;
    }),
  );
  appliesToSelect.value =
    names.find((name) => name.toLowerCase() === selected.toLowerCase()) ?? ALL_APPLIES_TO;
  renderCategoryNav();
}

/** The options each button group was last drawn from. */
const categoriesDrawn = { key: "" };
const appliesToDrawn = { key: "" };

/**
 * mirrorSelect mirrors a filter select as a list of buttons in a left bar
 * (branding category_filter: list): one per option, the chosen one marked.
 * The select stays the one place the choice is kept. The buttons are
 * rebuilt only when the options change, so a render does not take focus
 * off them; a select with no choices has no list, and neither does a page
 * that hides the list, Activity or an item's detail page.
 */
function mirrorSelect(
  select: HTMLSelectElement,
  nav: HTMLElement,
  list: HTMLElement,
  drawn: { key: string },
): void {
  const options = [...select.options].map((option) => [option.value, option.textContent ?? ""]);
  nav.hidden = options.length < 2 || viewHome.hidden;
  const key = JSON.stringify(options);
  if (key !== drawn.key) {
    drawn.key = key;
    list.replaceChildren(
      ...options.map(([value, label]) => {
        const button = document.createElement("button");
        button.type = "button";
        button.className = "nav-link category-link";
        button.dataset.value = value;
        button.textContent = label;
        button.addEventListener("click", () => {
          if (select.value !== value) {
            select.value = value;
            filterChanged();
          }
        });
        return button;
      }),
    );
  }
  for (const button of list.querySelectorAll<HTMLButtonElement>("button")) {
    button.setAttribute("aria-current", String(button.dataset.value === select.value));
  }
}

function renderCategoryNav(): void {
  mirrorSelect(categorySelect, categoryNav, categoryList, categoriesDrawn);
  mirrorSelect(appliesToSelect, appliesToNav, appliesToList, appliesToDrawn);
}

/**
 * filterChanged redraws the list for a new filter choice. The grid is emptied
 * first, so the cards of the new selection all ease in, one after another.
 */
function filterChanged(): void {
  grid.replaceChildren();
  renderCategoryNav();
  renderGrid();
}

/** setGlyph writes an item's monogram and its stable colour tone. */
function setGlyph(glyph: HTMLElement, item: OptionalInstallItem): void {
  glyph.textContent = monogram(item);
  glyph.dataset.tone = String(glyphTone(item));
}

/** statusLine is a status paragraph: a spinner shown while work runs, then the text. */
function statusLine(): HTMLParagraphElement {
  const status = document.createElement("p");
  status.className = "card-status";
  status.setAttribute("aria-live", "polite");
  const spinner = spinnerTemplate.content.firstElementChild?.cloneNode(true);
  const text = document.createElement("span");
  text.className = "status-text";
  if (spinner) {
    status.append(spinner);
  }
  status.append(text);
  return status;
}

function card(item: OptionalInstallItem): HTMLElement {
  const article = document.createElement("article");
  article.className = "card";

  const glyph = document.createElement("span");
  glyph.className = "glyph";
  glyph.setAttribute("aria-hidden", "true");
  setGlyph(glyph, item);

  const name = document.createElement("h3");
  const dialogView = branding.itemView === "dialog";
  if (dialogView) {
    // The card has no Details button in dialog mode: its name is the control
    // that opens the item's dialog for keyboard and screen-reader users.
    const open = document.createElement("button");
    open.type = "button";
    open.className = "card-open";
    open.setAttribute("aria-haspopup", "dialog");
    open.textContent = item.displayName;
    open.addEventListener("click", (event) => openDetail(item, event.detail === 0));
    name.append(open);
  } else {
    name.textContent = item.displayName;
  }

  const meta = document.createElement("p");
  meta.className = "card-meta";
  meta.textContent = [item.category, item.developer].filter(Boolean).join(" · ");
  meta.hidden = meta.textContent === "";

  const appliesTo = document.createElement("p");
  appliesTo.className = "card-meta";
  appliesTo.textContent = appliesToLine(item);
  appliesTo.hidden = appliesTo.textContent === "";

  // The status line shows only what the button cannot: a pending or required
  // item's status, or a failure or deferral reason (see cardStatus and
  // updateCardProgress). It is a live region, so a change is announced without
  // the whole grid being re-read.
  const status = statusLine();

  const actions = document.createElement("p");
  actions.className = "card-actions";

  // An item the organisation requires has no action, only Details.
  const derived = deriveAction(item);
  if (derived) {
    const action = document.createElement("button");
    action.type = "button";
    // Board B draws the undo action (Remove) outlined; the others are solid.
    action.className = `pill card-action ${derived.kind === "undo" ? "pill-outline" : "pill-primary"}`;
    // Only the item that owns an in-flight operation is busy; other items
    // stay actionable and route independently by operation ID (paintCard).
    action.addEventListener("click", () => void runAction(item, derived));
    // Cancel, beside the action, shows only under branding progress: button.
    const cancel = document.createElement("button");
    cancel.type = "button";
    cancel.className = "pill pill-outline card-cancel";
    cancel.hidden = true;
    cancel.addEventListener("click", () => cancelFrom(cancel));
    // style.css keeps Cancel's place after the action, so the action never
    // moves when Cancel comes or goes, and the DOM order is the one seen.
    actions.append(action, cancel);
  }

  if (!dialogView) {
    const details = document.createElement("button");
    details.type = "button";
    details.className = "pill pill-outline card-details";
    details.textContent = "Details";
    details.setAttribute("aria-label", `Details for ${item.displayName}`);
    details.addEventListener("click", () => openDetail(item));
    actions.append(details);
  }

  // The wrappers let style.css turn a card a sparse row stretches into a
  // single line: glyph, text, then the buttons. A required item in dialog
  // mode has no button at all.
  const text = document.createElement("div");
  text.className = "card-text";
  text.append(name, meta, appliesTo, status);
  const body = document.createElement("div");
  body.className = "card-body";
  body.append(glyph, text, ...(actions.childElementCount > 0 ? [actions] : []));
  article.append(body);
  article.dataset.item = item.itemName;
  // A click anywhere else on the card opens the item too. The card is not a
  // control itself, so it nests none: keyboard and screen-reader users reach
  // the same page or dialog through its Details button or its name.
  article.addEventListener("click", (event) => {
    const selecting = !(document.getSelection()?.isCollapsed ?? true);
    if (opensDetails(event.target instanceof Element ? event.target : null, selecting)) {
      openDetail(item, false);
    }
  });
  paintCard(article, item);
  return article;
}

/** paintCard writes what changes while an item works into its card, in place. */
function paintCard(article: HTMLElement, item: OptionalInstallItem): void {
  const action = article.querySelector<HTMLButtonElement>(".card-action");
  const derived = deriveAction(item);
  if (action && derived) {
    paintAction(action, item, derived);
  }
  const cancel = article.querySelector<HTMLButtonElement>(".card-cancel");
  // The control that opens the item: Details, or the name in dialog mode.
  const opener = article.querySelector<HTMLElement>(".card-details, .card-open");
  if (cancel && action && opener) {
    // A card is never the dialog: only progress button gives it a Cancel.
    paintCancel(cancel, item, action, opener, "page");
  }
  updateCardProgress(article, item, cardStatus(item), true);
}

/** latestOperation is the most recently started operation on an item, if any. */
function latestOperation(itemName: string): OperationView | undefined {
  let found: OperationView | undefined;
  for (const operation of operations.values()) {
    if (operation.item.itemName === itemName) {
      found = operation;
    }
  }
  return found;
}

/**
 * updateCardProgress writes the item's operation state into its status line:
 * idleText when nothing is going on (an empty one hides the line), otherwise a
 * spinner and one line while the operation runs and the outcome afterwards.
 * warningsOnly (a Home card) shows idleText in place of running work and keeps
 * only outcomes that need attention. The bar is only in the bottom strip and
 * the full timeline only in Activity. Under branding progress: button the
 * bar is the item's button, so every status line keeps only outcomes that
 * need attention and the reason a cancel was refused while the work goes on.
 */
function updateCardProgress(
  container: HTMLElement,
  item: OptionalInstallItem,
  idleText: string,
  warningsOnly = false,
): void {
  const status = container.querySelector<HTMLParagraphElement>(".card-status");
  const text = status?.querySelector<HTMLSpanElement>(".status-text");
  const spinner = status?.querySelector<SVGElement>(".spinner");
  if (!status || !text || !spinner) {
    throw new Error("SofCat UI card is missing its status line");
  }
  const operation = latestOperation(item.itemName);
  const progress = operation
    ? cardProgress(item, operation.records, operation.outcome, operation.action.method, operation.notice)
    : null;
  const inButton = branding.progress === "button";
  const refused = inButton && operation?.outcome === "active" && Boolean(operation.notice);
  if (!progress || ((warningsOnly || inButton) && !needsAttention(progress) && !refused)) {
    text.textContent = idleText;
    status.hidden = idleText === "";
    delete status.dataset.outcome;
    spinner.toggleAttribute("hidden", true);
    return;
  }
  text.textContent = progressText(progress);
  status.hidden = false;
  // "plain" opts out of the warning box the stylesheet gives finished outcomes.
  status.dataset.outcome = progress.plain ? "plain" : progress.outcome;
  spinner.toggleAttribute("hidden", inButton || progress.outcome !== "active");
}

/** cardOf is an item's card in the grid, if the list shows it. */
function cardOf(itemName: string): HTMLElement | null {
  return grid.querySelector<HTMLElement>(`[data-item="${CSS.escape(itemName)}"]`);
}

/** renderCardFor refreshes one item's card in place, keeping the rest of the grid untouched. */
function renderCardFor(itemName: string): void {
  const article = cardOf(itemName);
  const item = view.items.find((candidate) => candidate.itemName === itemName);
  if (article && item) {
    paintCard(article, item);
  }
  if (item && detailItemName === itemName) {
    // Under button the item's buttons carry the progress, so they are
    // repainted (in place) as well.
    if (branding.progress === "button") {
      renderDetail();
    } else {
      updateCardProgress(detailContent, item, "");
      paintCancel(detailCancel, item, detailAction, detailFallback());
    }
  }
}

function renderGrid(): void {
  const mine = currentView === "mine";
  const tab = currentTab();
  const listed = listedItems();
  const shown = visibleItems(listed, searchInput.value, categorySelect.value, appliesToSelect.value);
  // The Software tab keeps the admin's heading; a section tab is headed by its name.
  homeHeading.textContent = mine
    ? "My items"
    : tab
      ? (listTabs.find((candidate) => candidate.key === tab)?.label ?? "")
      : branding.listHeading;
  // The list page is entering while hidden or while its own rise-in runs
  // (getAnimations flushes styles, so it sees one started this task).
  const entering = enteringItems(
    [...grid.children].map((child) => (child as HTMLElement).dataset.item),
    shown,
    viewHome.hidden !== false || viewHome.getAnimations().length > 0,
  );
  // A card whose item is unchanged stays and is repainted in place, so its
  // button's bar eases on; any other is rebuilt. A rebuild replaces the
  // card's buttons, so a focused card button (its action goes busy at the
  // click) hands focus to its replacement.
  const focused = grid.contains(document.activeElement) ? document.activeElement : null;
  const focusedItem = focused?.closest<HTMLElement>(".card")?.dataset.item;
  const focusedClass =
    ["card-action", "card-cancel"].find((name) => focused?.classList.contains(name)) ?? openControl(branding.itemView);
  const listedCards = new Map([...grid.children].map((child) => [(child as HTMLElement).dataset.item, child]));
  const articles = shown.map((item) => {
    // The item view decides the card's controls, so it is part of its source.
    const source = JSON.stringify([branding.itemView, item]);
    const kept = listedCards.get(item.itemName) as HTMLElement | undefined;
    const article = kept && cardSources.get(kept) === source ? kept : card(item);
    if (article === kept) {
      paintCard(article, item);
    } else {
      cardSources.set(article, source);
    }
    article.toggleAttribute("data-entering", entering.has(item.itemName));
    return article;
  });
  // Stale cards go first, so placing the rest moves none that stays.
  const keep = new Set<Element>(articles);
  for (const child of [...grid.children]) {
    if (!keep.has(child)) {
      child.remove();
    }
  }
  // New cards ease in one after another; the first dozen set the pace.
  articles
    .filter((article) => article.hasAttribute("data-entering"))
    .forEach((article, index) => article.style.setProperty("--enter-order", String(Math.min(index, 12))));
  articles.forEach((article, index) => {
    if (grid.children[index] !== article) {
      grid.insertBefore(article, grid.children[index] ?? null);
    }
  });
  if (focusedItem !== undefined && !grid.contains(document.activeElement)) {
    const owner = grid.querySelector<HTMLElement>(`[data-item="${CSS.escape(focusedItem)}"]`);
    const target = owner?.querySelector<HTMLButtonElement>(`.${focusedClass}`);
    // A Cancel gone with its operation hands focus to the action beside it.
    (target && !target.hidden && !target.disabled ? target : owner?.querySelector<HTMLElement>(".card-action"))?.focus();
  }
  // The dialog stays over its item's card; once a refresh or a branding
  // change takes the card off the list, there is nothing left to show it over.
  if (dialog.open && detailItemName !== null && !cardOf(detailItemName)) {
    closeDetail();
  }
  if (listed.length === 0) {
    resultStatus.textContent =
      view.source === "loading"
        ? ""
        : mine
          ? "Nothing here is installed or managed yet."
          : tab
            ? "Nothing is available here yet."
            : "No software is available yet.";
    return;
  }
  resultStatus.textContent =
    shown.length === listed.length
      ? `Showing all ${listed.length} items.`
      : `Showing ${shown.length} of ${listed.length} items.`;
}

/**
 * renderStrip shows the one operation being worked on, with the only progress
 * bar in the UI: determinate when the latest record carries a percentage,
 * indeterminate (never a fake 0%) otherwise.
 */
function renderStrip(): void {
  navBusy.toggleAttribute("hidden", active.size === 0);
  const inButton = branding.progress === "button";
  for (const part of [stripGlyph, stripBody, stripCancel]) {
    part.hidden = inButton;
  }
  if (inButton) {
    renderQueue();
    return;
  }
  queueToggle.hidden = true;
  closeQueue(false);
  const shown = stripView(operations.values());
  strip.hidden = !shown;
  if (!shown) {
    return;
  }
  const { item } = shown.operation;
  setGlyph(stripGlyph, item);
  need<HTMLSpanElement>("#strip-name").textContent = item.displayName;
  need<HTMLSpanElement>("#strip-text").textContent = stripLine(shown);
  if (typeof shown.progress.percent === "number") {
    stripBar.value = shown.progress.percent;
  } else {
    stripBar.removeAttribute("value");
  }
  stripBar.setAttribute("aria-label", `${shown.progress.label} ${item.displayName}`);
  // Cancel stays visible, but is a real disabled button once the installer has
  // started; its title says why.
  setCancel(stripCancel, shown.operation);
}

/**
 * renderQueue is the strip under branding progress: button. Each item's
 * button shows its own progress, so the strip shows only while two or more
 * operations run, and then only their count: a button that opens the list of
 * them above it, each row with its own Cancel. Rows are kept per operation
 * and updated in place, so a screen reader in the list is not thrown out of
 * it by every tick.
 */
function renderQueue(): void {
  const queue = queueView(operations.values());
  if (!queue) {
    // Focus inside a strip about to hide would fall to the page's body.
    const hadFocus = strip.contains(document.activeElement);
    closeQueue(false);
    strip.hidden = true;
    queueToggle.hidden = true;
    if (hadFocus) {
      focusAfterQueue();
    }
    return;
  }
  strip.hidden = false;
  queueToggle.hidden = false;
  queueText.textContent = queue.line;
  const focusedRow = document.activeElement?.closest<HTMLElement>("#queue-list li")?.dataset.operationId;
  const rows = new Map([...queueList.children].map((row) => [(row as HTMLElement).dataset.operationId, row]));
  const wanted = queue.rows.map(({ operation, text }) => {
    const row = (rows.get(operation.operationId) as HTMLLIElement | undefined) ?? queueRow(operation);
    const [glyph, name, state, cancel] = row.children as unknown as [HTMLElement, HTMLElement, HTMLElement, HTMLButtonElement];
    setGlyph(glyph, operation.item);
    name.textContent = operation.item.displayName;
    if (state.textContent !== text) {
      state.textContent = text;
    }
    setCancel(cancel, operation);
    return row;
  });
  const keep = new Set<Element>(wanted);
  for (const row of [...queueList.children]) {
    if (!keep.has(row)) {
      row.remove();
    }
  }
  wanted.forEach((row, index) => {
    if (queueList.children[index] !== row) {
      queueList.insertBefore(row, queueList.children[index] ?? null);
    }
  });
  // A Cancel that went off or away with its row hands focus to the strip.
  if (focusedRow !== undefined && !queuePanel.hidden) {
    const cancel = queueList.querySelector<HTMLButtonElement>(`[data-operation-id="${CSS.escape(focusedRow)}"] button`);
    if (!cancel || cancel.disabled) {
      queueToggle.focus();
    } else if (!queueList.contains(document.activeElement)) {
      cancel.focus();
    }
  }
}

/** queueRow builds an empty row of the queue list; renderQueue fills it. */
function queueRow(operation: OperationView): HTMLLIElement {
  const row = document.createElement("li");
  row.dataset.operationId = operation.operationId;
  const glyph = document.createElement("span");
  glyph.className = "glyph glyph-small";
  glyph.setAttribute("aria-hidden", "true");
  const name = document.createElement("span");
  name.className = "queue-name";
  const state = document.createElement("span");
  state.className = "queue-state";
  const cancel = document.createElement("button");
  cancel.type = "button";
  cancel.className = "pill pill-outline";
  cancel.addEventListener("click", () => cancelFrom(cancel));
  row.append(glyph, name, state, cancel);
  return row;
}

/** openQueue shows the queue list above the strip; focus stays on the strip. */
function openQueue(): void {
  queuePanel.hidden = false;
  queueToggle.setAttribute("aria-expanded", "true");
}

/** closeQueue hides the queue list, handing focus back to the strip when asked. */
function closeQueue(refocus: boolean): void {
  if (queuePanel.hidden) {
    return;
  }
  queuePanel.hidden = true;
  queueToggle.setAttribute("aria-expanded", "false");
  if (refocus && !queueToggle.hidden) {
    queueToggle.focus();
  }
}

/**
 * focusAfterQueue gives focus a place when the strip hides with it inside:
 * the action of the item still running when its card or page shows, else
 * the top of the page shown.
 */
function focusAfterQueue(): void {
  const running = [...operations.values()].find((operation) => operation.outcome === "active");
  if (!viewDetail.hidden) {
    (detailAction.hidden ? detailBack : detailAction).focus();
    return;
  }
  // A card on a hidden page (Activity) cannot take focus.
  const action =
    running && !viewHome.hidden
      ? grid.querySelector<HTMLElement>(`[data-item="${CSS.escape(running.item.itemName)}"] .card-action`)
      : null;
  (action ?? (viewHome.hidden ? navActivity : searchInput)).focus();
}

function renderActivity(): void {
  activityEmpty.hidden = activity.length > 0;
  activityList.replaceChildren(
    ...activity.map((record) => {
      const entry = document.createElement("li");
      entry.textContent = activityLine(record);
      return entry;
    }),
  );
}

function render(): void {
  renderBanner();
  renderNav();
  renderCategories();
  renderAppliesTo();
  renderGrid();
  renderDetail();
  renderStrip();
}

/**
 * pushRecord files a record in its operation and, unless it is only a
 * percentage update, in Activity; false for a duplicate.
 */
function pushRecord(operation: OperationView, record: ActivityRecord): boolean {
  if (!insertRecord(operation.records, record)) {
    return false;
  }
  if (isProgressTick(operation.records, record)) {
    return true;
  }
  activity = addActivity(activity, record, ACTIVITY_LIMIT);
  saveActivity(storage, activity);
  if (!viewActivity.hidden) {
    renderActivity();
  }
  return true;
}

function reason(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

async function runAction(item: OptionalInstallItem, action: ItemAction): Promise<void> {
  // A second click, or a click on any other button of a busy item, asks for
  // nothing: the item is busy from the first click until its new state shows.
  if (busyOf(item.itemName)) {
    return;
  }
  requesting.set(item.itemName, action);
  render();
  const started = new Date().toISOString();
  let operationId = "";
  try {
    const accepted =
      action.method === "InstallItem"
        ? await api.installItem(item.itemName, action.placement)
        : await api.removeItem(item.itemName, action.placement);
    operationId = (accepted.operationId ?? "").trim();
    if (!accepted.accepted || !operationId) {
      throw new Error("the service did not accept the request");
    }
  } catch (error) {
    requesting.delete(item.itemName);
    localOperations += 1;
    const failed: OperationView = {
      operationId: `local-${localOperations}`,
      item,
      action,
      records: [],
      outcome: "error",
    };
    operations.set(failed.operationId, failed);
    pushRecord(
      failed,
      localRecord(failed.operationId, item, localErrorState(reason(error)), requestErrorText(reason(error)), started),
    );
    render();
    return;
  }

  requesting.delete(item.itemName);
  const operation: OperationView = { operationId, item, action, records: [], outcome: "active" };
  operations.set(operationId, operation);
  pushRecord(
    operation,
    localRecord(operationId, item, REQUESTED_STATE, `${action.label} accepted by the service.`, started),
  );
  active = trackOperation(active, operationId, item.itemName);
  render();

  // Watching must not block the UI: status arrives on the shared event channel
  // and only failures come back through this promise.
  void api.watchOperation(operationId).catch((error) => failOperation(operationId, reason(error)));
}

/**
 * cancelOperation asks the service to cancel. An accepted cancel ends the
 * operation through its Canceled record on the status channel; a refusal
 * leaves the item's state alone and shows the service's reason on its card.
 */
async function cancelOperation(operation: OperationView): Promise<void> {
  operation.cancelPending = true;
  renderCardFor(operation.item.itemName);
  renderStrip();
  try {
    await api.cancelOperation(operation.operationId);
  } catch (error) {
    operation.cancelPending = false;
    operation.notice = refusalText(reason(error));
    renderCardFor(operation.item.itemName);
    renderStrip();
  }
}

/** failOperation marks only the local display record; it never touches item state. */
function failOperation(operationId: string, message: string): void {
  const operation = operations.get(operationId);
  if (!operation || operation.outcome !== "active") {
    return;
  }
  operation.outcome = "error";
  announced.delete(operationId);
  active = releaseOperation(active, operationId);
  pushRecord(
    operation,
    localRecord(operationId, operation.item, localErrorState(message), message, new Date().toISOString()),
  );
  render();
}

// Wails emits each event on its own goroutine and never serialises them, so
// records can arrive out of order. pushRecord files each by the seq the service
// gave it: a record that arrives after the terminal one lands before it, and
// the finished operation keeps its terminal record as its latest.
api.onOperationStatus((status) => {
  const operation = operations.get(status.operationId);
  if (!operation || !shouldAcceptRecord(operation.outcome)) {
    return;
  }
  const record = statusRecord(status);
  if (!pushRecord(operation, record) || operation.outcome === "terminal") {
    return;
  }
  // ItemCompleted and ItemFailed are per-item records; only the four terminal
  // states end the operation, and only an authoritative list changes the cards.
  if (isTerminalState(record.state)) {
    // The item stays busy until the refresh below brings its new state, so
    // its button never shows the old action again in between.
    operation.outcome = "terminal";
    announced.delete(operation.operationId);
    render();
    void refresh();
    return;
  }
  // Status lines and the strip are live regions, so updating them in place
  // announces the new state without re-reading the whole grid.
  renderCardFor(operation.item.itemName);
  renderStrip();
  // Under button the buttons are not live regions: the announcer says each
  // new phase once per operation, without its percentage.
  const line = branding.progress === "button" ? announcement(operation) : "";
  if (line && announced.get(operation.operationId) !== line) {
    announced.set(operation.operationId, line);
    announcer.textContent = line;
  }
});

function setText(selector: string, value: string, fallback = "Not provided"): void {
  const element = need<HTMLElement>(selector);
  const text = value.trim() || fallback;
  // Unchanged text is left alone, so a repaint on every progress tick
  // (branding progress: button) keeps a selection in the details.
  if (element.textContent !== text) {
    element.textContent = text;
  }
}

/**
 * renderDetail fills the item's details, on the drill-down page or in the
 * dialog, for the item being viewed. It reads
 * the item from the current list, so a refresh after a terminal record updates
 * the status and the action button in place.
 */
function renderDetail(): void {
  const item = detailItemName ? view.items.find((candidate) => candidate.itemName === detailItemName) : undefined;
  if (!item) {
    return;
  }
  setGlyph(need<HTMLElement>("#detail-glyph"), item);
  setText("#detail-name", item.displayName, item.itemName);
  setText("#detail-developer", item.developer ?? "", "");
  const placements = itemPlacements(item);
  const appliesTo = need<HTMLParagraphElement>("#detail-applies-to");
  appliesTo.textContent = placements.length ? "" : appliesToLine(item);
  appliesTo.hidden = appliesTo.textContent === "";
  renderPlacements(item, placements);
  setText("#detail-description", item.description ?? "", "No description is published for this item.");
  setText("#detail-developer-value", item.developer ?? "");
  setText("#detail-version", item.version);
  setText("#detail-category", item.category ?? "");
  setText("#detail-status", statusLabel(item));
  setText("#detail-restart", restartBadge(item), "None");

  const derived = deriveAction(item);
  detailAction.hidden = derived === null;
  if (derived) {
    detailAction.className = `pill pill-large ${derived.kind === "undo" ? "pill-outline" : "pill-primary"}`;
    paintAction(detailAction, item, derived);
    detailAction.onclick = () => void runAction(item, derived);
  }

  // The Status row already says the catalog state, so the line only appears
  // for work in progress or an outcome.
  updateCardProgress(detailContent, item, "");
  paintCancel(detailCancel, item, detailAction, detailFallback());
}

/** detailFallback takes focus from the item's Cancel when it has no action: Close or Back. */
function detailFallback(): HTMLElement {
  return dialog.open ? detailClose : detailBack;
}

/**
 * renderPlacements draws one row per placement on the detail page: its label,
 * its status and the item's action for that placement alone. Rows follow the
 * item's own button while it has an operation running. The rows are built
 * once per item and set of placements, then updated in place, so focus and a
 * button's bar stay; a rebuild hands focus back to the row that had it.
 */
function renderPlacements(item: OptionalInstallItem, placements: OptionalInstallPlacement[]): void {
  const focused = document.activeElement?.closest<HTMLElement>("#detail-placements li")?.dataset.placement;
  detailPlacements.hidden = placements.length === 0;
  detailPlacements.setAttribute("aria-label", actionLabel(item, "applies_to"));
  const rows = JSON.stringify([item.itemName, ...placements.map((placement) => placement.name)]);
  if (detailPlacements.dataset.rows !== rows) {
    detailPlacements.dataset.rows = rows;
    detailPlacements.replaceChildren(
      ...placements.map((placement) => {
        const row = document.createElement("li");
        row.dataset.placement = placement.name;
        const label = document.createElement("span");
        label.className = "placement-label";
        const status = document.createElement("span");
        status.className = "card-meta";
        const action = document.createElement("button");
        action.type = "button";
        row.append(label, status, action);
        return row;
      }),
    );
  }
  placements.forEach((placement, index) => {
    const [label, status, action] = detailPlacements.children[index].children as unknown as [
      HTMLElement,
      HTMLElement,
      HTMLButtonElement,
    ];
    label.textContent = placement.label;
    status.textContent = placementStatus(item, placement);
    // An item the organisation requires has no action on any row.
    const derived = placementAction(item, placement);
    action.hidden = derived === null;
    if (derived) {
      action.className = `pill ${derived.kind === "undo" ? "pill-outline" : "pill-primary"}`;
      paintAction(action, item, derived, placement);
      action.onclick = () => void runAction(item, derived);
    }
  });
  if (focused !== undefined && !detailPlacements.contains(document.activeElement)) {
    detailPlacements.querySelector<HTMLButtonElement>(`[data-placement="${CSS.escape(focused)}"] button`)?.focus();
  }
}

/**
 * setHomeHidden shows or hides the list page. Hiding it ends every card's
 * entrance: a shown element restarts its animations, and the page's own
 * rise-in is the only motion it should bring back. The bar's category list
 * goes with the page.
 */
function setHomeHidden(hidden: boolean): void {
  if (hidden) {
    for (const entering of grid.querySelectorAll("[data-entering]")) {
      entering.removeAttribute("data-entering");
    }
  }
  viewHome.hidden = hidden;
  renderCategoryNav();
}

/**
 * openDetail shows an item: its detail page, or its dialog for branding
 * item_view dialog. focusVisible false (a pointer opened it) opens the dialog
 * without a focus ring on its close button; the page keeps its own.
 */
function openDetail(item: OptionalInstallItem, focusVisible = true): void {
  if (branding.itemView === "dialog") {
    openDialog(item, focusVisible);
    return;
  }
  detailItemName = item.itemName;
  renderDetail();
  setHomeHidden(true);
  viewActivity.hidden = true;
  viewDetail.hidden = false;
  markNav();
  detailBack.focus();
}

/**
 * focusCard hands focus to the control of the item's card that opens it (its
 * Details button, or its name in dialog mode). The card is found by item,
 * because a refresh while the item was open (the end of every operation
 * brings one) rebuilds the grid. When the card is gone from the list, dropped
 * by a refresh or hidden by the filters, focus goes to the search field at the
 * top of the list instead.
 */
function focusCard(itemName: string, focusVisible = true): void {
  const control = cardOf(itemName)?.querySelector<HTMLButtonElement>(`.${openControl(branding.itemView)}`);
  focusQuietly(control ?? searchInput, focusVisible);
}

/** closeDetail closes the open item: back to the list from its page, or its dialog. */
function closeDetail(): void {
  if (dialog.open) {
    closeDialog();
    return;
  }
  if (viewDetail.hidden || detailItemName === null) {
    return;
  }
  const itemName = detailItemName;
  showView(currentView);
  focusCard(itemName);
}

const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
// The dialog's grow-out-of-the-card motion while it plays, and a count of the
// times the dialog opened, so a close that finishes late leaves a later one be.
let opening: Animation | null = null;
let dialogOpenings = 0;

/**
 * morph plays the dialog growing out of the item's card, or shrinking back
 * into it when closing. Null, and no motion, when the card is not wholly on
 * screen (morphFrom), the user asked for reduced motion or the web view
 * cannot animate. It measures the dialog as laid out, so no other motion may
 * be playing on it.
 */
function morph(itemName: string, closing: boolean): Animation | null {
  const card = cardOf(itemName);
  if (!card || reducedMotion.matches || typeof dialog.animate !== "function") {
    return null;
  }
  // The strip covers the bottom of the window, so a card behind it is not
  // on screen either.
  const covered = strip.hidden ? 0 : strip.offsetHeight;
  const viewport = {
    width: document.documentElement.clientWidth,
    height: document.documentElement.clientHeight - covered,
  };
  const from = morphFrom(card.getBoundingClientRect(), dialog.getBoundingClientRect(), viewport);
  if (!from) {
    return null;
  }
  const small = { transform: from, opacity: 0.2 };
  const full = { transform: "none", opacity: 1 };
  return dialog.animate(closing ? [full, small] : [small, full], {
    duration: closing ? 180 : 280,
    easing: "cubic-bezier(0.2, 0.8, 0.2, 1)",
  });
}

function openDialog(item: OptionalInstallItem, focusVisible: boolean): void {
  if (dialog.open) {
    return;
  }
  detailItemName = item.itemName;
  dialogOpenings += 1;
  // The strip is inert under the modal dialog, so its queue list (open from
  // the keyboard, then Tab to a card) must not float above the backdrop.
  closeQueue(false);
  // A modal dialog takes everything outside it out of the accessibility
  // tree, the announcer too, so it goes in with the dialog while it is open.
  dialog.append(announcer);
  renderDetail();
  dialog.showModal();
  dialog.scrollTop = 0;
  // showModal has already focused the close button, its first control, with
  // the web view's own choice of ring. Opened by a pointer, it is focused
  // again without one, which needs it to lose focus first.
  if (!focusVisible && quietFocus) {
    detailClose.blur();
    focusQuietly(detailClose, false);
  }
  opening = morph(item.itemName, false);
  fadeBackdrop(false);
}

// Whether the web view takes focus({focusVisible}): one that does reads the
// member, even on an element that cannot take focus.
const quietFocus = ((): boolean => {
  let read = false;
  try {
    document.createElement("button").focus({
      get focusVisible() {
        read = true;
        return false;
      },
    });
  } catch {
    // An old web view: no options at all.
  }
  return read;
})();

/**
 * focusQuietly focuses element, without a focus ring when focusVisible is
 * false: after a pointer action the ring only says what the pointer already
 * showed. A web view without the focusVisible option draws its own choice.
 */
function focusQuietly(element: HTMLElement, focusVisible: boolean): void {
  element.focus(focusVisible || !quietFocus ? undefined : { focusVisible: false });
}

// The backdrop's fade, in or out, while it plays.
let backdropFade: Animation | null = null;

/**
 * fadeBackdrop fades the dimmed backdrop in as the dialog opens, or out as it
 * closes. A close while it is still fading in fades out from where it has got
 * to; both hold their end, so the backdrop never shows at full dim again
 * before the dialog closes. Reduced motion, or a web view that cannot animate
 * a pseudo-element, gives no fade.
 */
function fadeBackdrop(closing: boolean): void {
  if (reducedMotion.matches || typeof dialog.animate !== "function" || !("pseudoElement" in KeyframeEffect.prototype)) {
    return;
  }
  // Where the fade-in has got to: its eased progress is the opacity now. A new
  // fade out starts there at once, where reverse() would run on for a frame.
  const from = closing ? (backdropFade?.effect?.getComputedTiming().progress ?? 1) : 0;
  backdropFade?.cancel();
  try {
    backdropFade = dialog.animate(closing ? { opacity: [from, 0] } : { opacity: [0, 1] }, {
      duration: closing ? 180 * from : 200,
      easing: "ease-out",
      fill: "both",
      pseudoElement: "::backdrop",
    });
  } catch {
    // A web view that animates pseudo-elements but not ::backdrop: no fade,
    // and the dialog still opens and closes.
    backdropFade = null;
  }
}

/**
 * closeDialog shrinks the dialog back into its card, then closes it and
 * focuses the card (dialogClosed). Closed while it is still growing, it
 * plays that motion back from where it is: measuring it then would take the
 * scaled box for its size. focusVisible false (a pointer closed it) focuses
 * the card without a focus ring.
 */
function closeDialog(focusVisible = true): void {
  const itemName = detailItemName;
  if (!dialog.open || itemName === null || "closing" in dialog.dataset) {
    return;
  }
  const openingNow = dialogOpenings;
  dialog.dataset.closing = "";
  let leaving: Animation | null = null;
  if (opening?.playState === "running" && cardOf(itemName) && !reducedMotion.matches) {
    opening.reverse();
    leaving = opening;
  } else {
    opening?.cancel();
    leaving = morph(itemName, true);
  }
  opening = null;
  const finish = (): void => {
    if (dialogOpenings !== openingNow || detailItemName !== itemName) {
      return;
    }
    dialog.close();
    dialogClosed(focusVisible);
  };
  if (leaving) {
    leaving.finished.then(finish, finish);
    // The backdrop fades while the dialog shrinks.
    fadeBackdrop(true);
  } else {
    finish();
  }
}

/**
 * dialogClosed ends the open dialog's item once the dialog is closed, however
 * it closed: by closeDialog, by a mode change, or by the web view itself (a
 * second Escape without a click between them closes a dialog whatever its
 * cancel handler says).
 */
function dialogClosed(focusVisible = true): void {
  const itemName = detailItemName;
  if (dialog.open || itemName === null) {
    return;
  }
  detailItemName = null;
  // The announcer goes back out with the modal dialog (see openDialog).
  document.body.append(announcer);
  delete dialog.dataset.closing;
  opening = null;
  backdropFade?.cancel();
  backdropFade = null;
  for (const animation of dialog.getAnimations()) {
    animation.cancel();
  }
  focusCard(itemName, focusVisible);
}

async function refresh(): Promise<void> {
  // Only operations that ended before this request are released: a list
  // fetched earlier may not show their result yet. A failed fetch releases
  // them too, so a button is never stuck busy while offline.
  const ended = endedOperations(active, operations);
  try {
    const items = await api.listOptionalInstalls();
    const savedAtUtc = new Date().toISOString();
    view = withLive(items, savedAtUtc);
    lastError = "";
    saveList(storage, items, savedAtUtc);
  } catch (error) {
    lastError = error instanceof Error ? error.message : String(error);
    view = withFailure(view);
  }
  for (const operationId of ended) {
    active = releaseOperation(active, operationId);
  }
  render();
}

/** showView shows a page; "list" shows tab, or without one the last list tab. */
function showView(next: View, tab?: string): void {
  const changed = next !== currentView || (tab !== undefined && resolveTab(listTabs, tab) !== currentTab());
  currentView = next;
  if (tab !== undefined) {
    lastListTab = tab;
  }
  viewDetail.hidden = true;
  detailItemName = null;
  setHomeHidden(next === "activity");
  viewActivity.hidden = next !== "activity";
  markNav();
  if (next === "activity") {
    renderActivity();
  } else if (changed) {
    // The list page stays up from one list to the next, so its heading
    // eases in by hand; a page coming back from Activity brings its own.
    if (!reducedMotion.matches && viewHome.getAnimations().length === 0) {
      listHead.animate([{ opacity: 0, transform: "translateY(0.375rem)" }, { opacity: 1, transform: "none" }], {
        duration: 220,
        easing: "cubic-bezier(0.2, 0.8, 0.2, 1)",
      });
    }
    // The list tabs and My items share the grid; only switching lists rebuilds
    // it, so Back from the detail page can refocus the card it came from. Each
    // list has its own categories. Emptying it first lets the new list's
    // cards all ease in, those it shares with the old one too.
    grid.replaceChildren();
    renderCategories();
    renderAppliesTo();
    renderGrid();
  }
}

stripCancel.addEventListener("click", () => cancelFrom(stripCancel));
detailCancel.addEventListener("click", () => cancelFrom(detailCancel));
queueToggle.addEventListener("click", () => {
  if (queuePanel.hidden) {
    openQueue();
  } else {
    closeQueue(true);
  }
});
// A click outside the strip closes the queue list. Focus goes back to the
// strip only when the click left it nowhere (on no control), so a click on
// another control keeps the focus that control took.
document.addEventListener("click", (event) => {
  // composedPath, not contains: a click can re-render its own target away.
  if (queuePanel.hidden || event.composedPath().includes(strip)) {
    return;
  }
  const lost = document.activeElement === null || document.activeElement === document.body || strip.contains(document.activeElement);
  closeQueue(lost);
});
brandLink.addEventListener("click", () => {
  // The service validated the URL; check again so nothing but http(s) ever
  // leaves the WebView, and open it in the system browser, never in here.
  const url = httpUrl(branding.helpUrl);
  if (url) {
    void api.openExternal(url).catch(() => {});
  }
});
filtersForm.addEventListener("submit", (event) => event.preventDefault());
searchInput.addEventListener("input", renderGrid);
categorySelect.addEventListener("change", filterChanged);
appliesToSelect.addEventListener("change", filterChanged);
retryButton.addEventListener("click", () => {
  view = { ...view, source: view.items.length ? "cache" : "loading" };
  render();
  void refresh();
});
navMine.addEventListener("click", () => showView("mine"));
navActivity.addEventListener("click", () => showView("activity"));
detailBack.addEventListener("click", closeDetail);
detailClose.addEventListener("click", (event) => closeDialog(event.detail === 0));
// Escape asks the dialog to close, and closeDialog does it with its motion.
// When the web view will not let the dialog stay open (the cancel event is
// not cancelable), the close event below does the rest.
dialog.addEventListener("cancel", (event) => {
  if (event.cancelable) {
    event.preventDefault();
    closeDialog();
  }
});
dialog.addEventListener("close", () => dialogClosed());
// A click on the backdrop lands on the dialog element itself, outside its
// box. Pressing inside and releasing outside, as at the end of selecting the
// description's text, is not one.
const outsideDialog = (event: MouseEvent): boolean => {
  const box = dialog.getBoundingClientRect();
  return (
    event.target === dialog &&
    (event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom)
  );
};
let pressedOutside = false;
dialog.addEventListener("pointerdown", (event) => {
  pressedOutside = outsideDialog(event);
});
dialog.addEventListener("click", (event) => {
  if (pressedOutside && outsideDialog(event)) {
    closeDialog(false);
  }
  pressedOutside = false;
});
document.addEventListener("keydown", (event) => {
  // The dialog opens only over a list, and opening it closes the queue list,
  // so none of the Escapes below applies while it is open; its own cancel
  // event handles Escape.
  if (event.key === "Escape" && !queuePanel.hidden) {
    // The open queue list is the innermost thing Escape can close.
    event.preventDefault();
    closeQueue(true);
  } else if (event.key === "Escape" && !viewDetail.hidden) {
    event.preventDefault();
    closeDetail();
  } else if (event.key === "Escape" && !viewActivity.hidden) {
    event.preventDefault();
    showView("list");
  } else if (
    // Ctrl+L shows or hides the log, like Managed Software Center's ⌘L; it is
    // claimed here so the WebView does not treat it as address-bar focus.
    event.ctrlKey &&
    !event.altKey &&
    !event.shiftKey &&
    !event.metaKey &&
    event.key.toLowerCase() === "l"
  ) {
    event.preventDefault();
    // The dialog is modal: the pages under it keep still while it is open.
    if (!dialog.open) {
      showView(viewActivity.hidden ? "activity" : "list");
    }
  }
});

// Render whatever is cached before any network work, then converge on the
// authoritative service response.
applyBranding(brandingView(loadBranding(storage)));
view = fromCache(loadList(storage));
render();
showView("list");
void refresh();
void refreshBranding();
