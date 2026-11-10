---
title: Zip items
description: Place directory trees such as company wallpapers from deterministic zips, per user or per machine.
---

A zip item places a directory tree, such as a pack of company wallpapers, instead of
running an installer. The catalog lists one zip per placement and OS:

```yaml
acme-wallpapers:
  version: 1.0.0
  installer:
    type: zip
    variants:
      wallpapers:
        darwin:
          location: packages/wallpapers/acme-wallpapers-1.0.0-7f654b4f9bcd.zip
          hash: 8f1b121ca72cfda3d952aa0578f383a64837a6b67fab192dbda3e82d3e512331
          tree_hash: sha256:7f654b4f9bcd94696c9026e404f741050a2898def06e152495e7207126bcebfd
        windows: {location: ..., hash: ..., tree_hash: ...}
```

- `variants` maps a placement name from `placements.yaml` to an OS, `darwin` or
  `windows`, to one zip. A rootless placement, which only writes files, holds
  no items and cannot be one.
- `location` is the zip's path in the repository, `hash` its sha256 and
  `tree_hash` the tree hash of its contents.
- The zip's root holds the item's files directly, such as `acme-blue.png`, with
  no `<item>/` folder around them. The tree is placed at
  `<placement path>/<item_name>`, or `<placement path>/<items_dir>/<item_name>`
  when the placement sets `items_dir`.
- A zip item needs no `check` block and no `uninstaller`.

