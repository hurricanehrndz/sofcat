// Public demo helpers. main.ts calls scopedStorage only behind import.meta.env
// checks that Vite folds to false in a production build, and only the demo
// build's entry (demo-main.ts) calls mountDesktop, so neither the desktop nor
// its text reaches the Wails bundle. Keep this module free of top-level side
// effects (and of imports from the mock) so it tree-shakes cleanly.
import type { StorageLike } from "./cache.ts";

// Mirrors mock-stores.ts's STORE_NAMES, without importing the fixtures.
const STORES = [
  ["apps", "Software Center"],
  ["skills", "AI Skills"],
] as const;

function current(): { store: string; unbranded: boolean } {
  const params = new URLSearchParams(location.search);
  const wanted = params.get("store");
  return {
    store: STORES.find(([name]) => name === wanted)?.[0] ?? STORES[0][0],
    unbranded: params.has("unbranded"),
  };
}

/**
 * scopedStorage gives each mock store (and its unbranded twin) its own cache
 * and Activity, so switching stores never shows the other store's list. A
 * `?branding=` link gets one too, so its look never flashes in the plain store.
 */
export function scopedStorage(storage: StorageLike): StorageLike {
  const { store, unbranded } = current();
  const custom = new URLSearchParams(location.search).has("branding");
  const suffix = `.${store}${unbranded ? ".unbranded" : ""}${custom ? ".custom" : ""}`;
  return {
    getItem: (key) => storage.getItem(key + suffix),
    setItem: (key, value) => storage.setItem(key + suffix, value),
  };
}

/**
 * embedUrl is the storefront the desktop's window shows: the page's own URL
 * with `embed` added, so every other demo parameter reaches the mock.
 */
export function embedUrl(href: string): string {
  const url = new URL(href);
  url.search = url.search.length > 1 ? `${url.search}&embed` : "?embed";
  return url.href;
}

// An original wallpaper and a window drawn after macOS's, nothing of Apple's.
// Below 48rem there is no desktop: the menu bar is a plain notice and the
// storefront fills the rest, so a phone gets the storefront's own layout.
const STYLE = `
:root {
  --menu-bg: rgb(255 255 255 / 55%);
  --menu-ink: #1d1d22;
  --frame: #e9e9ec;
  --frame-line: rgb(0 0 0 / 18%);
  --frame-ink: #4a4a52;
  --wallpaper:
    radial-gradient(ellipse at 15% 20%, #f7c59f 0, transparent 55%),
    radial-gradient(ellipse at 85% 15%, #9fc3f7 0, transparent 50%),
    radial-gradient(ellipse at 70% 90%, #c3a6f0 0, transparent 55%),
    linear-gradient(160deg, #f4d9c6, #b9cdf2);
  color-scheme: light dark;
  font-family: system-ui, sans-serif;
}
@media (prefers-color-scheme: dark) {
  :root {
    --menu-bg: rgb(30 30 36 / 55%);
    --menu-ink: #f2f2f4;
    --frame: #2b2b30;
    --frame-line: rgb(255 255 255 / 14%);
    --frame-ink: #c4c4cc;
    --wallpaper:
      radial-gradient(ellipse at 15% 20%, #6b3a5c 0, transparent 55%),
      radial-gradient(ellipse at 85% 15%, #1f4a7a 0, transparent 50%),
      radial-gradient(ellipse at 70% 90%, #3d2a6b 0, transparent 55%),
      linear-gradient(160deg, #2a1d33, #0f1a2e);
  }
}
html, body { height: 100%; margin: 0; }
body {
  display: flex;
  flex-direction: column;
  background: var(--wallpaper);
  color: var(--menu-ink);
}
.menu-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.25rem 1.25rem;
  padding: 0.3rem 1rem;
  background: var(--menu-bg);
  -webkit-backdrop-filter: blur(20px);
  backdrop-filter: blur(20px);
  font-size: 0.8rem;
}
.menu-bar p { margin: 0; }
.menu-bar nav { display: flex; flex-wrap: wrap; gap: 0.25rem 0.75rem; }
.menu-bar a { color: inherit; text-underline-offset: 2px; }
.menu-bar a[aria-current="page"] { font-weight: 600; text-decoration: none; }
.menu-bar .demo-home { margin-left: auto; }
.desk {
  flex: 1;
  display: grid;
  place-items: center;
  min-height: 0;
}
.window {
  display: flex;
  flex-direction: column;
  width: min(80rem, 100% - 6rem);
  height: min(52rem, 100% - 4rem);
  overflow: hidden;
  border: 1px solid var(--frame-line);
  border-radius: 0.75rem;
  background: var(--frame);
  box-shadow: 0 1.5rem 4rem rgb(0 0 0 / 35%), 0 0 0 0.5px rgb(0 0 0 / 30%);
}
.titlebar {
  display: grid;
  grid-template-columns: 4.5rem 1fr 4.5rem;
  align-items: center;
  height: 1.9rem;
  padding: 0 0.75rem;
  border-bottom: 1px solid var(--frame-line);
  color: var(--frame-ink);
  font-size: 0.8rem;
  font-weight: 600;
}
.lights { display: flex; gap: 0.5rem; }
.lights i { width: 0.75rem; height: 0.75rem; border-radius: 50%; background: #ff5f57; }
.lights i:nth-child(2) { background: #febc2e; }
.lights i:nth-child(3) { background: #28c840; }
.titlebar .title { overflow: hidden; text-align: center; text-overflow: ellipsis; white-space: nowrap; }
.window iframe { flex: 1; width: 100%; border: 0; }
@media (max-width: 48rem) {
  body { background: Canvas; }
  .menu-bar {
    background: #fdf3d7;
    border-bottom: 1px solid #d9b34a;
    color: #1b1b1f;
    font-size: 0.85rem;
  }
  .window { width: 100%; height: 100%; border: 0; border-radius: 0; box-shadow: none; }
  .titlebar { display: none; }
}
@media (max-width: 48rem) and (prefers-color-scheme: dark) {
  .menu-bar { background: #3b3320; border-color: #9a7e2b; color: #f2f2f4; }
}
`;

