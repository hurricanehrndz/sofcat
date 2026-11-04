# SofCat UI Architecture

## Boundary

The Wails process runs as the interactive standard user. The SYSTEM SofCat service remains the authorization and filesystem boundary. UI code receives no SofCat configuration, credentials, arbitrary paths, package-server settings, or internal catalog objects.

Any local user can reach the service, so it authorizes each mutation by item, not by caller. `installItem` accepts only an item offered in the admin manifests' `optional_installs`. `removeItem` accepts an item offered there or in `default_installs`, or one already in the self-serve manifest's own lists, so an item stays removable after it leaves the offer. An item an admin manifest lists in `managed_installs` is never removable, even when it is also offered: the service refuses with `item_not_removable`, and the managed run also skips a self-serve uninstall of such an item. `listOptionalInstalls` marks it `isRequired`, and the UI shows it as "Managed by your organisation" with Details but no Install or Remove.

The self-serve manifest is per machine, as in Munki, so on a shared machine (fast user switching, Remote Desktop Services) every user sees one self-service selection. The service also authorizes by caller, so one user cannot undo another's request. It records who selected each item in the self-serve manifest's `selected_by` (item name to SID on Windows, uid elsewhere), from the connection, when `installItem` selects an item that has none recorded; a second user selecting the same item succeeds and leaves the record alone. `removeItem`, and an `installItem` for an item waiting for removal (which takes the removal back), are accepted only from that selector or a local administrator; `cancelOperation` only from the user who requested the operation or an administrator. Anyone else gets `not_authorized`, before anything changes. An administrator is a caller whose pipe client token has `BUILTIN\Administrators` enabled (a UAC-filtered token, where the group is deny-only, is not one); on macOS uid 0 or a member of group `admin`. A caller the service cannot resolve is never an administrator and matches no recorded user. An item with no recorded selector (a `default_installs` item, or one selected before selectors were recorded) stays removable by anyone. The selector is cleared when the item's removal completes, in the same file update that prunes its `managed_uninstalls` entry, so the next user to select it becomes its selector; a cancel that puts back the selection also puts back the selector it had. Streaming is not owned: anyone who knows an operation ID can stream it. IDs are therefore 128 random bits from `crypto/rand`, hex encoded, which no other user can guess.

`UIService` binds exactly six calls backed by the shared `pkg/service.Client`:

1. `ListOptionalInstalls`
2. `InstallItem`
3. `RemoveItem`
4. `WatchOperation`
5. `GetBranding`
6. `CancelOperation`

`WatchOperation` uses Wails' application-lifetime context and emits typed records only on `sofcat:operation-status`. The frontend imports generated calls through `frontend/src/wails-api.ts`.

## Protocol

This section is the contract between the service and its clients (`ui`
and `sofcat -S`, both through `pkg/service.Client`).

