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
import { mountDemoBar, scopedStorage } from "./demo.ts";
import {
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
  categories,
  deriveAction,
  enteringItems,
  fromCache,
  glyphTone,
  httpUrl,
  insertRecord,
  actionProgress,
  busyAction,
  endedOperations,
  isProgressTick,
  isTerminalState,
  itemPlacements,
  localErrorState,
  localRecord,
  monogram,
  myItems,
  needsAttention,
  opensDetails,
  placementAction,
  placementStatus,
  progressText,
  refusalText,
  releaseOperation,
  requestErrorText,
  resolveTab,
  restartBadge,
  shouldAcceptRecord,
  showRetry,
  statusLabel,
  statusRecord,
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
if (import.meta.env.MODE === "demo") {
  mountDemoBar();
}

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
const listHead = need<HTMLDivElement>(".list-head");
const resultStatus = need<HTMLParagraphElement>("#result-status");
const grid = need<HTMLDivElement>("#item-grid");
const activityList = need<HTMLOListElement>("#activity-list");
const activityEmpty = need<HTMLParagraphElement>("#activity-empty");
const dialog = need<HTMLDialogElement>("#item-dialog");
const detailClose = need<HTMLButtonElement>("#detail-close");
const detailAction = need<HTMLButtonElement>("#detail-action");
const detailCancel = need<HTMLButtonElement>("#detail-cancel");
const detailPlacements = need<HTMLUListElement>("#detail-placements");
const announcer = need<HTMLParagraphElement>("#announcer");
const spinnerTemplate = need<HTMLTemplateElement>("#spinner");

/** View is a page the nav can show; "list" is one of the list tabs. */
type View = "list" | "mine" | "activity";

let view: ListView = { items: [], source: "loading", savedAtUtc: "" };
let lastError = "";
// The page shown; an item's dialog opens over it.
let currentView: View = "list";
// The list tabs the nav shows, and the one last chosen (null for the first):
// "list" shows it, or the first tab once it is gone.
let listTabs: ListTab[] = [];
let lastListTab: string | null = null;
let navTabsDrawn = "";
// The item the dialog shows, or null while it is closed.
let detailItemName: string | null = null;
let activity: ActivityRecord[] = loadActivity(storage);
let active: ActiveOperations = new Map();
// Operations started in this session, oldest first; Activity keeps the history.
const operations = new Map<string, OperationView>();
let localOperations = 0;
// The action asked for on each item whose request the service has not
// answered yet, so its buttons are busy from the click on.
const requesting = new Map<string, ItemAction>();

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
  root.dataset.navigation = next.navigation;
  root.dataset.appBarBrand = next.appBarBrand;
  // The tab order and the Software tab's heading are branding too.
  renderNav();
  renderCategories();
  renderGrid();
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

/** markNav sets aria-current on the nav button of the page shown. */
function markNav(): void {
  for (const link of document.querySelectorAll<HTMLButtonElement>("nav .nav-link")) {
    const current =
      link.dataset.view === currentView &&
      (currentView !== "list" || link.dataset.tab === currentTab());
    link.setAttribute("aria-current", current ? "page" : "false");
  }
  placeMarker();
}

/**
 * placeMarker moves the nav's marker under the current page's button; the
 * stylesheet eases it there, down the sidebar or along the tabs. It is
 * placed again whenever the nav changes size or shape.
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

let categoriesDrawn = "";

/**
 * renderCategoryNav mirrors the Category select as a list of buttons in the
 * sidebar: one per option, the chosen one marked. The select stays the one
 * place the choice is kept. The buttons are rebuilt only when the categories
 * change, so a render does not take focus off them; a list with no
 * categories, and Activity, have no list.
 */
function renderCategoryNav(): void {
  const options = [...categorySelect.options].map((option) => [option.value, option.textContent ?? ""]);
  categoryNav.hidden = options.length < 2 || currentView === "activity";
  const drawn = JSON.stringify(options);
  if (drawn !== categoriesDrawn) {
    categoriesDrawn = drawn;
    categoryList.replaceChildren(
      ...options.map(([value, label]) => {
        const button = document.createElement("button");
        button.type = "button";
        button.className = "nav-link category-link";
        button.dataset.category = value;
        button.textContent = label;
        button.addEventListener("click", () => {
          if (categorySelect.value !== value) {
            categorySelect.value = value;
            filterChanged();
          }
        });
        return button;
      }),
    );
  }
  for (const button of categoryList.querySelectorAll<HTMLButtonElement>("button")) {
    button.setAttribute("aria-current", String(button.dataset.category === categorySelect.value));
  }
}

/**
 * filterChanged redraws the list for a new category. The grid is emptied
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

/** statusLine is a status paragraph, a live region: an outcome or a reason, never running work. */
function statusLine(): HTMLParagraphElement {
  const status = document.createElement("p");
  status.className = "card-status";
  status.setAttribute("aria-live", "polite");
  const text = document.createElement("span");
  text.className = "status-text";
  status.append(text);
  return status;
}

/** itemNamed is the item of the current list with this name. */
function itemNamed(itemName: string): OptionalInstallItem | undefined {
  return view.items.find((candidate) => candidate.itemName === itemName);
}

/**
 * fillAction gives an action button its parts: a spinner, shown while its
 * request waits, and the label. The bar is the button's own background
 * (style.css), so the button turns into its progress where it stands.
 */
function fillAction(button: HTMLButtonElement): HTMLButtonElement {
  const spinner = spinnerTemplate.content.firstElementChild?.cloneNode(true);
  const label = document.createElement("span");
  label.className = "action-label";
  if (spinner) {
    button.append(spinner);
  }
  button.append(label);
  return button;
}

function actionButton(className: string): HTMLButtonElement {
  const button = document.createElement("button");
  button.type = "button";
  button.className = `pill action ${className}`.trim();
  return fillAction(button);
}

/**
 * paintAction writes an item's action into its button, in place: the label,
 * or while the item is busy its progress (actionProgress), from the click
 * on. The button is never replaced while it shows, so its bar and its width
 * ease from one state to the next. A placement row's button names its row.
 * Without an action (an item the organisation requires) the button is hidden.
 */
function paintAction(
  button: HTMLButtonElement,
  item: OptionalInstallItem,
  action: ItemAction | null,
  row?: OptionalInstallPlacement,
): void {
  button.hidden = action === null;
  if (!action) {
    return;
  }
  // Only the item that owns an in-flight operation is busy; other items
  // stay actionable and route independently by operation ID.
  const busy = busyOf(item.itemName);
  // A request not answered yet has no operation: the item's last one is over.
  const operation = requesting.has(item.itemName) ? undefined : latestOperation(item.itemName);
  const progress = actionProgress(item, action, busy, operation, row?.name);
  const label = progress?.label ?? action.label;
  // The undo action (Remove) is outlined; the others are solid.
  button.classList.toggle("pill-outline", action.kind === "undo");
  button.classList.toggle("pill-primary", action.kind !== "undo");
  const text = button.querySelector<HTMLSpanElement>(".action-label");
  if (text && text.textContent !== label) {
    text.textContent = label;
  }
  markBusy(button, busy);
  if (progress) {
    button.dataset.progress =
      progress.phase === "waiting" ? "waiting" : typeof progress.percent === "number" ? "measured" : "unmeasured";
    button.style.setProperty("--progress", `${progress.percent ?? 0}%`);
  } else {
    delete button.dataset.progress;
    button.style.removeProperty("--progress");
  }
  button.setAttribute("aria-label", row ? `${label}: ${row.label}` : `${label} ${item.displayName}`);
  button.onclick = () => void runAction(item, action);
}

/**
 * createCard builds an item's card, empty; updateCard fills it. A card stays
 * in the grid for as long as its item is listed and is updated in place, so
 * focus stays where it is and the action button eases into its progress.
 */
function createCard(itemName: string): HTMLElement {
  const article = document.createElement("article");
  article.className = "card";
  article.dataset.item = itemName;

  const glyph = document.createElement("span");
  glyph.className = "glyph";
  glyph.setAttribute("aria-hidden", "true");

  // The name is the card's control for keyboard and screen-reader users: it
  // opens the item's dialog, as a click anywhere on the card does.
  const name = document.createElement("h3");
  const open = document.createElement("button");
  open.type = "button";
  open.className = "card-open";
  open.setAttribute("aria-haspopup", "dialog");
  name.append(open);

  const meta = document.createElement("p");
  meta.className = "card-meta card-about";
  const appliesTo = document.createElement("p");
  appliesTo.className = "card-meta card-applies";

  // The status line shows only what the button cannot: a pending or required
  // item's status, or a failure or deferral reason (see cardStatus and
  // updateCardProgress). It is a live region, so a change is announced without
  // the whole grid being re-read.
  const status = statusLine();

  const actions = document.createElement("p");
  actions.className = "card-actions";
  actions.append(actionButton("card-action"));

  // The wrappers let style.css turn a card a sparse row stretches into a
  // single line: glyph, text, then the button.
  const text = document.createElement("div");
  text.className = "card-text";
  text.append(name, meta, appliesTo, status);
  const body = document.createElement("div");
  body.className = "card-body";
  body.append(glyph, text, actions);
  article.append(body);

  const openItem = (): void => {
    const item = itemNamed(itemName);
    if (item) {
      openDetail(item);
    }
  };
  open.addEventListener("click", openItem);
  // A click anywhere but on the action opens the dialog too.
  article.addEventListener("click", (event) => {
    const selecting = !(document.getSelection()?.isCollapsed ?? true);
    if (opensDetails(event.target instanceof Element ? event.target : null, selecting)) {
      openItem();
    }
  });
  return article;
}

function updateCard(article: HTMLElement, item: OptionalInstallItem): void {
  const part = <E extends HTMLElement>(selector: string): E => {
    const element = article.querySelector<E>(selector);
    if (!element) {
      throw new Error(`SofCat UI card is missing ${selector}`);
    }
    return element;
  };
  setGlyph(part(".glyph"), item);
  part(".card-open").textContent = item.displayName;
  const meta = part(".card-about");
  meta.textContent = [item.category, item.developer].filter(Boolean).join(" · ");
  meta.hidden = meta.textContent === "";
  const appliesTo = part(".card-applies");
  appliesTo.textContent = appliesToLine(item);
  appliesTo.hidden = appliesTo.textContent === "";
  // An item the organisation requires has no action.
  const action = part<HTMLButtonElement>(".card-action");
  paintAction(action, item, deriveAction(item));
  part(".card-actions").hidden = action.hidden;
  updateCardProgress(article, item, cardStatus(item));
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
 * updateCardProgress writes into a status line what the item's button cannot
 * say: idleText when nothing is going on (an empty one hides the line), an
 * outcome that needs attention, or the reason a cancel was refused while the
 * work carries on. Running work is on the button and the full timeline only
 * in Activity.
 */
function updateCardProgress(container: HTMLElement, item: OptionalInstallItem, idleText: string): void {
  const status = container.querySelector<HTMLParagraphElement>(".card-status");
  const text = status?.querySelector<HTMLSpanElement>(".status-text");
  if (!status || !text) {
    throw new Error("SofCat UI card is missing its status line");
  }
  const operation = latestOperation(item.itemName);
  const progress = operation
    ? cardProgress(item, operation.records, operation.outcome, operation.action.method, operation.notice)
    : null;
  const refused = operation?.outcome === "active" && Boolean(operation.notice);
  if (!progress || !(needsAttention(progress) || refused)) {
    text.textContent = idleText;
    status.hidden = idleText === "";
    delete status.dataset.outcome;
    return;
  }
  text.textContent = progressText(progress);
  status.hidden = false;
  // "plain" opts out of the warning box the stylesheet gives finished outcomes.
  status.dataset.outcome = progress.plain ? "plain" : progress.outcome;
}

/** cardOf is the card of an item in the grid, if the list shows it. */
function cardOf(itemName: string): HTMLElement | null {
  return grid.querySelector<HTMLElement>(`[data-item="${CSS.escape(itemName)}"]`);
}

/** renderCardFor refreshes one item's card, and the dialog when it shows that item, in place. */
function renderCardFor(itemName: string): void {
  const article = cardOf(itemName);
  const item = itemNamed(itemName);
  if (article && item) {
    updateCard(article, item);
  }
  if (detailItemName === itemName) {
    renderDetail();
  }
}

function renderGrid(): void {
  const mine = currentView === "mine";
  const tab = currentTab();
  const listed = listedItems();
  const shown = visibleItems(listed, searchInput.value, categorySelect.value);
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
  // Cards already listed are updated where they stand; only new ones are
  // built, and only cards out of place are moved.
  const listedCards = new Map([...grid.children].map((child) => [(child as HTMLElement).dataset.item, child]));
  const articles = shown.map((item) => {
    const existing = listedCards.get(item.itemName) as HTMLElement | undefined;
    const article = existing ?? createCard(item.itemName);
    updateCard(article, item);
    article.toggleAttribute("data-entering", !existing && entering.has(item.itemName));
    return article;
  });
  // New cards ease in one after another; the first dozen set the pace.
  articles
    .filter((article) => article.hasAttribute("data-entering"))
    .forEach((article, index) => article.style.setProperty("--enter-order", String(Math.min(index, 12))));
  articles.forEach((article, index) => {
    if (grid.children[index] !== article) {
      grid.insertBefore(article, grid.children[index] ?? null);
    }
  });
  while (grid.children.length > articles.length) {
    grid.lastElementChild?.remove();
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
  renderGrid();
  renderDetail();
  navBusy.toggleAttribute("hidden", active.size === 0);
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
 * leaves the item's state alone and shows the service's reason in its status line.
 */
async function cancelOperation(operation: OperationView): Promise<void> {
  operation.cancelPending = true;
  renderCardFor(operation.item.itemName);
  try {
    await api.cancelOperation(operation.operationId);
  } catch (error) {
    operation.cancelPending = false;
    operation.notice = refusalText(reason(error));
    renderCardFor(operation.item.itemName);
  }
}

/** failOperation marks only the local display record; it never touches item state. */
function failOperation(operationId: string, message: string): void {
  const operation = operations.get(operationId);
  if (!operation || operation.outcome !== "active") {
    return;
  }
  operation.outcome = "error";
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
    render();
    void refresh();
    return;
  }
  // The buttons are updated in place and are not live regions; the announcer
  // says each new phase once, without its percentage.
  renderCardFor(operation.item.itemName);
  const progress = cardProgress(operation.item, operation.records, "active", operation.action.method);
  const line = progress ? `${operation.item.displayName}: ${progress.label}` : "";
  if (line && announcer.textContent !== line) {
    announcer.textContent = line;
  }
});

function setText(selector: string, value: string, fallback = "Not provided"): void {
  need<HTMLElement>(selector).textContent = value.trim() || fallback;
}

/**
 * renderDetail fills the dialog for the item being viewed. It reads the item
 * from the current list, so a refresh after a terminal record updates the
 * status and the action button in place. An item that left the list closes it.
 */
function renderDetail(): void {
  if (detailItemName === null) {
    return;
  }
  const item = itemNamed(detailItemName);
  if (!item) {
    closeDetail();
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

  paintAction(detailAction, item, deriveAction(item));

  // Cancel sits beside the action while an operation on the item runs. It is
  // a real disabled button once the installer has started; its title says
  // why. A busy Cancel action is already the way out of a pending request.
  const operation = latestOperation(item.itemName);
  const running = operation?.outcome === "active" && operation.action.kind !== "cancel" ? operation : undefined;
  detailCancel.hidden = !running;
  if (running) {
    const latest = running.records.at(-1)?.state ?? "";
    const pending = Boolean(running.cancelPending);
    detailCancel.disabled = pending || !canCancel(latest);
    detailCancel.title = cancelTitle(latest, pending);
    const what = running.action.method === "RemoveItem" ? "removal" : "install";
    detailCancel.textContent = actionLabel(item, "cancel");
    detailCancel.setAttribute("aria-label", `${detailCancel.textContent} ${what} of ${item.displayName}`);
    detailCancel.onclick = () => void cancelOperation(running);
  }

  // The Status row already says the catalog state and the button shows
  // running work, so the line only appears for an outcome or a refusal.
  updateCardProgress(dialog, item, "");
}

/**
 * renderPlacements draws one row per placement in the dialog: its label, its
 * status and the item's action for that placement alone. Rows follow the
 * item's own button while it has an operation running. The rows are built
 * once per item and set of placements, then updated in place.
 */
function renderPlacements(item: OptionalInstallItem, placements: OptionalInstallPlacement[]): void {
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
        row.append(label, status, actionButton(""));
        return row;
      }),
    );
  }
  placements.forEach((placement, index) => {
    const row = detailPlacements.children[index];
    const [label, status, action] = row.children;
    label.textContent = placement.label;
    status.textContent = placementStatus(item, placement);
    paintAction(action as HTMLButtonElement, item, placementAction(item, placement), placement);
  });
}

