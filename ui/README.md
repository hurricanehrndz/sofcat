# SofCat UI

SofCat UI is a Wails v3 desktop application in the repository's root Go module.
It runs as the logged-in standard user and calls the existing SYSTEM SofCat
service through the shared typed client in `pkg/service`. It never receives
SofCat configuration, credentials, package-server settings, or internal catalog
objects.

## Layout

- `main.go`: Wails application, `--pipe-name` flag (default `sofcat-service`),
  bundled WebView window titled with the branding title, or `SofCat UI`
- `service.go`: the bound service — `ListOptionalInstalls`, `InstallItem`,
  `RemoveItem`, `WatchOperation`, `GetBranding`, `CancelOperation`
- `log.go`: opt-in diagnostics
- `assets_production.go` / `assets_development.go`: embedded `frontend/dist` under
  the `production` tag, compile-safe source filesystem otherwise
- `frontend/`: vanilla TypeScript, CSS, and Vite assets (no framework, router, or
  component library)
- `frontend/bindings/`: Wails-generated TypeScript — committed, never hand-edited
- `frontend/src/wails-api.ts` and `mock-api.ts`: the two implementations of the
  single `SofCatApi` adapter declared in `frontend/src/api.ts`

## Backend surface

Six bound methods and exactly one event channel, `sofcat:operation-status`.
Each status record carries `operationId`, `seq`, `itemName`, `displayName`,
`state`, item-scoped `progressPercent` (only when measured), `message`, a
millisecond `timestampUtc`, and terminal error/cancellation fields. The frontend subscribes once, routes
records by `operationId` and orders each operation's records by `seq`.

Behind the bindings, `pkg/service.Client` talks JSON-RPC 2.0 to the service over
the named pipe; the "Protocol" section of `ARCHITECTURE.md` is the contract.
Service errors reach the frontend as `<data.code>: <message>`, for example
`operation_not_cancelable: ...` or `server_busy: ...`.

Self-service selections belong to the machine: on a shared machine (fast
user switching, Remote Desktop Services) every user sees the one self-service
selection. Only the user who selected an item, or a local administrator, can
remove it, take back its pending removal, or cancel that user's operation;
anyone else gets `not_authorized`, which the card shows as "Only the person
who added this, or an administrator, can change it." An item nobody is
recorded as selecting (a default, or an older selection) stays removable by
anyone. Operation IDs are 128 random bits, so a user cannot guess another
user's operation to watch it.

## Progress semantics

- `progressPercent` is **per item**. It may reset when the event's item changes
  (for example when a dependency or updater runs). It is never aggregate
  operation progress, and is present only when measured: a download of known
  length is; an install or removal is not. An item's detail page shows its
  current phase beside a spinner; a Home card shows no progress, only a
  pending, required or partly installed item's status ("Installed in Claude
  Code") and a failure or deferral reason. The one
  bar is in the bottom strip, for the operation being worked on, and is
  indeterminate for a phase without a percentage. With branding `progress:
  button` the bar is in each item's action button instead, and the strip only
  counts two or more running operations. The record timeline is only
  in Activity.
- `ItemCompleted` and `ItemFailed` are **non-terminal**. A dependency failure does
  not end the operation.
- Only `Succeeded`, `Failed`, `Deferred`, and `Canceled` end an operation.
- Installed/managed state is never inferred from progress. After every terminal
  record the UI calls `ListOptionalInstalls` and replaces the list and cache from
  that authoritative response. If that refresh fails, the previous data is kept
  and marked stale.
- Cancel, in the bottom strip (beside the item's button and in the strip's
  list with `progress: button`), works only until the service starts the item's
  installer or uninstaller (`Requested`, `Queued`, `Downloading`); after that it
  is disabled, and the service refuses a late request with
  `operation_not_cancelable`. A running installer is never interrupted. An
  accepted cancel reverts the request's self-service selection, skips the item in
  the run under way (aborting its download), and ends the operation with
  `Canceled` from the user. Test it with `sofcat.exe -S CancelOperation:<id>`.
- `Failed`, `Deferred`, `Canceled`, pipe unavailability, request timeout, and
  premature stream end (`stream_ended`) are shown as-is and never converted into
  success.

## Branding

The service resolves and validates the storefront's branding; the UI only asks
for it with `GetBranding` and never reads `config.yaml` or the registry itself.
It caches the last payload in `localStorage` key `sofcat.branding.v1`, applies
it at start, then fetches again. Check what the service resolved with
`sofcat.exe -S GetBranding`.

The keys, their limits and where they come from are documented on the site,
which is the only reference:

- <https://sofcat.app/customization/branding/>: title, logo, accent, banner and link button
- <https://sofcat.app/customization/layout/>: navigation, filters, item view and progress
- <https://sofcat.app/customization/catalog/>: sections, list heading and the applies-to filter
- <https://sofcat.app/customization/delivery/>: managed settings, `config.yaml` and precedence

Their sources are under `site/src/content/docs/customization/`. A new branding
key needs its entry there in the same change.

## Cache and Activity limitations

`localStorage` keys `sofcat.optional-items.v1` and `sofcat.activity.v1` hold the
last successful list (with timestamp) and locally initiated activity. A valid cache
renders immediately, then a live refresh replaces it; a failed refresh keeps the
cached data behind a non-blocking stale/service-unavailable notice with Retry in
the app bar.
Corrupt or unavailable storage is ignored and never blocks a live request.

Activity is **local display history for this UI profile only** — up to the 100 most
recent records. It is not inventory, an audit log, or cross-user history, and
restarting the app does not resume an old stream; the next authoritative list
refresh provides convergence.

