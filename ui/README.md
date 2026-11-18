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
  indeterminate for a phase without a percentage. The record timeline is only
  in Activity.
- `ItemCompleted` and `ItemFailed` are **non-terminal**. A dependency failure does
  not end the operation.
- Only `Succeeded`, `Failed`, `Deferred`, and `Canceled` end an operation.
- Installed/managed state is never inferred from progress. After every terminal
  record the UI calls `ListOptionalInstalls` and replaces the list and cache from
  that authoritative response. If that refresh fails, the previous data is kept
  and marked stale.
- Cancel, in the bottom strip, works only until the service starts the item's
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

An organisation can put its name, a tagline, a logo, a help link, a banner background image and an accent
colour on the UI with no rebuild or re-signing, and it works offline. The SYSTEM
service resolves the branding and validates it; the UI only asks for it with
`GetBranding` and never reads `config.yaml` or the registry itself. Each field
comes from the first source that sets it:

1. Managed settings: values under `HKLM\SOFTWARE\Policies\SofCat\branding` on
   Windows, or the `branding` dict in
   `/Library/Managed Preferences/<IDENTIFIER>.plist` on macOS. A rebranded build
   reads `HKLM\SOFTWARE\Policies\<Name>\branding` instead, where `<Name>` is its
   `pkg/identity` name, so profiles for one build never brand another. The value
   names are the `config.yaml` keys; the full key list is at
   <https://sofcat.app/configuration/#managed-settings>.
2. The `branding:` block in `config.yaml` (see `examples/example_config.yaml`).
3. Nothing. The UI then looks exactly as it does unbranded.