The service speaks [JSON-RPC 2.0](https://www.jsonrpc.org/specification): one
JSON object per line, one request per connection. On Windows the transport is
the named pipe `\\.\pipe\sofcat-service`, owned by the service's user
(LocalSystem), with a protected DACL: SYSTEM and Administrators full,
Authenticated Users read and write but not `FILE_CREATE_PIPE_INSTANCE`, so no
user can add a server instance. Remote clients are rejected. The service
creates the first instance with `FILE_FLAG_FIRST_PIPE_INSTANCE` and always
keeps one listening, so if another process holds the name the service fails to
start ("pipe ... already exists") rather than serve beside it. Before sending
anything a client checks that the pipe's owner is LocalSystem, which only a
SYSTEM process can set, and refuses the pipe otherwise; it also connects at
identification level, so the server can tell who it is but never act as it.
(The owner stands in for the server process's token, which a standard user
cannot open.) Elsewhere it is the Unix socket
`/var/run/<slug>/<slug>-service.sock` (macOS, the launchd daemon's) or
`/run/<slug>/...` (Linux, where only tests run the service), mode 0666 in a
0755 directory, and a client refuses a socket whose peer credentials are not
root's.

A request has a string `id`, a camelCase `method` and an object `params`. The
answer echoes the `id` with a `result` or an `error`:

```json
{"jsonrpc":"2.0","id":"1759580000000000000","method":"installItem","params":{"itemName":"GoogleChrome"}}
{"jsonrpc":"2.0","id":"1759580000000000000","result":{"operationId":"9f2c4e7a1b3d5f60718293a4b5c6d7e8","accepted":true,"queuedAtUtc":"2026-10-04T12:00:00.123Z","requestedBy":"PC01\\alice"}}
```

| Method | Params | Result |
| --- | --- | --- |
| `getServiceInfo` | none | `{version, protocolVersion: "2.0", apiVersion: 1, capabilities: [methods], busy, uptimeSeconds}` |
| `listOptionalInstalls` | none | `{items: [OptionalInstallItem]}` (shape below) |
| `getBranding` | none | `Branding`: `{title, tagline, helpUrl, helpLabel, accent, logoMime, logoBase64, listHeading}` |
| `installItem` | `{itemName}` | `AcceptedOperation` |
| `removeItem` | `{itemName}` | `AcceptedOperation` |
| `cancelOperation` | `{operationId}` | `{canceled: true}` |
| `streamOperationStatus` | `{operationId}` | `{streamAccepted: true}`, then notifications |
| `listUserPlacements` | none | `{entries: [{item, version, placement, path, archive, hash, treeHash, held}]}` |
| `reportUserPlacements` | `{results: [{item, placement, outcome, error?}]}` | `{}` |

`getServiceInfo`, `getBranding`, the two mutations and `cancelOperation` skip the
command queue and answer while a managed run is under way;
`listOptionalInstalls` waits behind it. `apiVersion` changes only for a breaking
change to these methods; additions show up in `capabilities`.

An `OptionalInstallItem` is `{itemName, displayName, version, catalog,
description?, category?, developer?, iconName?, restartAction?, isManaged,
isRequired, isInstalled, status, statusUpdatedAtUtc, lastOperationId?,
actionLabels?, worksIn?}`. `actionLabels` holds only the label keys
(`install`, `installing`, `installed`, `remove`, `removing`, `update`,
`cancel`, `pending_install`, `pending_remove`) that resolved: from the catalog item's `custom_action_labels`, else
from the first of its placements with a variant on this OS, in
`harnesses.yaml` order, whose `action_labels` sets the key. `worksIn` is the
union of those placements' `works_in`, in table order, without repeats. Both
are left out when empty, and only zip items have placements, so another item
gets at most its own labels. The UI fills in its built-in labels ("Install",
"Installing…", "Installed", "Remove", "Removing…", "Update", "Cancel",
"Will be installed", "Will be removed") for the keys left out:

```json
{"itemName":"tapas-optional-plugin","displayName":"Tapas Optional Plugin","version":"1.0.0","catalog":"tapas","isManaged":false,"isRequired":false,"isInstalled":false,"status":"NotInstalled","statusUpdatedAtUtc":"2026-11-03T12:00:00Z","actionLabels":{"install":"Enable for me","installed":"Available","installing":"Making available…","remove":"Withdraw"},"worksIn":["Claude Code","Codex"]}
```

The two user-placement methods serve the per-user helper, `<slug>-ui.exe
--user-sync`, and are not bound into Wails. SYSTEM never writes into a user
profile: for zip variants whose placement scope on Windows is `user`, a run
that is not check-only stages each verified archive as `<hash>.zip` in
`user-artifacts` inside the data directory
(`${ProgramData}\<Name>\Data\user-artifacts`, which administrators write and
users read), removes archives no plan entry references, and then stores the
plan in the data directory as `user-plan.json` (`{entries: [...]}`, written
atomically). The plan is that run's desired set (installs and updates with
their dependencies and `update_for` riders, the set scope exit keeps)
filtered to those variants, the same for every caller. `listUserPlacements`
skips the queue and returns the last run's stored plan without reading the
repository, so it answers at logon even while the repository is unreachable;
a helper that runs before the first run gets an empty plan. `path` is the
unexpanded placement root, `archive` the staged copy, and `held` marks an
item under an undecided `conditional_items` block, which the helper neither
places nor removes. The helper, run at logon by the HKLM `Run` value
`<slug>.exe -serviceinstall` writes
and in the background whenever the storefront starts, retries a service it
cannot connect to every 5 seconds for up to a minute (without a plan it
removes nothing), expands `${USERPROFILE}` itself, checks the path against
the build's `windows_user` roots, re-verifies `hash`, places the tree with
receipts in `%LOCALAPPDATA%\<Name>\receipts`, removes its own
receipt-tracked trees that left the plan, and sends every result with
`reportUserPlacements`. The service only logs them (failures at warn, with
the caller from the connection). A logged-on user gets new content at the
next logon or storefront start.

`streamOperationStatus` answers with its result and then sends every record of
the operation, from the first, as `operationStatus` notifications on the same
connection, until a terminal record, and closes:

```json
{"jsonrpc":"2.0","method":"operationStatus","params":{"operationId":"9f2c4e7a1b3d5f60718293a4b5c6d7e8","seq":2,"timestampUtc":"2026-10-04T12:00:01.480Z","itemName":"GoogleChrome","displayName":"Google Chrome","state":"Downloading","progressPercent":40,"message":"Google Chrome","requestedBy":"PC01\\alice"}}
```

`seq` numbers an operation's records from 1 in the order the service recorded
them, and `timestampUtc` is RFC 3339 UTC with milliseconds. Records can reach
the frontend out of order (Wails emits each event on its own goroutine), so the
frontend files them by `seq`, and Activity by time and then `seq`.

Every local user can read what the service sends, so messages name items, not
locations. A `Downloading` record's message is the item's display name; the
package URL, which can carry a signed-query token, goes only to the debug log.
`command_failed` and a `managed_run_failed` record say to see the service log
instead of quoting the error, which can name repository URLs and local paths.
An `item_failed` record still quotes the installer's error (usually `exit
status N`).

`requestedBy` names the user who called `installItem` or `removeItem`. The
service reads it from the connection, never from the request: on Windows from
the token of the pipe client's process (`DOMAIN\user`; the SID goes to the
log), elsewhere from the socket's peer credentials (the user name). It is in
the `AcceptedOperation`, on every record of the operation, in the service log
at info level with the operation ID, and in `inventory.json` as `requested_by`
on the item, for the run the operation triggered. When the service cannot
resolve the user it records `""` and logs why at debug. The name is an audit
record; authorization (see Boundary) uses the SID or uid, and refuses an
unresolved caller only where a recorded selector or requester must match.

Errors use the standard codes for malformed requests and the server range for
application errors. `data.code` is the stable string clients branch on, and
`data.operationId` names the operation when there is one:

```json
{"jsonrpc":"2.0","id":"1759580000000000001","error":{"code":-32002,"message":"operation can no longer be canceled: work on Google Chrome has already started","data":{"code":"operation_not_cancelable","operationId":"9f2c4e7a1b3d5f60718293a4b5c6d7e8"}}}
```

| Code | `data.code` | When |
| --- | --- | --- |
| -32700 | `parse_error` | the line is not JSON |
| -32600 | `invalid_request` | not a JSON-RPC 2.0 request object (batches included), or a request over 64 KiB (`id` null) |
| -32601 | `method_not_found` | unknown method |
| -32602 | `invalid_params` | a required param is missing or of the wrong type |
| -32603 | `internal_error` | the service panicked while handling the request |
| -32000 | `server_busy` | every handler slot is taken (sent before reading the request, so `id` is null), or every stream slot |
| -32001 | `command_failed` | the command failed (for example the manifest fetch) |
| -32002 | `operation_not_cancelable` | cancel refused: the item was acted on, the operation finished, or it is unknown |
| -32003 | `unknown_operation` | stream of an operation the service does not track |
| -32004 | `item_not_available` | `installItem` for an item not offered for self-service |
| -32005 | `item_not_removable` | `removeItem` for an item that is not a self-service item, or that an admin manifest requires |
| -32006 | `not_authorized` | `removeItem`, `cancelOperation`, or an `installItem` that would take back a pending removal, from a caller who is neither the item's selector (the operation's requester, for a cancel) nor a local administrator |

The service does not support batches, and it does not act on a request without
an `id` (a notification): it closes the connection without a reply. The
`errorCode` inside a status record (`managed_run_failed`, `item_failed`,
`placement_conflict`, `placement_refused`, `blocked_by_running_app`,
`missing_software`) describes the operation's outcome, not the call. The two
placement codes are zip-item failures: a placed tree edited outside sofcat, and
a placement sofcat may not write ([docs/zip-items.md](../docs/zip-items.md)).
`missing_software` is a deferral because software the item requires is not
installed; the message is the deferral reason, which names that software.

The service bounds what one client can hold. It handles 32 connections at a
time, at most 16 of them streams; a client beyond either limit gets
`server_busy`. A client has 5 seconds to send its request line, after which the
service hangs up without an answer, and the line may be at most 64 KiB, or the
service answers `invalid_request` and hangs up. Each response and notification
must be taken within 30 seconds, so a client that stops reading a stream loses
it. On Windows the pipe handles are synchronous, so these deadlines cancel the
blocked read or write with `CancelIoEx`; on Unix they are socket deadlines.

The transport is the only per-platform part of `pkg/service`:
`transport_windows.go` and `transport_unix.go` each provide `listen` and `dial`.
The protocol, dispatch, command queue, operation tracking, streaming and cancel
live in portable files, so `go test ./pkg/service` runs the service over a real
Unix socket on Linux and macOS. Installing and controlling the service stays
Windows-only.

## Progress state machine

`pkg/service` attaches an operation-scoped `installer.ProgressFn` to the run's
`installer.Runner.Emit`, so every record names a real item. The run reconciles
every item, and a record goes to each open operation it concerns: the
operation's requested item and the items its install or removal brings along,
dependencies (the walk lists them in `installer.Runner.PulledBy`) and updaters
(`update_for` the item). So an item another request is still waiting for shows
progress as soon as any run works on it. A record no open operation asked for,
such as a failed install retried every run, goes only to the service log. Runner states map to
record states `Downloading`, `Installing`, `Removing`, `ItemCompleted`, and
`ItemFailed`; all five are non-terminal. Only `Succeeded`, `Failed`, `Deferred`,
and `Canceled` — from the requested item's real run report, a service
cancellation or error, or a user cancel — end an operation.

`progressPercent` is scoped to the record's `itemName` and may reset at an item
boundary. An item's detail page shows one status line with a spinner while its
operation runs, naming the record's item when a dependency or updater takes
over. A Home card shows no progress, and a status only where its button
cannot say it: a pending item's (its button reads only Cancel) and a required
item's (it has no button). Otherwise the button names the state, and is
disabled while the item's operation runs; the card's line also appears for a
`Failed` or `Deferred` outcome or a refused request. The only bar is in the
bottom `#operation-strip`, which reports one running operation — the earliest
the service has started work on — as "n of N" with a `<progress max="100">`
that is determinate only when the latest record carries a percentage. The
percentage is still the record's item's, not an aggregate. The record timeline is shown only in Activity, never on the Home view. No
percentage is fabricated for status checks, no-action items, blocking-app checks,
or pre/post scripts.

The app bar carries the connection state (a dot and the cached/stale message
with Retry). Above it, the `#banner` section stays hidden until branding
fills it (see Branding). "My items" is the same list filtered on the client to installed or
managed items; it is not a separate service call. The Software view's heading
is the branding `listHeading`, or "Available software"; "My items" keeps its
name.

Buttons and status text use the item's labels: `install` on the install
button, `remove` on the remove button, `cancel` on the Cancel of a pending
request and on the strip's Cancel, `installing` and `removing` while the
item's own install or removal runs, `installed` as an installed item's status
on the detail page, and `pending_install` and `pending_remove` as a pending
item's status on its card and detail page. Nothing shows an update action yet,
so `update` is accepted but unused. A card and the detail page add "Works in: Claude Code, Codex" when
`worksIn` is set. Labels, the heading and the works-in line are catalog or
admin text and go into the DOM only as `textContent`. A `Deferred` card shows
the deferral reason: for `missing_software` the service's message ("Needs Git
for Windows. Install it first, then try again."), for
`blocked_by_running_app` "Waiting: close <apps> to continue" (also matched on
the message, for services that send no code).

## Cancel

The strip has a Cancel button for the operation it shows. It is enabled only
while that operation's latest record is `Requested`, `Queued` or `Downloading`,
and otherwise is a real disabled button whose title says why. `CancelOperation`
skips the command queue, since the run it would stop holds that queue. The
service accepts it only while the operation is open and no run has started the
item's install or uninstall command; a running installer is never interrupted.
Anything else (item acted on, operation finished, unknown ID) is refused with
`operation_not_cancelable`, and the strip (and the detail page) shows the
service's message without changing the item. A cancel from anyone but the
operation's requester or a local administrator is refused with
`not_authorized`, and the strip says only that person or an administrator can
change the item.

An accepted cancel does three things. It puts back the self-serve selection the
`InstallItem` or `RemoveItem` replaced, so a later scheduled run does not
perform the request anyway. It withdraws the item from the run under way
(`installer.Cancels`): the run skips it before its download and again before
its command, and an in-flight download is aborted; other items carry on. And it
ends the operation with `Canceled` and `canceledBy: "user"`. The card then
reads "Canceled" as plain text until the next action, and Activity reads
"Canceled by you".

`InstallItem` and `RemoveItem` do not wait for the command queue either: they
write the selection, register the operation as `Queued` and answer at once, and
only the run they schedule is queued. A mutation that lands mid-run leaves that
run on the plan it loaded; the queued run picks up the newer selection. All
self-serve manifest writes, the run's included, go through
`manifest.UpdateSelfServe`, an in-process lock with a fresh load.

Installed and managed state come only from an authoritative `ListOptionalInstalls`
refresh performed after a terminal record, never from progress records. A failed
refresh keeps the prior data and marks it stale.

## Branding

Branding is admin configuration, not catalog data. The SYSTEM service resolves
it on each `GetBranding` call, field by field, first match wins:

1. REG_SZ values under `HKLM\SOFTWARE\Policies\SofCat\Branding` (`Title`,
   `Tagline`, `LogoPath`, `HelpUrl`, `HelpLabel`, `Accent`, `ListHeading`), for
   MDM and GPO.
2. The `branding:` block in `config.yaml`.
3. Nothing, and the UI keeps its default look.

`pkg/branding` validates after the merge, so a bad policy value is dropped rather
than replaced by the config value. Text is trimmed, stripped of control
characters and capped (title 120, tagline 240, help label 60, list heading 120
runes). The help
URL must be absolute http or https. The accent must match `#rrggbb`. The logo is
read from its local path on every call, capped at 512 KiB, and kept only if its
bytes sniff as PNG, JPEG or SVG. A dropped field is logged at debug level and
never fails the call.

`GetBranding` skips the service command queue, because the UI calls it once
before creating its window to pick the window title, icon and caption colour
and must not wait behind a managed run. That startup call gives up after two
seconds and falls back to "SofCat UI", the embedded sofcat icon and the system
caption. The caption colour goes through the Windows 11 DWM caption attributes
(`CustomTheme`), so the native title bar stays and only its colours follow the
accent; the window is not frameless. The frontend then applies the cached payload from
`sofcat.branding.v1`, fetches a fresh one and reapplies it. The banner shows
when a title, tagline, logo or help URL is set. The logo is an `<img>` from a
`data:` URL, so an SVG cannot run script. The help button opens its URL in the
system browser through the Wails Browser API after the frontend checks the
scheme again. The UI never reads `config.yaml` or the registry itself.

## Frontend state

The frontend is vanilla TypeScript with one adapter (`src/api.ts`) whose
implementation Vite selects by mode: `wails-api.ts` for production, `mock-api.ts`
for `npm run dev`. The production bundle therefore contains no mock fixture data
or `SOFCAT_VITE_MOCK_ONLY` marker.

`localStorage` keys `sofcat.optional-items.v1` and `sofcat.activity.v1` provide
cache-first startup and up to 100 locally initiated activity records. Activity is
display history for this UI profile, not inventory, audit, or cross-user history;
a restart does not resume a stream. Catalog strings are written with `textContent`
only, and no catalog-provided URL is opened in the WebView.

## Assets and build

Production builds embed `frontend/dist` and expose `index.html` at the bundled asset root. Development Go builds use a compile-safe source filesystem, so Go tests do not require committed Vite output.

The build order is `npm ci`, Vite production assets, then a pure-Go Windows build with the `production` tag. The root module pins Wails v3; no nested Go module or alternate pipe client exists.

## Diagnostics

Structured `slog` diagnostics are opt-in through `SOFCAT_UI_DEBUG=1` or `SOFCAT_DEBUG=1`. Enabled logs use the existing lumberjack dependency at `%LOCALAPPDATA%\sofcat\ui-client.log`, 10 MiB with one backup. Lifecycle and status-event records are debug-level and include available operation, operation ID, state, result, and duration fields. Disabled diagnostics create no directory or file, and logging failures do not affect UI operations.

## Deliberate ceilings

- Progress is per item; there is no weighted or aggregate operation model. Upgrade
  only when the engine exposes a real work graph.
- Activity keeps 100 local display records and is not an audit trail. Upgrade only
  when an authoritative history operation exists.
- A closed app does not resume a stream; the next list refresh converges state.
- Icons are generated locally (monogram or category glyph); no remote icon
  transport exists.
- `OperationStatus` is hand-typed in `frontend/src/api.ts` because bindings are
  generated with `-noevents`, so the binding-diff gate cannot catch drift in it.
  Upgrade only when bindings are generated with events.