/**
 * setHomeHidden shows or hides the list page. Hiding it ends every card's
 * entrance: a shown element restarts its animations, and the page's own
 * rise-in is the only motion it should bring back.
 */
function setHomeHidden(hidden: boolean): void {
  if (hidden) {
    for (const entering of grid.querySelectorAll("[data-entering]")) {
      entering.removeAttribute("data-entering");
    }
  }
  viewHome.hidden = hidden;
}

const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");

/**
 * morph plays the dialog growing out of card, or back into it when closing:
 * it starts where the card is, at the card's size. Null, and no motion, when
 * the card is not on screen, the user asked for reduced motion or the web
 * view cannot animate.
 */
function morph(card: Element | null, closing: boolean): Animation | null {
  if (!card || reducedMotion.matches || typeof dialog.animate !== "function") {
    return null;
  }
  const from = card.getBoundingClientRect();
  const to = dialog.getBoundingClientRect();
  if (to.width === 0 || to.height === 0) {
    return null;
  }
  const small = {
    transform: `translate(${from.left - to.left}px, ${from.top - to.top}px) scale(${from.width / to.width}, ${from.height / to.height})`,
    opacity: 0.2,
  };
  const full = { transform: "none", opacity: 1 };
  return dialog.animate(closing ? [full, small] : [small, full], {
    duration: closing ? 180 : 280,
    easing: "cubic-bezier(0.2, 0.8, 0.2, 1)",
  });
}