function link(href: string, text: string, isCurrent = false): HTMLAnchorElement {
  const anchor = document.createElement("a");
  anchor.href = href;
  anchor.textContent = text;
  if (isCurrent) {
    anchor.setAttribute("aria-current", "page");
  }
  return anchor;
}

function menuBar(): HTMLElement {
  const { store, unbranded } = current();
  const bar = document.createElement("aside");
  bar.className = "menu-bar";
  bar.setAttribute("aria-label", "Demo");

  const notice = document.createElement("p");
  const strong = document.createElement("strong");
  strong.textContent = "Demo";
  notice.append(strong, " — simulated service, nothing is installed");

  const nav = document.createElement("nav");
  nav.setAttribute("aria-label", "Demo stores");
  nav.append(
    ...STORES.map(([name, label]) => link(`?store=${name}`, label, !unbranded && name === store)),
    link(`?store=${store}&unbranded`, "Unbranded", unbranded),
  );

  const home = link("https://sofcat.app/", "sofcat.app ↗");
  home.className = "demo-home";
  // The landing page may frame this page too; leave the frame rather than nest the site in it.
  home.target = "_top";

  bar.append(notice, nav, home);
  return bar;
}

/**
 * The window's title follows the storefront's product name, as the app's
 * window is titled with the branding title, or the product name without one.
 * The document title never changes, so it would always read "SofCat UI".
 */
function mirrorTitle(frame: HTMLIFrameElement, title: HTMLElement): void {
  frame.addEventListener("load", () => {
    const name = frame.contentDocument?.querySelector("#product-name");
    if (!name) {
      return;
    }
    const update = (): void => {
      title.textContent = name.textContent;
    };
    update();
    new MutationObserver(update).observe(name, { subtree: true, childList: true, characterData: true });
  });
}

/**
 * mountDesktop replaces the page with the demo's desktop: the notice and store
 * switcher in a menu bar, and the storefront (embedUrl) in a window. The
 * storefront runs in its own document, so its layout follows the window's
 * width and its dialogs stay inside the window, as in the app.
 */
export function mountDesktop(): void {
  // The shell's own stylesheet styles body; the desktop brings its own.
  for (const sheet of document.querySelectorAll('link[rel="stylesheet"], style')) {
    sheet.remove();
  }
  const style = document.createElement("style");
  style.textContent = STYLE;
  document.head.append(style);

  const lights = document.createElement("span");
  lights.className = "lights";
  lights.append(...Array.from({ length: 3 }, () => document.createElement("i")));
  const title = document.createElement("span");
  title.className = "title";
  const titlebar = document.createElement("div");
  titlebar.className = "titlebar";
  titlebar.setAttribute("aria-hidden", "true");
  titlebar.append(lights, title, document.createElement("span"));

  const frame = document.createElement("iframe");
  frame.title = "SofCat storefront";
  frame.src = embedUrl(location.href);
  mirrorTitle(frame, title);

  const win = document.createElement("div");
  win.className = "window";
  win.append(titlebar, frame);
  const desk = document.createElement("div");
  desk.className = "desk";
  desk.append(win);

  document.body.replaceChildren(menuBar(), desk);
}