| Key (policy value and `config.yaml`) | Accepted |
|---|---|
| `title` | Plain text, up to 120 characters. Also the window title and the app-bar name. |
| `tagline` | Plain text, up to 240 characters. |
| `logo` | Local path to a PNG, JPEG or SVG of at most 512 KiB, checked by content. A PNG also becomes the window, taskbar and Alt+Tab icon. |
| `link_url` | Absolute `http` or `https` URL. A button opens it in the system browser; without a valid one there is no button. `help_url`, its earlier name, is still accepted: where one source (the policy, or `config.yaml`) sets both, `link_url` wins; across sources the policy wins as for every key, whichever name it uses. |
| `link_label` | The button's text: plain text, up to 60 characters. Defaults to "Get help". `help_label`, its old name, is still accepted the same way. |
| `link_placement` | `banner`, the default: the button in the banner, placed by `link_position`; a link alone brings the banner up, with just the button. `app_bar`: the button at the app bar's right end, an outlined pill before the connection status, the two wrapping together in a narrow window; a long label wraps inside the pill. There a link alone does not bring the banner up. Any other value is `banner`. |
| `link_position` | Where in the banner the button sits; ignored with `link_placement: app_bar`. `auto`, the default: as `banner_align` lays it out, at the right edge for `left`, the left edge for `right`, under the tagline for `center`, in the banner's vertical middle. `top_left`, `top_right`, `bottom_left`, `bottom_right`: that corner, inside the banner's padding, never over the content. A corner on the content's side, or any corner of a centred banner, gives the button a row of its own above or below the content, with as much room left empty on the other side, so the content stays in the middle and the banner grows when its height leaves no room; on a centred `large` or `x-large` banner the extra height those add moves from the padding into these rows, so the button stays in its corner. A corner on the far side (`top_right` or `bottom_right` with `banner_align: left`, the left corners with `right`) gives it a column of its own instead, up to 40% of the width, and the banner keeps its height; below 40rem (640pt) wide it takes a row there too. Screen readers and the keyboard meet the button after the title and tagline in every position. Any other value is `auto`. |
| `accent` | `#rrggbb`. Recolours buttons, links and the banner, and on Windows 11 the native title bar (Windows 10 keeps its default caption). |
| `background_image` | Local path to a PNG, JPEG or SVG of at most 1 MiB, checked by content. Fills the banner in place of the accent, cropped to cover it. Buttons and links keep the accent. Pick a wide image with no text near the edges. |
| `background_text` | `light` (the default) or `dark`. The colour of the banner's title, tagline and link button over `background_image`. sofcat draws nothing over the image, so pick the one that reads on it. |
| `list_heading` | Plain text, up to 120 characters. Replaces the "Available software" heading; "My items" is unchanged. |
| `title_size` | `small`, `medium` (the default), `large` or `x-large`. The size of the banner title and, in step with it, the "Available software" (or `list_heading`), "My items" and "Activity" headings: 0.85, 1, 1.25 or 1.5 times the default. Any other value keeps the default. |
| `tagline_size` | The same four sizes for the banner tagline. |
| `content_width` | How much of the window the lists, My items, Activity and the detail page use. `wide`, the default, centres them in a column of up to 96rem (1536pt) and lets the cards of a short row share its width: a row of one or two cards shows each as a single line, glyph, name and buttons. `full` drops the column's cap, so the content runs to the window's edges on any screen. `compact` is the layout from before this key: a column of up to 76rem (1216pt) whose cards keep one column's width, leaving the rest of a short row empty. Any other value is `wide`. The banner, app bar and operation strip span the window in all three. |
| `filter_placement` | Where the search field and the category list sit above a list and My items. `right`, the default: on the heading's row, at its right edge; when the row is too narrow for both, they move under the heading. `left`: above the heading, at the left, as before this key. In a window narrower than 40rem (640pt) both values stretch the two controls to the full width, one under the other: under the heading for `right`, above it for `left`. Any other value is `right`. Either way the keyboard reaches the search field, then the category list, then the cards. |
| `banner_style` | `text`, the default: the logo, if any, in a small white tile beside the title. `logo`: the logo in the title's place, as a wordmark with no tile behind it. It is at most 3.5rem (56pt) tall, times the `title_size` factor, keeps its shape and shrinks to fit a narrow window; the tagline and link button stay. `title` is still the window and product name and becomes the logo's text alternative, so set it to the words the logo shows; without a title the product name is. The logo stays out of the app-bar mark and the window icon. Without a usable `logo`, or when the logo's data does not decode, it is `text`. Any other value is `text`. Leave `<title>` and `aria-label` out of an SVG logo: the macOS web view reads them to screen readers in place of the text alternative, in either style. `examples/branding/` has a stock wordmark in light lettering (for the accent or a dark `background_image`) and dark lettering (for a light one, with `background_text: dark`). |
| `banner_align` | `left` (the default), `center` or `right`: where the banner's logo, title and tagline sit, in either style. `right` mirrors `left`, with the link button at the banner's left edge; `center` stacks logo, title, tagline and link button in a centred column. Any other value is `left`. |
| `banner_height` | The banner's minimum height. `small`: a slim strip, with 1rem of padding above and below, tighter gaps when the content wraps or is centred, a 2.5rem logo tile and a `banner_style: logo` wordmark of 2.5rem times the `title_size` factor; over a `background_image` it drops the 7.5rem minimum and hugs the content too. `medium`, the default: the content's own height, at least 7.5rem (120pt) over a `background_image`. `large`: at least 11rem (176pt). `x-large`: at least 16rem (256pt), room to show more of a background image. Each is a minimum: content that needs more room, a large `title_size` or a title that wraps in a narrow window, grows the banner, and a banner whose content is already taller than the minimum is unchanged. The exception is `banner_align: center`, which stacks logo, text and link button in a column often taller than 11rem: there `large` and `x-large` also add padding (3rem and 4.5rem above and below), so each step is taller than the one below. The content stays in the banner's middle. Over a background image a taller banner shows more of the picture's height, still centred and cropped to cover. Any other value is `medium`. |
| `app_bar_brand` | What the app bar shows of the product beside the tabs: `full` (the default) the mark and the name, `mark` the mark only, `none` neither. Meant for a banner that already names the organisation: while no banner shows, it is `full`. Screen readers still get the name, the page's heading, in all three. With `none` and a banner that shows neither a title nor a logo, no name is visible anywhere. Any other value is `full`. |
| `item_view` | How a card opens its item. `page`, the default: the item's detail page replaces the list, with Back, and each card has a Details button. `dialog`: the same details in a dialog over the dimmed list, which grows out of the card and shrinks back into it (it just appears and disappears when the card is not wholly on screen, over a backdrop that still fades in; under reduced motion neither moves); its close button, `Escape` or a click outside it closes it. The cards have no Details button: the item's name is the button that opens its dialog. The list behind the dialog and the dialog itself keep updating. The operation strip stays at the window's bottom, dimmed and out of reach like the rest of the list (a click on it closes the dialog), so while an operation on the open item runs the dialog shows a Cancel just before its action, which follows the strip's rules. Any other value is `page`. |
| `sections` | A list; a REG_MULTI_SZ as policy. Up to 50 entries of plain text, up to 120 characters each. Sets the storefront tab order: the sections listed come first, then the rest alphabetically. Items without a catalog `section` appear under a "Software" tab, so give every item a section to hide it. |

Set the policy values with Intune (a custom OMA-URI or a registry script), Group
Policy Preferences, or `reg add`:

```bat
reg add "HKLM\SOFTWARE\Policies\SofCat\branding" /v title /t REG_SZ /d "Acme Software Center" /f
reg add "HKLM\SOFTWARE\Policies\SofCat\branding" /v accent /t REG_SZ /d "#0b6e4f" /f
```

The service reads managed settings at the start of every run, so a policy change
shows after the next run. A `config.yaml` change needs a service restart. A value that fails its check is
dropped (debug log only) and is not replaced by the config value. Check the
result with `sofcat.exe -S GetBranding`, which prints the payload with the logo
replaced by its size in bytes. The UI caches the last payload in
`localStorage` key `sofcat.branding.v1`, applies it at start, then fetches
again.

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
`focusVisible` focus option); keyboard use draws one. `Ctrl+L` shows or hides Activity (like
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
- `?dialog`: branding `item_view: dialog` over the store's branding, so items
  open in a dialog; combine it with the others (`?store=skills&dialog`).

Each store keeps its own cache and Activity in `localStorage` under the mock, so
switching stores does not show the other store's list.

`npm run build:demo` builds the public demo (`vite build --mode demo --base
/demo/ --outDir dist-demo`) that sofcat.app serves at `/demo/`. Demo mode adds
a bar above the shell saying the service is simulated, with links between the
stores and the unbranded view; the bar and the mock are absent from `dist/`.

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
