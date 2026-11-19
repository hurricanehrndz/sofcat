# SofCat UI Architecture

## Boundary

The Wails process runs as the interactive standard user. The SYSTEM SofCat service remains the authorization and filesystem boundary. UI code receives no SofCat configuration, credentials, arbitrary paths, package-server settings, or internal catalog objects.

Any local user can reach the service, so it authorizes each mutation by item, not by caller. `installItem` accepts only an item offered in the admin manifests' `optional_installs`. `removeItem` accepts an item offered there or in `default_installs`, or one already in the self-serve manifest's own lists, so an item stays removable after it leaves the offer. An item an admin manifest lists in `managed_installs` is never removable, even when it is also offered: the service refuses with `item_not_removable`, and the managed run also skips a self-serve uninstall of such an item. `listOptionalInstalls` marks it `isRequired`, and the UI shows it as "Managed by your organisation" with Details but no Install or Remove.

The self-serve manifest is per machine, as in Munki, so on a shared machine (fast user switching, Remote Desktop Services) every user sees one self-service selection. The service also authorizes by caller, so one user cannot undo another's request. It records who selected each item in the self-serve manifest's `selected_by` (item name to SID on Windows, uid elsewhere), from the connection, when `installItem` selects an item that has none recorded; a second user selecting the same item succeeds and leaves the record alone. `removeItem`, and an `installItem` for an item waiting for removal (which takes the removal back), are accepted only from that selector or a local administrator; `cancelOperation` only from the user who requested the operation or an administrator. Anyone else gets `not_authorized`, before anything changes. An administrator is a caller whose pipe client token has `BUILTIN\Administrators` enabled (a UAC-filtered token, where the group is deny-only, is not one); on macOS uid 0 or a member of group `admin`. A caller the service cannot resolve is never an administrator and matches no recorded user. An item with no recorded selector (a `default_installs` item, or one selected before selectors were recorded) stays removable by anyone. The selector is cleared when the item's removal completes, in the same file update that prunes its `managed_uninstalls` entry, so the next user to select it becomes its selector; a cancel that puts back the selection also puts back the selector it had. Streaming is not owned: anyone who knows an operation ID can stream it. IDs are therefore 128 random bits from `crypto/rand`, hex encoded, which no other user can guess.

`UIService` binds exactly seven calls backed by the shared `pkg/service.Client`:

1. `ListOptionalInstalls`
2. `InstallItem`
3. `RemoveItem`
4. `WatchOperation`
5. `GetBranding`
6. `CancelOperation`
7. `GetItemIcon`

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
| `getBranding` | none | `Branding`: `{title, tagline, helpUrl, helpLabel, linkPlacement, linkPosition, accent, logoMime, logoBase64, backgroundMime, backgroundBase64, backgroundText, listHeading, appliesToFilterLabel, titleSize, taglineSize, contentWidth, filterPlacement, navPlacement, categoryFilter, appBarBrand, bannerStyle, bannerAlign, bannerHeight, itemView, progress, refreshIntervalSeconds, sections?}` |
| `getItemIcon` | `{iconName}` | `{mime, base64}`, both empty when the service holds no icon by that name |
| `installItem` | `{itemName, placement?}` | `AcceptedOperation` |
| `removeItem` | `{itemName, placement?}` | `AcceptedOperation` |
| `cancelOperation` | `{operationId}` | `{canceled: true}` |
| `streamOperationStatus` | `{operationId}` | `{streamAccepted: true}`, then notifications |
| `run` | none | `AcceptedOperation` without `operationId`, then one `runResult` notification |
| `listUserPlacements` | none | `{entries: [{item, version, placement, path, archive, hash, treeHash, held, managed}], facts: {...}, placements: {name: spec}}` |
| `reportUserPlacements` | `{results: [{item, placement, outcome, error?}]}` | `{}` |

`getServiceInfo`, `getBranding`, `getItemIcon`, the two mutations and `cancelOperation` skip the
command queue and answer while a managed run is under way;
`listOptionalInstalls` and the run that `run` asks for wait behind it. `apiVersion` changes only for a breaking
change to these methods; additions show up in `capabilities`.

Every managed run, check-only included, holds the file lock `run.lock` in the
data directory, so the service's run and a command-line `sofcat` run never
overlap. The service waits for the lock, and so does `sofcat -checkonly`, which
says so on stderr and still prints its inventory JSON on stdout. It waits with
no limit, and a run can be long, so a monitoring wrapper should apply its own
timeout. Any other
command-line run fails at once with "another managed run is in progress".

