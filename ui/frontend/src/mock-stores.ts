// SOFCAT_VITE_MOCK_ONLY — fixture stores for mock-api.ts (`npm run dev` and the
// public demo build). vite.config.ts resolves the mock only in development and
// demo modes, so none of this reaches the Wails bundle.
import type { Branding, OptionalInstallItem } from "./api.ts";

export const STORE_NAMES = ["apps", "skills"] as const;
export type StoreName = (typeof STORE_NAMES)[number];

/** How the mock plays an item's operation; the default succeeds. */
export type MockBehavior = {
  outcome?: "fail" | "missing" | "blocked" | "no-terminal";
  /** The service's message for fail, missing and blocked. */
  reason?: string;
  /** An item installed as part of this one's run. */
  dependency?: { itemName: string; displayName: string };
};

/** MockItem is a wire item plus how the mock plays it; listOptionalInstalls strips mock. */
export type MockItem = OptionalInstallItem & { mock?: MockBehavior };

export type MockStore = { branding: Branding; items: MockItem[] };

function params(): URLSearchParams {
  return new URLSearchParams(globalThis.location?.search ?? "");
}

/** storeName is `?store=`, defaulting to apps for a missing or unknown value. */
export function storeName(): StoreName {
  const wanted = params().get("store");
  return STORE_NAMES.find((name) => name === wanted) ?? "apps";
}

/** unbranded is `?unbranded`: the stock SofCat look over the same items. */
export function unbranded(): boolean {
  return params().has("unbranded");
}

export function offline(): boolean {
  return params().has("offline");
}

// Original marks drawn for the fictional Acme; btoa keeps them editable here.
const APPS_LOGO =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="14" fill="#0b6e4f"/>' +
  '<path d="M32 11 51 53h-9.5l-3.7-8.5H26.2L22.5 53H13z" fill="#fff"/><path d="M28.6 37h6.8L32 28.6z" fill="#0b6e4f"/></svg>';
const SKILLS_LOGO =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="14" fill="#6d28d9"/>' +
  '<path d="M28 9c2 11 5 15 18 19-13 4-16 8-18 19-2-11-5-15-18-19 13-4 16-8 18-19z" fill="#fff"/>' +
  '<circle cx="48" cy="48" r="6" fill="#fff"/></svg>';

const UPDATED = "2026-11-01T12:00:00Z";

function item(fields: Partial<MockItem> & Pick<MockItem, "itemName" | "displayName" | "version">): MockItem {
  return {
    catalog: "acme",
    isManaged: false,
    isRequired: false,
    isInstalled: false,
    status: "NotInstalled",
    statusUpdatedAtUtc: UPDATED,
    ...fields,
  };
}

function installed(fields: Partial<MockItem>): Partial<MockItem> {
  return { isManaged: true, isInstalled: true, status: "Installed", ...fields };
}

const ACME_IT = "Acme IT";
const ACME_AI = "Acme AI Platform";

