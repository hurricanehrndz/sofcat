// Screenshots of the storefront demo for the Customization pages
// (`just site-screenshots`). It serves ui/frontend/dist-demo at /demo/, drives
// a headless Chromium (CHROMIUM, default `chromium`) over the DevTools
// protocol, and writes <name>-light.webp and <name>-dark.webp for each shot
// in SHOTS to site/src/assets/customization/, with shots.json, which maps
// each name to its demo query so the docs can link to the live example.
//
//   node site/scripts/screenshots.mjs [name ...]
//
// With names it captures only those shots. Plain Node 24, no dependencies.
import { spawn } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { readFile, stat } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { extname, join, normalize, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = resolve(fileURLToPath(import.meta.url), "../../..");
const DEMO = join(ROOT, "ui/frontend/dist-demo");
const OUT = join(ROOT, "site/src/assets/customization");

// Original artwork for the fictional Acme: a banner background, light marks
// on dark hills, and a wordmark with light lettering for the accent colour.
const BACKGROUND_SVG =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1600 400" preserveAspectRatio="xMidYMid slice">' +
  '<defs><linearGradient id="g" x2="1" y2="1"><stop offset="0" stop-color="#0a2f4a"/>' +
  '<stop offset="1" stop-color="#0b6e4f"/></linearGradient></defs>' +
  '<rect width="1600" height="400" fill="url(#g)"/>' +
  '<circle cx="1160" cy="170" r="56" fill="#f7c59f" fill-opacity=".55"/>' +
  '<path d="M0 290C300 210 520 330 820 260S1300 190 1600 250V400H0Z" fill="#fff" fill-opacity=".08"/>' +
  '<path d="M0 340C380 280 700 390 1010 320S1420 290 1600 330V400H0Z" fill="#fff" fill-opacity=".12"/></svg>';
const WORDMARK_SVG =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 420 96">' +
  '<path d="M12 84 48 12l36 72Z" fill="none" stroke="#fff" stroke-width="9" stroke-linejoin="round"/>' +
  '<text x="104" y="66" fill="#fff" font-family="Helvetica,Arial,sans-serif" font-size="56" ' +
  'font-weight="700" letter-spacing="3">ACME</text>' +
  '<text x="292" y="66" fill="#fff" fill-opacity=".8" font-family="Helvetica,Arial,sans-serif" ' +
  'font-size="30">Software</text></svg>';
const svg = (text) => Buffer.from(text).toString("base64");
const BACKGROUND = { backgroundMime: "image/svg+xml", backgroundBase64: svg(BACKGROUND_SVG) };
const WORDMARK = { logoMime: "image/svg+xml", logoBase64: svg(WORDMARK_SVG) };

// Interactions run in the storefront's window (the desktop's iframe) as
// act(window, document); the capture waits until until(window, document)
// is true. They are serialised into the page, so they must not close over
// anything here. freeze stops the mock's timers once until holds, so running
// work is caught at the same step on every run.
const install = (...names) =>
  new Function(
    "w",
    "d",
    `for (const n of ${JSON.stringify(names)}) d.querySelector('article.card[data-item="' + n + '"] .card-action').click();`,
  );
const percentIs = (name, percent) =>
  new Function(
    "w",
    "d",
    `return d.querySelector('article.card[data-item="${name}"] .action-percent')?.textContent === "${percent}";`,
  );
const openDialog = (w, d) => d.querySelector('article.card[data-item="AcmeNotes"] .card-open').click();
const dialogOpen = (w, d) => d.querySelector("#item-dialog").open;

// The desktop's window stops at 80rem; content_width needs a wider one.
const WIDE_WINDOW = ".window { width: calc(100% - 6rem); }";

// The single source of truth for the docs' pictures. viewport is CSS width,
// height and device scale factor. crop is a selector in the storefront: the
// picture ends CROP_MARGIN CSS pixels below that element.
const DEFAULT_VIEWPORT = [1280, 800, 2];
const CROP_MARGIN = 24;
const SHOTS = [
  // Overview
  { name: "storefront" },
  { name: "unbranded", params: ["unbranded"] },
  // Branding
  { name: "banner-align-center", branding: { bannerAlign: "center" }, crop: ".app-bar" },
  { name: "banner-height-small", branding: { bannerHeight: "small" }, crop: ".app-bar" },
  { name: "banner-height-x-large", branding: { bannerHeight: "x-large" }, crop: ".app-bar" },
  {
    name: "banner-height-fit",
    branding: { ...BACKGROUND, backgroundText: "light", bannerHeight: "fit" },
    crop: ".app-bar",
  },
  {
    name: "background-image",
    branding: { ...BACKGROUND, backgroundText: "light", bannerHeight: "large" },
    crop: ".app-bar",
  },
  { name: "banner-style-logo", branding: { ...WORDMARK, bannerStyle: "logo" }, crop: ".app-bar" },
  { name: "title-size-large", branding: { titleSize: "large", taglineSize: "large" }, crop: ".list-title" },
  { name: "link-app-bar", branding: { linkPlacement: "app_bar" }, crop: ".app-bar" },
  {
    name: "link-position-corner",
    branding: { linkPosition: "bottom_right", bannerHeight: "large" },
    crop: ".app-bar",
  },
  // Layout
  { name: "nav-left", params: ["nav=left", "categories=list"] },
  { name: "filter-placement-left", branding: { filterPlacement: "left" } },
  { name: "content-width-compact", branding: { contentWidth: "compact" }, viewport: [1900, 1000, 1], css: WIDE_WINDOW, crop: "#item-grid" },
  { name: "content-width-full", branding: { contentWidth: "full" }, viewport: [1900, 1000, 1], css: WIDE_WINDOW, crop: "#item-grid" },
  { name: "app-bar-brand-mark", branding: { appBarBrand: "mark" }, crop: ".app-bar" },
  { name: "item-view-dialog", params: ["dialog"], act: openDialog, until: dialogOpen },
  {
    name: "progress-button",
    params: ["progress=button"],
    act: install("PaperTrailPDF"),
    until: percentIs("PaperTrailPDF", "55%"),
    freeze: true,
  },
  {
    // ?serial runs one at a time, as the service does, so the rest wait.
    name: "progress-button-strip",
    params: ["progress=button", "serial"],
    act: install("PaperTrailPDF", "AcmeNotes", "AcmeDocs"),
    until: percentIs("PaperTrailPDF", "55%"),
    freeze: true,
  },
  {
    name: "side-nav-recipe",
    params: ["nav=left", "categories=list", "dialog", "progress=button"],
    act: openDialog,
    until: dialogOpen,
  },
  // Catalog
  { name: "catalog-skills", store: "skills" },
];

/** query is the shot's demo query string, without the "?". */
function query(shot) {
  const parts = [`store=${shot.store ?? "apps"}`, ...(shot.params ?? [])];
  if (shot.branding) {
    parts.push(`branding=${encodeURIComponent(JSON.stringify(shot.branding))}`);
  }
  return parts.join("&");
}

const TYPES = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript",
  ".css": "text/css",
  ".png": "image/png",
  ".svg": "image/svg+xml",
  ".json": "application/json",
};