An `OptionalInstallItem` is `{itemName, displayName, version, catalog,
description?, category?, section?, developer?, iconName?, iconHash?, restartAction?, isManaged,
isRequired, isInstalled, status, statusUpdatedAtUtc, lastOperationId?,
actionLabels?, appliesTo?, placements?}`. `iconHash` is the sha256 of the icon the
service holds for `iconName`, and is absent when it holds none. Each list call
fills the service's icon cache (`icons` in its data directory) from the
repository's `icons/<icon_name>`: it fetches an icon that is missing, that is
not the one the catalog's `icon_hash` names or that, without an `icon_hash`,
is a day old, keeps only a PNG or JPEG of at most 512 KiB, and keeps the copy
it has when a fetch fails. `getItemIcon` reads that cache and never the
network. The UI asks for an item's icon only when it has an `iconHash`, one
call at a time, keeps the images for the session keyed by name and hash, and
shows the monogram until one arrives. `actionLabels` holds only the label keys
(`install`, `installing`, `installed`, `remove`, `removing`, `update`,
`cancel`, `pending_install`, `pending_remove`, `applies_to`) that resolved: from the catalog item's `custom_action_labels`, else
from the first of its placements with a variant on this OS, in
`placements.yaml` order, whose `action_labels` sets the key. `appliesTo` is the
union of those placements' `applies_to`, in table order, without repeats. Both
are left out when empty, and only zip items have placements, so another item
gets at most its own labels. The UI fills in its built-in labels ("Install",
"Installing…", "Installed", "Remove", "Removing…", "Update", "Cancel",
"Will be installed", "Will be removed", "Applies to") for the keys left out:

```json
{"itemName":"tapas-optional-plugin","displayName":"Tapas Optional Plugin","version":"1.0.0","catalog":"tapas","isManaged":false,"isRequired":false,"isInstalled":false,"status":"NotInstalled","statusUpdatedAtUtc":"2026-11-03T12:00:00Z","actionLabels":{"install":"Enable for me","installed":"Available","installing":"Making available…","remove":"Withdraw","applies_to":"Works in"},"appliesTo":["Claude Code","Codex"]}
```

`placements` is set only for a zip item with two or more placements on this
OS: one `{name, label, isInstalled, status}` per placement, in
`placements.yaml` order. `label` is the placement's `applies_to` joined with
", ", else its name. `status` is `Installed`, `NotInstalled`,
`WillBeInstalled` or `WillBeRemoved`, worked out as the item's, except that an
installed placement the user took away from a selected item reads
`WillBeRemoved`. A user-scope placement is installed when the caller's user
agent last reported it placed.

```json
"placements":[{"name":"claude-skills","label":"Claude Code","isInstalled":true,"status":"Installed"},{"name":"codex-skills","label":"Codex","isInstalled":false,"status":"NotInstalled"}]
```

`installItem` and `removeItem` take an optional `placement`, one of the item's
placements on this OS, to add or remove the item there only. Without it they
act on every placement, as before. The service records a choice in the
self-serve manifest's `placement_selections` (item name to its placements,
sorted); an item without an entry goes to every placement.

- `installItem` without `placement` selects the item and clears its entry.
  With one, an item not selected (or waiting for removal) is selected for that
  placement only; one already narrowed gains it; one selected everywhere is
  left as it is.
- `removeItem` without `placement` removes the item and clears its entry. With
  one, it takes that placement from the item's placements (every placement,
  when it has no entry) and leaves the item selected for the rest. When none
  is left, or the item is not selected, it is a plain removal.
- An entry that comes to cover every placement the item has is cleared.
- Taking a placement away is a removal for authorization: only the item's
  selector or an administrator may do it, and never for an item an admin
  manifest requires.
- A placement the item does not have on this device, or one named for an item
  that is not a zip item, is refused with `invalid_params` and nothing changes.
- A cancel puts back the item's entry with the rest of its selection.

The entry narrows only an item in the self-serve `managed_installs`; one an
admin manifest lists in `managed_installs` goes to every placement. A run
places such an item only in its chosen placements, in machine scope and
through the user-placement plan, and removes its tree from any other
placement it holds a receipt for. `isInstalled` and `status` of a narrowed
item are worked out over its chosen placements only.