const apps: MockItem[] = [
  item({
    itemName: "AcmeNotes",
    displayName: "Acme Notes",
    version: "8.2.0",
    description: "Notebooks for meetings and projects, synced to your Acme account. MSI installer.",
    category: "Notes",
    section: "Productivity",
    developer: ACME_IT,
    restartAction: "RecommendRestart",
    mock: { dependency: { itemName: "AcmeRuntime", displayName: "Acme Runtime" } },
  }),
  item({
    itemName: "AcmeDocs",
    displayName: "Acme Docs Suite",
    version: "2026.10",
    description: "Word processor, spreadsheets and slides. Close any open documents before installing.",
    category: "Office",
    section: "Productivity",
    developer: ACME_IT,
    restartAction: "RequireRestart",
    mock: { outcome: "blocked", reason: "blocking application(s) running: acmedocs" },
  }),
  item({
    itemName: "AcmeMeet",
    displayName: "Acme Meet",
    version: "5.4.1",
    description: "Video meetings and chat. Already requested; Cancel withdraws the request.",
    category: "Communication",
    section: "Productivity",
    developer: ACME_IT,
    isManaged: true,
    status: "WillBeInstalled",
  }),
  item({
    itemName: "PaperTrailPDF",
    displayName: "PaperTrail PDF",
    version: "11.0.3",
    description: "Read, sign and annotate PDF files. EXE installer, silent install.",
    category: "Office",
    section: "Productivity",
    developer: "PaperTrail Labs",
  }),
  item({
    itemName: "AcmeCode",
    displayName: "Acme Code Editor",
    version: "1.96.0",
    description: "A fast editor with Acme's extensions and settings preinstalled.",
    category: "Editors",
    section: "Developer tools",
    developer: ACME_IT,
    mock: { dependency: { itemName: "AcmeRuntime", displayName: "Acme Runtime" } },
  }),
  item({
    itemName: "ForgeGitTools",
    displayName: "Forge Git Tools",
    version: "2.47.0",
    description: "Version control client and credential helper. This build fails to install, on purpose.",
    category: "Source control",
    section: "Developer tools",
    developer: "Forge Software",
    mock: { outcome: "fail", reason: "installer exited with code 1603" },
  }),
  item({
    itemName: "HarborDesktop",
    displayName: "Harbor Desktop",
    version: "4.36.0",
    description: "Local containers for development. Already requested for removal.",
    category: "Containers",
    section: "Developer tools",
    developer: "Harbor Works",
    restartAction: "RequireRestart",
    ...installed({ status: "WillBeRemoved" }),
  }),
  item({
    itemName: "AcmeVPN",
    displayName: "Acme VPN",
    version: "6.1.0",
    description: "Required on every Acme device: no Install or Remove, only Details.",
    category: "Security",
    section: "Utilities",
    developer: ACME_IT,
    isRequired: true,
    ...installed({ isManaged: false }),
  }),
  item({
    itemName: "AcmeRuntime",
    displayName: "Acme Runtime",
    version: "3.2.0",
    description: "Shared components for Acme apps; installs with the apps that need it.",
    category: "Runtimes",
    section: "Utilities",
    developer: ACME_IT,
    ...installed({}),
  }),
  item({
    itemName: "ZipKit",
    displayName: "ZipKit Archiver",
    version: "24.08",
    description: "Open and create zip, 7z and tar archives from Explorer.",
    category: "Files",
    section: "Utilities",
    developer: "ZipKit Project",
  }),
  item({
    itemName: "ScreenSnap",
    displayName: "ScreenSnap",
    version: "3.0.2",
    description: "Screenshots and short screen recordings with redaction tools.",
    category: "Files",
    section: "Utilities",
    developer: "Snapline",
    restartAction: "RecommendRestart",
  }),
];

