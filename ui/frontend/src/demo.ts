// Public demo helpers. main.ts calls them only behind import.meta.env checks
// that Vite folds to false in a production build, so neither function nor the
// bar's text reaches the Wails bundle. Keep this module free of top-level side
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
 * and Activity, so switching stores never shows the other store's list.
 */
export function scopedStorage(storage: StorageLike): StorageLike {
  const { store, unbranded } = current();
  const suffix = `.${store}${unbranded ? ".unbranded" : ""}`;
  return {
    getItem: (key) => storage.getItem(key + suffix),
    setItem: (key, value) => storage.setItem(key + suffix, value),
  };
}

const STYLE = `
.demo-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.25rem 1.25rem;
  padding: 0.4rem 2rem;
  background: var(--warn-bg);
  border-bottom: 1px solid var(--warn-line);
  color: var(--ink);
  font-size: 0.85rem;
}
.demo-bar p { margin: 0; }
.demo-bar nav { display: flex; flex-wrap: wrap; gap: 0.25rem 0.75rem; }
.demo-bar a { color: inherit; text-underline-offset: 2px; }
.demo-bar a[aria-current="page"] { font-weight: 600; text-decoration: none; }
.demo-bar .demo-home { margin-left: auto; }
@media (max-width: 40rem) {
  .demo-bar { padding: 0.4rem 1rem; }
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

/** mountDemoBar adds the demo notice and store switcher above the shell. */
export function mountDemoBar(): void {
  const { store, unbranded } = current();
  const style = document.createElement("style");
  style.textContent = STYLE;
  document.head.append(style);

  const bar = document.createElement("aside");
  bar.className = "demo-bar";
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
  // The landing page frames the demo; leave the frame rather than nest the site in it.
  home.target = "_top";

  bar.append(notice, nav, home);
  document.body.prepend(bar);
}