The two user-placement methods serve the per-user helper,
`<slug>-useragent.exe` (`cmd/sofcat-useragent`), and are not bound into
Wails. SYSTEM never writes into a user
profile: for zip variants whose placement scope on Windows is `user`, a run
that is not check-only stages each verified archive as `<hash>.zip` in
`user-artifacts` inside the data directory
(`${ProgramData}\<Name>\Data\user-artifacts`, which administrators write and
users read), removes archives no plan entry references, and then stores the
plan in the data directory as `user-plan.json` (`{entries: [...], facts:
{...}, placements: {...}}`, written atomically). The plan is that run's desired set (installs
and updates with their dependencies and `update_for` riders, the set scope
exit keeps) filtered to those variants, the same for every caller, with the
facts that run collected (built-in and admin-script facts alike, once per
response, not per entry). `listUserPlacements` skips the queue and returns
the last run's stored plan without reading the repository, so it answers at
logon even while the repository is unreachable; a helper that runs before
the first run gets an empty plan. `path` is the unexpanded placement root,
`archive` the staged copy, `held` marks an item under an undecided
`conditional_items` block, which the helper neither places nor removes, and
`managed` is the manifest's flag, which the helper records in its receipt and
renders the tree's templates with (`.Item.Managed`); the templates also
render with `facts` and the account's login name, and the helper runs no
script. `placements` maps the name of every placement whose scope on this
OS is `user`, whether or not an entry targets it, to its spec as
`placements.yaml` gives it (`paths`, `scope`, `exclusive`, `items_dir`,
`vars`, `files` and the rest, with their YAML key names), so the helper
enforces `exclusive`, places under `items_dir` and renders the placement's
`files` as the service does in machine scope; `{}` when there is none. A
plan from an older service has no `placements`: the helper then builds each
spec from the entry's `path` alone, with none of those, and logs that at
debug. An entry whose archive fails to stage is stored `held`, so a user
keeps the trees they have until it stages. The helper runs at logon from the
HKLM `Run` value `<slug>.exe -serviceinstall` writes. There it retries a
service it cannot connect to every 5 seconds until a minute has passed since
the first attempt (without a plan it removes nothing). The service also starts
it, with `--no-retry`, whenever a run stores a plan that differs from the one
the helpers were last started for: one helper per user SID, in each active or
disconnected session, with the session's token (UAC-filtered, so unelevated)
and that user's environment. Until every helper has started, each later run
starts them again, even for a plan that returns to the last one they were
started for, since a helper may have applied the plan in between. A plan
stored by an elevated administrator's `-config` run cannot start helpers (no
SeTcbPrivilege), so it is recorded as not yet given and the next service run
starts them. On macOS the helper,
`<slug>-useragent` beside the CLI, is the LaunchAgent `<identifier>.useragent`
(`/Library/LaunchAgents`, written by `-serviceinstall` and the package), which
launchd runs once at each GUI login; on a new plan the root service runs
`launchctl kickstart gui/<uid>/<label>`, or `bootstrap` where it is not
loaded, for every uid owning a `loginwindow`, with the same record-on-success
rule. A helper still running is not kickstarted (launchd runs one instance
and it may have fetched its last plan), so the next run starts
it. Its arguments are fixed, so it runs without `--no-retry` there.
`-serviceremove` boots it out of every such session, deletes the plist and
ends (SIGTERM) every process running from `/Applications/<Name>.app`.
The storefront does not sync. One user's syncs run one at a time. Each fetches the plan, takes the
lock file `.sync.lock` in the receipt directory, waiting while another sync
holds it, and fetches the plan again under the lock. The helper expands
the home variable itself (`${USERPROFILE}` on Windows, `${HOME}` on macOS),
checks the path against the build's `windows_user` or `darwin_user` roots,
re-verifies `hash`, places the tree with receipts in
`%LOCALAPPDATA%\<Name>\receipts` (`~/Library/Application Support/<Name>/receipts`
on macOS), removes its own
receipt-tracked trees that left the plan, regenerates the `files` of every
placement it still holds a receipt in, and sends every result with
`reportUserPlacements`; a failed regeneration is a result with the
placement and an empty `item`. The service logs them (failures at warn, with
the caller from the connection) and merges each caller's results for items the
stored plan lists into `user-placement-status.json` in its data directory, by
the caller's SID; only SYSTEM and Administrators (the Data directory's ACL)
read it. `ListOptionalInstalls` reads a user-scope
item as installed for that caller when its machine-scope placements are and
their agent last reported every user-scope one placed (`installed`,
`current`, `adopted` or `unchanged`); a `conflict` keeps the earlier outcome,
and an item the caller has no outcome for reads from the machine's receipts.
The service never opens a user's receipts. A signed-in user gets new content as soon as
the run that changed the plan ends. An `installItem` or `removeItem` whose
run succeeded and changed the requester's user-scope entries for its item
(an entry added or changed and not held, or one dropped that the requester
last reported placed) stays open until the requester's helper reports on
each of them, so the list the storefront reloads at the end already reads
the result (`awaitUserHelper` in `pkg/service/user_placement_wait.go`). It
waits only when that run started a helper for the requester's SID or uid; a
requester with no signed-in session (an administrator over SSH), a machine
without the helper and a failed start end at once. Meanwhile its latest
record is `Installing` or `Removing` for the item and a cancel is refused as
work already started. A result for the entry with an error ends it `Failed`,
with the code a machine-scope placement failure has (`placement_conflict`,
`placement_refused`, else `item_failed`) and the helper's error, as the row
stays not installed. A dropped entry's result that still reads placed comes
from a helper that fetched the old plan and does not count. After 30 seconds
(`userHelperWait`) the operation ends as the run left it, and the service
logs a warning. A managed run, the `run` method and `-config` wait for no
helper. `-serviceremove` ends any helper still
running beside it, after it removes the `Run` value, and any storefront, so an
open window does not keep `<slug>-ui.exe` locked against the uninstaller.