Do not write the `installer` block by hand. `makecatalogs build` writes it
from an item file that lists the item's placements and a tree that goes to
all of them ([Building a repository](/zip-items/building/#building-a-repository)).

These pages are the reference for zip items and use a small wallpaper example.
sofcat only places the files; choosing a wallpaper is left to a configuration
profile, a policy or the user.

[Build an AI store](/ai-store/overview/) is the fully worked example: a
complete store for Claude Code, Codex and Claude Desktop. It uses zip items to
their full extent, with placements for skills and plugin marketplaces,
placement files, tree templates and per-user scope.

- [Placements](/zip-items/placements/): `placements.yaml`, rootless placements, storefront labels and write roots.
- [Placement files](/zip-items/placement-files/): files sofcat renders from a placement's receipts, and their templates.
- [Tree templates](/zip-items/tree-templates/): files rendered inside a tree on the device.
- [Building zip items](/zip-items/building/): packing, the tree hash, `makecatalogs` commands and checks.

## Installing

sofcat installs a zip item by placing one tree per placement for the OS it
runs on, in `placements.yaml` order. The service skips placements whose scope
on this OS is `user`; they neither fail nor succeed the item. Instead, after
each run that is not check-only, it stages their verified archives in
`user-artifacts`, which users may read: inside its data directory on Windows,
the one entry there users may read, and beside it on macOS, where `Data` is
root-only ([Data directory](/reference/data-directory/)), each named `<hash>.zip` after
its sha256 (never after its catalog `location`), removes archives no entry
references, and then stores the plan, built from that run's desired set
(dependencies and `update_for` riders included), as `user-plan.json` in its
data directory. An entry whose archive fails to stage is stored held, so the
helper leaves that item's trees as they are until the archive stages. The
per-user helper, `<slug>-useragent`, fetches that stored plan, so it
needs no repository access, and places the trees in each user's profile under the
`windows_user` or `darwin_user` roots, with receipts in
`%LOCALAPPDATA%\<NAME>\receipts` or `~/Library/Application Support/<NAME>/receipts`, and
writes the placements' `files` ([Placement files](/zip-items/placement-files/))
([Protocol](https://github.com/hurricanehrndz/sofcat/blob/main/ui/ARCHITECTURE.md#protocol)). The item succeeds when every
machine-scope placement does; a failed placement leaves the others alone and
is reported on its own.

The helper starts at every logon, from the HKLM `Run` value that
`-serviceinstall` writes. The service also starts it whenever a run stores a
plan the signed-in users have not been given yet: one helper per user, in
each active or disconnected session, running as that user and unelevated. A
plan stored by an elevated administrator's own `<slug>.exe -config` run
cannot start helpers, so those users catch up at the next service run. The
storefront does not sync. A user's syncs run one at a time. Each takes a lock
file in that user's receipt directory, waiting while another sync holds it. `-serviceremove` ends
any helper still running and closes an open storefront, so the uninstaller
can delete their executables. Each process it ends, and each it could not,
is recorded in the [event log](/reference/event-log/).

On macOS the helper is a LaunchAgent,
`/Library/LaunchAgents/<IDENTIFIER>.useragent.plist`, which the package
installs and `-serviceinstall` writes: launchd runs it once at every GUI
login, as that user. On a new plan the service runs it again for each user
signed in to a GUI session, fast-user-switched ones included, with
`launchctl kickstart`, or with `launchctl bootstrap` for a user who signed in
before the install. A helper still running then is started again at the
next service run. `-serviceremove` boots it out of every user's session,
which ends a running helper, deletes the plist and quits an open storefront
without asking. The helper logs to
`~/Library/Application Support/<NAME>/useragent.log`.

Each helper run logs what it placed, held and failed to
`%LOCALAPPDATA%\<NAME>\useragent.log`. The file rotates at 1 MiB to a single
backup. `SOFCAT_DEBUG=1` in the user's environment adds debug records.

A tree has two hashes. The catalog's `tree_hash` is the hash of the archive's
contents, as CI packed them. The receipt's `placed_hash` is the hash of the
tree as sofcat wrote it, files sofcat rendered on the device included; with
nothing rendered, the two are equal. For each placement, sofcat hashes the
item's tree on disk and checks it in this order:

1. No tree: install it.
2. The tree equals its receipt's `placed_hash`: it is sofcat's and intact.
   When the receipt's `tree_hash` and version match the catalog and the
   inputs of every rendered file are what this run would render with,
   nothing to do (a receipt whose `managed` flag moved is rewritten). When
   only the inputs differ, sofcat re-renders those files in place from the
   templates still in the tree, without fetching the archive, rehashes and
   updates the receipt. When the catalog moved on to a different `tree_hash`,
   sofcat replaces the tree with the catalog version; a version bump that
   keeps the same `tree_hash` only rewrites the receipt.
3. The tree equals the catalog's `tree_hash` but not the receipt's
   `placed_hash`: the tree as shipped, placed by hand or before sofcat
   rendered anything. sofcat adopts it: renders in place and writes the
   receipt.
4. Anything else, including a tree with no receipt and an edit to a rendered
   file: a user edit. sofcat writes nothing and fails the item with
   `placement_conflict`.

To install, sofcat downloads the zip to its cache, checks its sha256 against
`hash`, and refuses an archive with an absolute path or `..`, a non-regular
entry, a duplicate or case-only duplicate path, a Windows reserved name or
forbidden character (on every OS), more than 10,000 entries, a file over
256 MiB, more than 1 GiB in total, or a compression ratio over 100:1. It
extracts into `.<slug>-staging-<item>` beside the tree, checks the tree hash
against `tree_hash`, then renders the tree's templates there, hashes the
result for `placed_hash`, and only then renames the tree into place.

An in-place re-render is all or nothing: every output is rendered to memory
first, then each file is written to a temporary file beside its target and
renamed over it, and a failure part way puts back the files already replaced,
so the tree still matches `placed_hash`. The one window left is a crash
between two renames, which leaves a tree matching neither hash; the next run
reports it as `placement_conflict`, and restoring the file by hand or
removing the tree clears it.

A removal (`managed_uninstalls`, a self-service removal, or an item that left
every manifest) deletes a tree only while it equals its receipt's
`placed_hash`; otherwise it fails with `placement_conflict` and the tree
stays. Items held by an unknown `conditional_items` condition are never
removed.

A zip item's installed status comes from its receipts and trees, never from a
`check` block.

## Receipts

Each placed tree has a JSON receipt at
`<data dir>/receipts/<placement>/<item_name>.json`:

```json
{
  "item": "acme-wallpapers",
  "version": "1.0.0",
  "placement": "wallpapers",
  "path": "C:\\ProgramData\\Acme\\Wallpapers\\acme-wallpapers",
  "tree_hash": "sha256:7f654b4f…",
  "placed_hash": "sha256:7f654b4f…",
  "rendered": [],
  "managed": true,
  "installed_at_utc": "2026-11-06T12:00:00Z"
}
```

`tree_hash` is the catalog's hash of the archive's contents; `placed_hash`
is the hash of the tree as sofcat wrote it, rendered files included, and the
hash sofcat compares the tree against before it touches or removes it.
`rendered` lists every file sofcat rendered in the tree, one
`{"file", "template", "inputs"}` record each: the rendered file's path and
the template's, slash-separated and relative to the tree, and the inputs the
render used ([Tree templates](/zip-items/tree-templates/#what-the-receipt-records)). With nothing
rendered, `placed_hash` equals `tree_hash` and
`rendered` is `[]`, present rather than omitted. `managed` is true when a
manifest lists the item in `managed_installs`. A receipt written before
`placed_hash` existed is read as if `placed_hash` were its `tree_hash`. A
run that finds nothing to do leaves the receipt untouched, while a `managed`
flip or a catalog version bump with the same contents rewrites only the
receipt.