function openDetail(item: OptionalInstallItem): void {
  detailItemName = item.itemName;
  renderDetail();
  if (!dialog.open) {
    dialog.showModal();
    dialog.scrollTop = 0;
    morph(cardOf(item.itemName), false);
  }
}

/**
 * closeDetail closes the dialog, back into the card it came from, and hands
 * focus to that card's name. The card is found by item, because a refresh
 * while the dialog was open may have moved it. When the card is gone from the
 * list, dropped by a refresh or hidden by the filters, focus goes to the
 * search field at the top of the list instead.
 */
function closeDetail(): void {
  if (!dialog.open || detailItemName === null || "closing" in dialog.dataset) {
    return;
  }
  const itemName = detailItemName;
  const finish = (): void => {
    delete dialog.dataset.closing;
    dialog.close();
    detailItemName = null;
    (cardOf(itemName)?.querySelector<HTMLButtonElement>(".card-open") ?? searchInput).focus();
  };
  // The backdrop fades while the dialog shrinks (style.css).
  dialog.dataset.closing = "";
  const leaving = morph(cardOf(itemName), true);
  if (leaving) {
    leaving.finished.then(finish, finish);
  } else {
    finish();
  }
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
  setHomeHidden(next === "activity");
  viewActivity.hidden = next !== "activity";
  markNav();
  if (next === "activity") {
    renderActivity();
    renderCategoryNav();
  } else if (changed) {
    // The list page stays up from one list to the next, so its heading
    // eases in by hand; a page coming back from Activity brings its own.
    if (!reducedMotion.matches && viewHome.getAnimations().length === 0) {
      listHead.animate([{ opacity: 0, transform: "translateY(0.375rem)" }, { opacity: 1, transform: "none" }], {
        duration: 220,
        easing: "cubic-bezier(0.2, 0.8, 0.2, 1)",
      });
    }
    // The list tabs and My items share the grid; only switching lists empties
    // it. Each list has its own categories. Emptying it first lets the new list's
    // cards all ease in, those it shares with the old one too.
    grid.replaceChildren();
    renderCategories();
    renderGrid();
  }
}

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
retryButton.addEventListener("click", () => {
  view = { ...view, source: view.items.length ? "cache" : "loading" };
  render();
  void refresh();
});
navMine.addEventListener("click", () => showView("mine"));
navActivity.addEventListener("click", () => showView("activity"));
detailClose.addEventListener("click", closeDetail);
// Escape asks the dialog to close; closeDetail does it, with its motion.
dialog.addEventListener("cancel", (event) => {
  event.preventDefault();
  closeDetail();
});
// A click on the backdrop lands on the dialog itself; its content is all
// inside .dialog-body.
dialog.addEventListener("click", (event) => {
  if (event.target === dialog) {
    closeDetail();
  }
});
fillAction(detailAction);
document.addEventListener("keydown", (event) => {
  // The dialog is modal: the pages under it keep still while it is open.
  if (dialog.open) {
    return;
  }
  if (event.key === "Escape" && !viewActivity.hidden) {
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
    showView(viewActivity.hidden ? "activity" : "list");
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