`run` asks for a managed run now and waits for its outcome; `sofcat -S run`
calls it, and it is not bound into Wails. Only a local administrator may call
it, by the rule that gives administrators their other rights (Boundary above;
`authorized` in `pkg/service/common.go`). On Windows that needs an elevated
token or SYSTEM, so a UAC-filtered administrator is refused; on macOS root or
a member of group `admin` passes; elsewhere only root. `sofcat -S run` itself
needs root on macOS all the same, because the CLI reads the root-only
`config.yaml` before it connects. Anyone
else, or a caller the service cannot resolve, gets `not_authorized`. The
service answers at once with an `AcceptedOperation` that has no
`operationId`, queues the run behind any run under way, and sends one
`runResult` notification when it has ended, then closes:

```json
{"jsonrpc":"2.0","id":"1759580000000000002","method":"run"}
{"jsonrpc":"2.0","id":"1759580000000000002","result":{"accepted":true,"queuedAtUtc":"2026-10-04T12:00:00.123Z","requestedBy":"PC01\\admin"}}
{"jsonrpc":"2.0","method":"runResult","params":{"completed":true,"failedItems":["GoogleChrome"],"deferredItems":[]}}
```

`completed` is whether the run ended without a fatal error. When it did not,
`errorCode` is `managed_run_failed`, and the service log has the error, or
`canceled` when the service stopped first. `canceled` is best effort: the
service closes open connections as it stops, so a client may instead see the
connection close before `runResult`. A client must read that as an unknown
outcome, never as success; `sofcat -S run` then exits non-zero.
`failedItems` and `deferredItems` name the items the run's report lists. The
run reports progress to any open operation whose item it reaches, as a
mutation's run does. A client that hangs up while it waits does not stop the
run. A run takes as long as it takes, so the client waits
on the notification with no deadline of its own.

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

`progressPercent` is present only when the service measured it, and is left
out otherwise. The only measured phase is a download whose server sent the
body's length: the item's percent climbs to 100 across all its archives,
reported at each whole-percent change. A download of unknown length, an
install, a removal and an `ItemFailed` record carry none. An `ItemCompleted`
record and the terminal record carry 100. A client must not read a missing
value as 0. Activity files a record only when it changes the item or the
state, so the percent updates of one download are a single entry.

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
| -32602 | `invalid_params` | a required param is missing or of the wrong type, or `placement` names a placement the item does not have on this device |
| -32603 | `internal_error` | the service panicked while handling the request |
| -32000 | `server_busy` | every handler slot is taken (sent before reading the request, so `id` is null), every stream slot, or every `run` waiter slot |
| -32001 | `command_failed` | the command failed (for example the manifest fetch) |
| -32002 | `operation_not_cancelable` | cancel refused: the item was acted on, the operation finished, or it is unknown |
| -32003 | `unknown_operation` | stream of an operation the service does not track |
| -32004 | `item_not_available` | `installItem` for an item not offered for self-service |
| -32005 | `item_not_removable` | `removeItem` for an item that is not a self-service item, or that an admin manifest requires |
| -32006 | `not_authorized` | `removeItem`, `cancelOperation`, or an `installItem` that would take back a pending removal, from a caller who is neither the item's selector (the operation's requester, for a cancel) nor a local administrator, and `run` from anyone who is not a local administrator |