/** serveDemo serves the demo build at /demo/ on a free local port. */
async function serveDemo() {
  const server = createServer(async (req, res) => {
    const path = decodeURIComponent(new URL(req.url, "http://x").pathname);
    if (!path.startsWith("/demo/")) {
      res.writeHead(404).end();
      return;
    }
    let file = normalize(join(DEMO, path.slice("/demo/".length)));
    if (!file.startsWith(DEMO)) {
      res.writeHead(403).end();
      return;
    }
    try {
      if ((await stat(file)).isDirectory()) {
        file = join(file, "index.html");
      }
      const body = await readFile(file);
      res.writeHead(200, { "content-type": TYPES[extname(file)] ?? "application/octet-stream" }).end(body);
    } catch {
      res.writeHead(404).end();
    }
  });
  await new Promise((done) => server.listen(0, "127.0.0.1", done));
  return server;
}

/** launch starts a headless Chromium and resolves with its DevTools URL. */
function launch(profile) {
  const chromium = spawn(
    process.env.CHROMIUM || "chromium",
    [
      "--headless=new",
      "--remote-debugging-port=0",
      `--user-data-dir=${profile}`,
      // It only ever loads the demo from this script's own server.
      "--no-sandbox",
      "--disable-gpu",
      "--no-first-run",
      "--no-default-browser-check",
      "--hide-scrollbars",
      "--mute-audio",
      "about:blank",
    ],
    { stdio: ["ignore", "ignore", "pipe"] },
  );
  const url = new Promise((done, fail) => {
    let log = "";
    chromium.stderr.on("data", (chunk) => {
      log += chunk;
      const found = log.match(/DevTools listening on (ws:\/\/\S+)/);
      if (found) {
        done(found[1]);
      }
    });
    chromium.on("error", fail);
    chromium.on("exit", (code) => fail(new Error(`chromium exited (${code}):\n${log}`)));
  });
  return { chromium, url };
}