const skills: MockItem[] = [
  item({
    itemName: "ReleaseNotesWriter",
    displayName: "Release Notes Writer",
    version: "1.4.0",
    description: "Drafts release notes from merged pull requests in Acme's house style. macOS and Windows.",
    category: "Writing",
    section: "Skills",
    developer: ACME_AI,
    actionLabels: { install: "Add", installing: "Adding…", installed: "Added", remove: "Remove" },
    appliesTo: ["Claude Code", "Codex"],
  }),
  item({
    itemName: "AcmeBrandVoice",
    displayName: "Acme Brand Voice",
    version: "2.1.0",
    description: "Tone and terminology for anything customers read. Required for everyone.",
    category: "Writing",
    section: "Skills",
    developer: "Acme Communications",
    isRequired: true,
    ...installed({ isManaged: false }),
    actionLabels: { installed: "Added" },
    appliesTo: ["Claude Code", "Codex"],
  }),
  item({
    itemName: "ReviewChecklist",
    displayName: "Code Review Checklist",
    version: "0.9.2",
    description: "Walks a diff against Acme's review checklist. Already requested.",
    category: "Engineering",
    section: "Skills",
    developer: ACME_AI,
    isManaged: true,
    status: "WillBeInstalled",
    actionLabels: { install: "Add", installing: "Adding…", installed: "Added", pending_install: "Will be added" },
    appliesTo: ["Claude Code"],
  }),
  item({
    itemName: "PostmortemHelper",
    displayName: "Postmortem Helper",
    version: "0.3.0",
    description: "Turns an incident timeline into a blameless postmortem. This one fails, on purpose.",
    category: "Engineering",
    section: "Skills",
    developer: ACME_AI,
    actionLabels: { install: "Add", installing: "Adding…", installed: "Added" },
    appliesTo: ["Codex"],
    mock: { outcome: "fail", reason: "archive checksum mismatch" },
  }),
  item({
    itemName: "SQLExplainer",
    displayName: "SQL Explainer",
    version: "1.0.1",
    description: "Explains query plans and suggests indexes for the warehouse.",
    category: "Data",
    section: "Skills",
    developer: "Acme Data",
    actionLabels: { install: "Add", installing: "Adding…", installed: "Added" },
    appliesTo: ["Claude Code", "Codex"],
  }),
  item({
    itemName: "DocsSearch",
    displayName: "Acme Docs Search",
    version: "1.2.0",
    description: "A plugin that searches the internal handbook and wiki, with its index skill.",
    category: "Knowledge",
    section: "Plugins",
    developer: ACME_AI,
    actionLabels: {
      install: "Make available",
      installing: "Making available…",
      installed: "Available",
      remove: "Withdraw",
      applies_to: "Works in",
    },
    appliesTo: ["Claude Code"],
    mock: { dependency: { itemName: "DocsIndex", displayName: "Docs Index skill" } },
  }),
  item({
    itemName: "TicketsPlugin",
    displayName: "Acme Tickets",
    version: "0.4.0",
    description: "Read and update service desk tickets from your coding agent.",
    category: "Workflow",
    section: "Plugins",
    developer: ACME_AI,
    actionLabels: {
      install: "Make available",
      installing: "Making available…",
      installed: "Available",
      remove: "Withdraw",
      applies_to: "Works in",
    },
    appliesTo: ["Claude Code", "Codex"],
    mock: { outcome: "missing", reason: "Needs Python 3.11 or later. Install it first, then try again." },
  }),
  item({
    itemName: "DesignTokens",
    displayName: "Design Tokens",
    version: "2.0.0",
    description: "Brings Acme's colours, spacing and type scale into UI work. Being withdrawn.",
    category: "Design",
    section: "Plugins",
    developer: "Acme Design",
    ...installed({ status: "WillBeRemoved" }),
    actionLabels: {
      installed: "Available",
      remove: "Withdraw",
      pending_remove: "Will be withdrawn",
      applies_to: "Works in",
    },
    appliesTo: ["Claude Code"],
  }),
];

// Dev only: a stream that ends before any terminal record, for the stream_ended UI.
if (import.meta.env?.DEV) {
  apps.push(
    item({
      itemName: "StreamEnds",
      displayName: "7",
      version: "7.0.0",
      description: "Dev only: a monogram fallback and a stream that ends before any terminal record.",
      status: "Unknown",
      mock: { outcome: "no-terminal" },
    }),
  );
}

export const STORES: Record<StoreName, MockStore> = {
  apps: {
    items: apps,
    branding: {
      title: "Acme Software Center",
      tagline: "Approved apps for your Acme PC. Need help? Call the service desk at ext. 1234.",
      helpUrl: "https://sofcat.app/getting-started/",
      helpLabel: "Service desk",
      accent: "#0b6e4f",
      logoMime: "image/svg+xml",
      logoBase64: btoa(APPS_LOGO),
      backgroundMime: "",
      backgroundBase64: "",
      backgroundText: "",
      listHeading: "Apps for Acme",
      sections: ["Productivity", "Developer tools", "Utilities"],
    },
  },
  skills: {
    items: skills,
    branding: {
      title: "Acme AI Skills",
      tagline: "Skills and plugins for Claude Code and Codex, on macOS and Windows.",
      helpUrl: "https://sofcat.app/ai-store/overview/",
      helpLabel: "Guidelines",
      accent: "#6d28d9",
      logoMime: "image/svg+xml",
      logoBase64: btoa(SKILLS_LOGO),
      backgroundMime: "",
      backgroundBase64: "",
      backgroundText: "",
      listHeading: "Skills for Acme teams",
      sections: ["Skills", "Plugins"],
    },
  },
};

export const UNBRANDED: Branding = {
  title: "",
  tagline: "",
  helpUrl: "",
  helpLabel: "",
  accent: "",
  logoMime: "",
  logoBase64: "",
  backgroundMime: "",
  backgroundBase64: "",
  backgroundText: "",
  listHeading: "",
  sections: [],
};