The service does not support batches, and it does not act on a request without
an `id` (a notification): it closes the connection without a reply. The
`errorCode` inside a status record (`managed_run_failed`, `item_failed`,
`placement_conflict`, `placement_refused`, `blocked_by_running_app`,
`missing_software`) describes the operation's outcome, not the call. The two
placement codes are zip-item failures: a placed tree edited outside sofcat, and
a placement sofcat may not write ([Zip items](../site/src/content/docs/zip-items/placements.md#write-roots)).
`missing_software` is a deferral because software the item requires is not
installed; the message is the deferral reason, which names that software.

The service bounds what one client can hold. It handles 32 connections at a
time, at most 16 of them streams and at most 4 of them `run` requests
waiting for their run; a client beyond any of these limits gets `server_busy`. A client has 5 seconds to send its request line, after which the
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
cancellation or error, or a user cancel — end an operation. A succeeded run
that changed the requester's user-scope placements of the item ends it only
after their helper has reported, or 30 seconds have passed (see the
user-placement plan above).

`progressPercent` is scoped to the record's `itemName` and may reset at an item
boundary. An item's detail page shows one status line with a spinner while its
operation runs, naming the record's item when a dependency or updater takes
over. A Home card shows no progress, and a status only where its button
cannot say it: a pending item's (its button reads only Cancel), a required
item's (it has no button) and a partly installed item's ("Installed in
Claude Code": some of its `placements` are installed and some are not; its
button stays the install one, a plain `installItem` that adds the rest).
Otherwise the button names the state. From the click until the list fetched
after the operation's end arrives, the item is busy: its buttons read its
`installing` or `removing` label (a Cancel keeps `cancel`, since it sends
the opposite mutation), with no percentage, and are
`aria-disabled`, so they keep focus and a click on them asks for nothing.
The button so goes from "Install" to "Installing…" to "Remove", and never
shows the old action between the end and the refresh; a failed request
or refresh frees it at once. The card's line also appears for a
`Failed` or `Deferred` outcome or a refused request. The only bar is in the
bottom `#operation-strip`, which reports one running operation — the earliest
the service has started work on — as "n of N" with a `<progress max="100">`
that is determinate only when the latest record carries a percentage, so a
download of known length fills it and an install shows it indeterminate. The
percentage is still the record's item's, not an aggregate. The record
timeline is shown only in Activity, never on the Home view. No percentage is
fabricated for installs, removals, status checks, no-action items,
blocking-app checks, or pre/post scripts.

All of that is branding `progress: strip`, the default. With `progress:
button` the bar moves into the busy item's own buttons (`actionProgress` in
`state.ts`): its card's and detail page's action, and each placement row the
work is for, read "Waiting…" with a spinner while the latest record is
`Requested` or `Queued` (or the request is unanswered), then the phase over a
bar filled to the percentage only when the record carries one and
indeterminate otherwise. The item is freed exactly as under `strip`: when the
list fetched after the operation's end arrives (or fails). The service queues
that list behind running installs, so the wait can be long; meanwhile only a
`Succeeded` button shows a full bar, and a `Failed`, `Canceled` or `Deferred`
one shows its action again, still `aria-disabled`, with no bar, spinner or
`aria-busy`. The
accessible name stays the busy label with no percentage, `aria-busy` is set,
and a visually hidden `#announcer` says each new phase once per operation. An
item's own Cancel (`itemCancel`) on the card and the detail page follows the
strip's rules below. Its place is kept while it is hidden, so the action
never moves under the pointer and a double click on Install cannot land on
it: on the detail page it sits before the action, on a single-line card
after it, and on a column card on the row under it, beside Details. The
status lines keep only outcomes and a refused cancel's reason, since running
work is on the button. The root reserves the scrollbar's gutter and the
Activity tab its spinner's width, so neither the strip nor the start of work
moves or narrows the page. The strip shows only while two or more operations run, as a count
("Installing 1 of 3", with the item's own installing or removing label and
the place `stripView` picks; "Waiting 1 of 3" until the service starts on
any), and is a disclosure button (`aria-expanded`) for a list
above it: a row per running operation, the working one first, with its state
and a Cancel. Escape or a click outside closes the list and returns focus to
the strip when nothing else took it. Cards whose item is unchanged are kept
and repainted in place, so a bar eases rather than restarting.

The app bar carries the connection state (a dot and the cached/stale message
with Retry). Above it, the `#banner` section stays hidden until branding
fills it (see Branding). "My items" is the same list filtered on the client to installed or
managed items; it is not a separate service call. Each distinct item
`section` is a nav tab before "My items": the branding `sections` first, in
that order, then the rest alphabetically, then "Software" for items without
one (the only tab when no item has a section). A tab's search and category
filter cover only its items, and "My items" spans every section. A section
tab is headed by its name; the Software tab's heading is the branding
`listHeading`, or "Available software"; "My items" keeps its name. A tab
that disappears on a refresh falls back to the first one.

The applies-to filter narrows the current list by one name from the items'
`appliesTo`. It appears only when the list's items name two or more distinct
names. It is a select between the search field and the Category select,
labelled "Works in" or the branding `appliesToFilterLabel`; with
`nav_placement: left` and `category_filter: list` it is a button group in the
left bar above the categories, headed the same way. It comes first in both
because it is the shorter list, which keeps it in view above a long category
list. Its first option is "All". While a name is
picked, an item with no names is hidden. Search matches the names whatever
the filter says. It filters display only and scopes nothing.

Buttons and status text use the item's labels: `install` on the install
button, `remove` on the remove button, `cancel` on the Cancel of a pending
request and on the strip's Cancel, `installing` and `removing` on the busy
item's buttons and while its own install or removal runs, `installed` as an installed item's status
on the detail page, and `pending_install` and `pending_remove` as a pending
item's status on its card and detail page. Nothing shows an update action yet,
so `update` is accepted but unused. A card and the detail page add a line such as
"Applies to: Claude Code, Codex" when `appliesTo` is set, prefixed with the
`applies_to` label; it is display text and scopes nothing. An offline cache
entry from before the rename carries `worksIn` and shows no line. Labels,
the heading and the applies-to line are catalog or
admin text and go into the DOM only as `textContent`. On the detail
page an item with `placements` shows, in place of that line, a list labelled
with `applies_to` and one row per placement: its `label`, its status in the
item's wording (`installed`, `pending_install`, `pending_remove`, else "Not
installed") and one button with the item's labels. `NotInstalled` gets
`install`, which sends `installItem` with that `placement`; `Installed` gets
`remove`, `removeItem` with it, on an item with `isManaged`; without it the
row keeps `install`, as the item does. A pending placement gets `cancel`, which
sends the opposite mutation for that placement, as the item's own Cancel of a
pending request does for the item: `removeItem` for `WillBeInstalled`,
`installItem` for `WillBeRemoved`. The item's own Cancel names the placement
too when exactly one of several is pending, so it leaves the others as they
are; with several pending, short of all, the item has no button and the rows
cancel. Each button's accessible name is "<button
label>: <placement label>". The rows are busy while the item is, like its
own button; only the row the work is for, or each row a whole-item action
covers, reads the busy label. A required item's rows have none. The
operation, its progress and its Cancel stay per item. A `Deferred` card shows
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
`manifest.UpdateSelfServe`, and every read through `manifest.LoadSelfServe`.
Both take an in-process lock and the file lock `service-manifest.yaml.lock`,
which covers a command-line run beside the service. A hold is one load, or
one load, change and save. The run's prune checks item status on a snapshot
before it takes the lock, and nothing under the lock waits on `run.lock`. So
a mutation waits at most for another short load or save, never for a run.

Installed and managed state come only from an authoritative `ListOptionalInstalls`
refresh performed after a terminal record, never from progress records. A failed
refresh keeps the prior data and marks it stale.

## Branding

Branding is admin configuration, not catalog data. The SYSTEM service resolves
it field by field, first match wins. It reads managed settings at the start of
every run, so a policy change applies from the next run without a restart:

1. Managed settings, for MDM and GPO: values under
   `HKLM\SOFTWARE\Policies\SofCat\branding` on Windows, or the `branding` dict of
   `/Library/Managed Preferences/<IDENTIFIER>.plist` on macOS. The names are the
   `config.yaml` keys (`title`, `tagline`, `logo`, `link_url`, `link_label`,
   `link_placement`, `link_position`, the old names `help_url` and `help_label`,
   `accent`, `background_image`, `background_text`, `list_heading`, `applies_to_filter_label`, `sections`,
   `title_size`, `tagline_size`, `content_width`, `filter_placement`,
   `nav_placement`, `category_filter`,
   `app_bar_brand`, `banner_style`, `banner_align`, `banner_height`, `item_view`,
   `progress`, `refresh_interval`;
   `sections` is a REG_MULTI_SZ).
2. The `branding:` block in `config.yaml`.
3. Nothing, and the UI keeps its default look.

A rebranded build may compile in a `branding.yaml`
(`pkg/config/branding_brand.go`, the `brand_branding` tag). A field it sets
comes before all three: neither policy nor `config.yaml` changes it.

`help_url` and `help_label` are the released names of `link_url` and
`link_label`. `pkg/config` folds them into the new names source by source
before the merge: within one source the new name wins, and across sources the
policy wins as for any key, so a policy still written with `help_url`
overrides a `config.yaml` that has moved to `link_url`. `getBranding` keeps
the field names it shipped with, `helpUrl` and `helpLabel`, for the link: an
older UI, and a branding payload a newer UI cached from an older service,
still show the button, in the banner (no `linkPlacement` reads as `banner`).

`pkg/branding` validates after the merge, so a bad policy value is dropped rather
than replaced by the config value. Text is trimmed, stripped of control
characters and capped (title 120, tagline 240, link label 60, list heading 120
runes). The link
URL must be absolute http or https. The accent must match `#rrggbb`. The logo is
read from its local path on every call, capped at 512 KiB, and kept only if its
bytes sniff as PNG, JPEG or SVG. The background image is read the same way,
capped at 1 MiB, and `background_text` must be `light` or `dark`. `title_size` and
`tagline_size` must be `small`, `medium`, `large` or `x-large`; the UI turns them into
the factors 0.85, 1, 1.25 and 1.5 on its default sizes (CSS `--title-scale` and
`--tagline-scale`), and anything else is 1. `content_width` must be `compact`, `wide`
or `full`; the UI sets it as `data-content-width` on the root element, and anything
else is `wide`. `filter_placement` must be `right` or `left`; the UI sets it as
`data-filter-placement` on the root element, and anything else is `right`.
`nav_placement` must be `top` or `left` and `category_filter` `select` or
`list`; the UI sets them as `data-nav-placement` and `data-category-filter`
on the root element, and anything else is the first. The header is one
element in both placements: `style.css` lays it out as a column at the left
for `left` in a window of 40rem or more, and as a bar across the top
otherwise. For `list` the column also shows the current list's categories as
buttons that set the Category select, which stays the one place the choice
is kept and is hidden while the buttons show; with the bar across the top
the buttons never show, so `list` is `select` there. The current page's
highlight is one marker element that `main.ts` moves under the current
page's button, so it slides between pages in either placement.
`app_bar_brand` must be `full`, `mark` or `none`, `banner_style` `text` or `logo`
and `banner_align` `left`, `center` or `right`; anything else is the first. The UI
sets them as `data-app-bar-brand` on the root element (`full` while no banner
shows, and hidden only visually, so the h1 keeps the product name), as the logo
in the title's place with the title as its alt text (`text` without a usable
logo, and no logo in the app-bar mark or the window icon) and as `data-align` on
the banner. `banner_height` must be `small`, `medium`, `large` or `x-large`; the UI
sets it as `data-height` on the banner, and anything else is `medium`.
`progress` must be `strip` or `button`; the UI sets it as `data-progress` on
the root element, and anything else is `strip`.
`link_placement` must be `banner` or `app_bar`, and `link_position` `auto`,
`top_left`, `top_right`, `bottom_left` or `bottom_right`; anything else is the
first. The UI moves the one link button to the app bar's end, before the
connection status, for `app_bar`, where a link alone no longer shows the
banner and `link_position` is ignored; for a corner of a banner with the link
it sets `data-link-position` on the banner. `item_view` must be `page` or
`dialog`, and anything else is `page`. For `page` a card's Details button, or
a click on the card, replaces the list with the item's detail page. For
`dialog` the card has no Details button, its name is a button instead, and
either opens the item in a modal `<dialog>` over the list. Both show the one
`#detail-content` subtree, which `renderDetail` fills: `main.ts` mounts it on
the detail page or in the dialog, and a change of mode closes an item open
the old way first. The dialog grows out of its card with the Web Animations
API and shrinks back into it; a close during the grow plays it back from
where it is, and a card not wholly on screen, or reduced motion, gives no
motion. Close, `Escape` or a click on the backdrop closes it and refocuses
the card's name, or the search field once a refresh has dropped the card,
which also closes the dialog. While it is open, refreshes and progress fill
the same subtree in place, so focus stays inside it. The operation strip
stays at the window's bottom, inert under the backdrop, so the dialog shows
its own Cancel beside the action while the open item's operation runs
(`itemCancel`), set up by the strip's own `setCancel`; when it goes off or
away while focused, focus moves to the action, or to the close button when
the item shows no action. The page has none under `progress: strip`; under
`button` the page and the dialog both have it, and a card has its own (see
the progress paragraphs above). The queue list of `progress: button` closes
when the dialog opens, so it never floats over the backdrop. In the dialog the action has a
row of its own under the title, at its right end, with Cancel before it on
a line that never wraps (a long Cancel label is cut short), so no label
takes width from the text, and the dialog's top edge is fixed rather than
centred. The action's right edge and top, and the placement rows' buttons,
stay where they were clicked as Cancel and the status line come and go. The
backdrop fades in as the dialog opens and out as it shrinks back (a close
with no shrink removes it at once), through the Web Animations API,
and a close during the fade-in fades out from where it has got to. After a
pointer action the dialog focuses its close button, and the card's name on
close, with `focusVisible: false` where the web view takes it, so only
keyboard use draws a ring. `refresh_interval` is a duration, sent as
`refreshIntervalSeconds`: 0 for none or one that does not parse (the UI then
uses ten minutes), -1 for a zero duration (the UI then has no timer) and at
least 60 otherwise. On each interval the UI calls `ListOptionalInstalls` and
`GetBranding` again, unless its window is hidden; it catches up when the
window shows. A
dropped field is logged at debug level and
never fails the call.