/** connect opens the DevTools socket; send(method, params, sessionId) answers with the result. */
async function connect(url) {
  const socket = new WebSocket(url);
  await new Promise((done, fail) => {
    socket.onopen = done;
    socket.onerror = () => fail(new Error(`cannot connect to ${url}`));
  });
  let next = 0;
  const waiting = new Map();
  socket.onmessage = ({ data }) => {
    const message = JSON.parse(data);
    const call = waiting.get(message.id);
    if (call) {
      waiting.delete(message.id);
      if (message.error) {
        call.fail(new Error(`${call.method}: ${message.error.message}`));
      } else {
        call.done(message.result);
      }
    }
  };
  const send = (method, params = {}, sessionId = undefined) =>
    new Promise((done, fail) => {
      const id = ++next;
      waiting.set(id, { method, done, fail });
      socket.send(JSON.stringify({ id, method, params, sessionId }));
    });
  return { send, close: () => socket.close() };
}

// Expressions run in the desktop page; the storefront is its iframe.
const FRAME = "document.querySelector('.window iframe')";
const READY = `(() => {
  const w = ${FRAME}?.contentWindow;
  const d = w?.document;
  return Boolean(
    d && d.readyState === "complete" &&
    d.querySelector("#item-grid article.card") &&
    d.querySelector("#connection")?.dataset.source === "live" &&
    document.querySelector(".titlebar .title")?.textContent &&
    [...d.images].every((image) => image.complete) &&
    d.fonts.status === "loaded" && document.fonts.status === "loaded"
  );
})()`;
// Nothing moving: no animation in either document, after two frames.
const STILL = `new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(() => {
  const d = ${FRAME}.contentDocument;
  done(document.getAnimations().length === 0 && d.getAnimations().length === 0);
})))`;
const inFrame = (fn) => `(${fn})(${FRAME}.contentWindow, ${FRAME}.contentDocument)`;
// Clears every pending timer of the storefront's window, the mock's included.
const FREEZE = inFrame((w) => {
  const last = w.setTimeout(() => {}, 0);
  for (let id = 0; id <= last; id++) {
    w.clearTimeout(id);
  }
});

async function evaluate(send, session, expression) {
  const { result, exceptionDetails } = await send(
    "Runtime.evaluate",
    { expression, awaitPromise: true, returnByValue: true },
    session,
  );
  if (exceptionDetails) {
    throw new Error(exceptionDetails.exception?.description ?? exceptionDetails.text);
  }
  return result.value;
}

async function until(send, session, expression, what, timeout = 15000) {
  const end = Date.now() + timeout;
  while (!(await evaluate(send, session, expression))) {
    if (Date.now() > end) {
      throw new Error(`timed out waiting for ${what}`);
    }
    await new Promise((done) => setTimeout(done, 50));
  }
}