## Keyboard

Everything is reachable with Tab and real buttons. A click anywhere on a card
but its buttons opens the item's detail page, as its Details button does (a
drag that selects the card's text does not); the card itself is not a control,
so keyboard and screen-reader users take Details. Back or `Escape` leaves the
detail page and focuses the card's Details button again, or the search field
when the card is no longer listed. With branding `item_view: dialog` the
card's name is its button in place of Details, and the item opens in a modal
dialog with focus on its close button; the close button, `Escape` or a click
on the dimmed list closes it and focuses the card's name again, or the search
field when the card is no longer listed. A refresh that takes the card off
the list closes the dialog the same way. After a click the dialog moves
focus without drawing a focus ring where the web view supports that (the
`focusVisible` focus option); keyboard use draws one. In a left bar the pages and, with `category_filter: list`, the categories are buttons in two `nav` landmarks, in reading order before the search field. `Ctrl+L` shows or hides Activity (like
Managed Software Center's ⌘L), and `Escape` leaves it too. All of this is
frontend-only; nothing is added to the bound surface.

## Development mock

`npm run dev` serves a browser-only mock (`frontend/src/mock-api.ts`) selected by
Vite's module alias in the `development` and `demo` modes. Every other mode
aliases the Wails adapter, so the production bundle contains no mock fixture
data and no `SOFCAT_VITE_MOCK_ONLY` marker; `src/vite-config.test.mjs` pins
that mapping.

The mock serves one of two Acme fixture stores (`frontend/src/mock-stores.ts`),
each with its own branding, sections and items. They share the mock's
behaviours: a failing install, a dependency installed in the same run, required
and managed items, pending installs and removals, custom action labels, restart
badges, a deferral, and Cancel during the download.

- `?store=apps` (the default): Acme Software Center, Windows installers in
  Productivity, Developer tools and Utilities.
- `?store=skills`: Acme AI Skills, cross-platform skills and plugins with
  `appliesTo` names and placement labels. Release Notes Writer and SQL Explainer
  have two placements, so their detail pages show a row per placement
  (installed, not installed and pending), and SQL Explainer's card reads
  "Installed in Claude Code".
- `?unbranded`: the same store's items with empty branding, the stock look.
- `?offline`: the item list fails, showing the cached list or the offline state.
- `?progress=button`: branding `progress: button` over either store, so the
  buttons carry the progress; start two or more installs to see the strip's
  count and its list.
- `?serial`: operations work one at a time and the rest stay queued, as on the
  service (the mock otherwise runs them side by side).
- `?slowlist=<ms>`: the item list answers after that long (default 250), as
  when the service queues it behind running installs.
- `?dialog`: branding `item_view: dialog` over the store's branding, so items
  open in a dialog; combine it with the others (`?store=skills&dialog`).
- `?branding=<json>`: a URL-encoded JSON object of branding fields laid over
  the store's own, for example `{"bannerAlign":"center"}`. The store keeps
  its text and link (`title`, `tagline`, `helpUrl`, `helpLabel`, `listHeading`,
  `appliesToFilterLabel`, `sections`), and anything that is not a JSON object
  is ignored.
- `?embed` (demo build only): the bare storefront, without the demo's desktop.

Each store keeps its own cache and Activity in `localStorage` under the mock, so
switching stores does not show the other store's list.

`npm run build:demo` builds the public demo (`vite build --mode demo --base
/demo/ --outDir dist-demo`) that sofcat.app serves at `/demo/`. Demo mode
starts at `src/demo-main.ts` instead of `main.ts` (a plugin in
`vite.config.ts`): the page draws a desktop with a menu bar saying the service
is simulated, with links between the stores and the unbranded view, and a
window whose frame loads the same URL with `?embed` added, where the storefront
runs as the app draws it. Below 48rem wide the desktop gives way to a plain
notice above the storefront. The landing page's own frame loads `?embed`
directly. The desktop and the mock are absent from `dist/`.

## Commands

```sh
just ui-lint    # tsc --noEmit plus the generated-binding check
just ui-test    # node --test frontend state/cache tests
just build      # frontend assets, then build/sofcat.exe, sofcat-ui.exe and sofcat-useragent.exe
```

The Windows executable is a pure-Go cross-build:

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -tags production -ldflags "-s -w -H windowsgui" -o build/sofcat-ui.exe ./ui
```

Regenerate committed bindings from `ui/` with the pinned command:

```sh
go run github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0 generate bindings -clean -ts -noevents -d frontend/bindings .
```

## Diagnostics

Diagnostics are disabled by default and create no directory or file. Set
`SOFCAT_UI_DEBUG=1` or `SOFCAT_DEBUG=1` before launch to write structured debug
records to `%LOCALAPPDATA%\sofcat\ui-client.log`. The log rotates at 10 MiB and
keeps one backup; setup or rotation failures never block startup or fail a UI
operation.

The only runtime argument is `--pipe-name`, defaulting to `sofcat-service`.

## Windows VM procedure

The real validation loop lives in the repository `AGENTS.md`: build, bootstrap the
`dialog-win11` VM with the self-serve fixtures and the real SYSTEM service, run
`run-selfserve-smoke.ps1` (must exit 0 with `SELF-SERVE SMOKE PASSED`), then launch
`launch-wails-ui.ps1` through an interactive scheduled task and screenshot the
desktop to judge Home, item progress, terminal `Failed`/`Deferred`, Activity, and
the offline cached/stale state.