`GetBranding` skips the service command queue, because the UI calls it once
before creating its window to pick the window title, icon and caption colour
and must not wait behind a managed run. That startup call gives up after two
seconds and falls back to "SofCat UI", the embedded sofcat icon and the system
caption. The caption colour goes through the Windows 11 DWM caption attributes
(`CustomTheme`), so the native title bar stays and only its colours follow the
accent; the window is not frameless. The frontend then applies the cached payload from
`sofcat.branding.v1`, fetches a fresh one and reapplies it. The banner shows
when a title, tagline, logo, background image or help URL is set. The logo is an
`<img>` from a `data:` URL, so an SVG cannot run script. The background image is
a CSS background from a `data:` URL, drawn over the accent, and takes the
accent's place in the banner only. The banner text over it is white, or
near-black when `backgroundText` is `dark`. The help button opens its URL in the
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

Structured `slog` diagnostics are opt-in through `SOFCAT_UI_DEBUG=1` or `SOFCAT_DEBUG=1`. Enabled logs use the existing lumberjack dependency at `%LOCALAPPDATA%\sofcat\ui-client.log`, 10 MiB with one backup. The per-user helper always logs, at info level, to its own `useragent.log` beside its receipts, so the two never rotate each other's file; it rotates at 1 MiB with one backup, the same switches add its debug records, and a log it cannot open never stops the sync. Lifecycle and status-event records are debug-level and include available operation, operation ID, state, result, and duration fields. The storefront's disabled diagnostics create no directory or file, and logging failures do not affect UI operations. The running service logs to `sofcat.log` in its data directory. The management commands (`-serviceinstall`, `-serviceremove`, `-servicestart`, `-servicestop`) log each step and outcome to stderr and, on Windows, to the Application log under the source `<slug>`, with one event ID per kind of step (`pkg/sofcatlog/eventlog.go`; `site/src/content/docs/reference/event-log.md`), so a silent uninstall's sweep is still on record. `-serviceinstall` registers the source, the others register it when it is missing, and nothing removes it. A source that cannot be registered or opened leaves stderr only, and never fails the command.

## Deliberate ceilings

- Progress is per item; there is no weighted or aggregate operation model. Upgrade
  only when the engine exposes a real work graph.
- Activity keeps 100 local display records and is not an audit trail. Upgrade only
  when an authoritative history operation exists.
- A closed app does not resume a stream; the next list refresh converges state.
- An item without an icon the service holds gets a locally generated one
  (monogram or category glyph). The UI never fetches an icon itself, and keeps
  the ones the service hands over only for the session, so a storefront opened
  while the service is unreachable shows monograms.
- `OperationStatus` is hand-typed in `frontend/src/api.ts` because bindings are
  generated with `-noevents`, so the binding-diff gate cannot catch drift in it.
  Upgrade only when bindings are generated with events.