/** capture takes one shot in one colour scheme, in a fresh browser context. */
async function capture(send, base, shot, scheme) {
  const [width, height, scale] = shot.viewport ?? DEFAULT_VIEWPORT;
  const { browserContextId } = await send("Target.createBrowserContext", { disposeOnDetach: true });
  try {
    const { targetId } = await send("Target.createTarget", { url: "about:blank", browserContextId });
    const { sessionId: s } = await send("Target.attachToTarget", { targetId, flatten: true });
    await send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: scale, mobile: false }, s);
    await send(
      "Emulation.setEmulatedMedia",
      {
        features: [
          { name: "prefers-color-scheme", value: scheme },
          { name: "prefers-reduced-motion", value: "reduce" },
        ],
      },
      s,
    );
    await send("Page.navigate", { url: `${base}/demo/?${query(shot)}` }, s);
    await until(send, s, READY, "the list and connection status");
    if (shot.css) {
      await evaluate(send, s, `document.head.append(Object.assign(document.createElement("style"), { textContent: ${JSON.stringify(shot.css)} }))`);
    }
    if (shot.act) {
      await evaluate(send, s, inFrame(shot.act));
      await until(send, s, inFrame(shot.until), `${shot.name}'s interaction`);
      if (shot.freeze) {
        await evaluate(send, s, FREEZE);
      }
    }
    await until(send, s, STILL, "animations to end");
    let clip;
    if (shot.crop) {
      const bottom = await evaluate(
        send,
        s,
        `${FRAME}.getBoundingClientRect().top + ${FRAME}.contentDocument.querySelector(${JSON.stringify(shot.crop)}).getBoundingClientRect().bottom`,
      );
      clip = { x: 0, y: 0, width, height: Math.ceil(bottom) + CROP_MARGIN, scale: 1 };
    }
    const { data } = await send("Page.captureScreenshot", { format: "webp", quality: 85, clip }, s);
    return Buffer.from(data, "base64");
  } finally {
    await send("Target.disposeBrowserContext", { browserContextId });
  }
}

async function main() {
  const names = process.argv.slice(2);
  const unknown = names.filter((name) => !SHOTS.some((shot) => shot.name === name));
  if (unknown.length > 0) {
    throw new Error(`unknown shot(s): ${unknown.join(", ")}; known: ${SHOTS.map((shot) => shot.name).join(", ")}`);
  }
  readFileSync(join(DEMO, "index.html")); // fail early without a demo build
  mkdirSync(OUT, { recursive: true });
  const chosen = names.length > 0 ? SHOTS.filter((shot) => names.includes(shot.name)) : SHOTS;

  const server = await serveDemo();
  const base = `http://127.0.0.1:${server.address().port}`;
  const profile = mkdtempSync(join(tmpdir(), "sofcat-shots-"));
  const { chromium, url } = launch(profile);
  try {
    const { send, close } = await connect(await url);
    let total = 0;
    for (const shot of chosen) {
      for (const scheme of ["light", "dark"]) {
        const image = await capture(send, base, shot, scheme);
        const file = `${shot.name}-${scheme}.webp`;
        writeFileSync(join(OUT, file), image);
        total += image.length;
        console.log(`${file}  ${(image.length / 1024).toFixed(0)} KB`);
      }
    }
    close();
    console.log(`${chosen.length * 2} images, ${(total / 1024).toFixed(0)} KB`);
  } finally {
    chromium.kill();
    server.close();
    rmSync(profile, { recursive: true, force: true });
  }
  // Every shot's query, whichever were captured, so the file never drops one.
  const queries = Object.fromEntries(SHOTS.map((shot) => [shot.name, query(shot)]));
  writeFileSync(join(OUT, "shots.json"), `${JSON.stringify(queries, null, 2)}\n`);
}

await main();
