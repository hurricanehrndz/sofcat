# Zip items

A zip item places a directory tree, such as an AI skill or plugin, instead of
running an installer. The catalog lists one zip per placement and OS:

```yaml
hello-world:
  version: 1.0.0
  installer:
    type: zip
    variants:
      claude-code-skills:
        darwin:
          location: packages/skills/hello-world-1.0.0-ffaa66a85382.zip
          hash: f035805d1c8f3571a7622db7be1a968b65eb815fc7c82afcdb16926a64654e91
          tree_hash: sha256:ffaa66a853824549c6f1e1047d0fc3ffd1d3e4c33919cfa4ce15c3d3ef94d9b0
        windows: {location: ..., hash: ..., tree_hash: ...}
```

- `variants` maps a placement name from `harnesses.yaml` to an OS, `darwin` or
  `windows`, to one zip.
- `location` is the zip's path in the repository, `hash` its sha256 and
  `tree_hash` the tree hash of its contents.
- The zip's root holds the item's files directly, such as `SKILL.md`, with
  no `<item>/` folder around them. The tree is placed at
  `<placement path>/<item_name>`, or `<placement path>/plugins/<item_name>`
  in a marketplace.
- A zip item needs no `check` block and no `uninstaller`.

Do not write the `installer` block by hand. `makecatalogs zip-item` writes it.
## Installing

sofcat installs a zip item by placing one tree per placement for the OS it
runs on, in `harnesses.yaml` order. The service skips placements whose scope
on this OS is `user`; they neither fail nor succeed the item. Instead, after
each run that is not check-only, it stages their verified archives in
`user-artifacts` beside its executable, each named `<hash>.zip` after its
sha256 (never after its catalog `location`), removes archives no entry
references, and then stores the plan, built from that run's desired set
(dependencies and `update_for` riders included), as `user-plan.json` in its
data directory. The per-user helper `<slug>-ui.exe --user-sync`, which runs
at every logon and storefront start, fetches that stored plan, so it needs no
repository access, and places the trees in each user's profile under the
`windows_user` roots, with receipts in `%LOCALAPPDATA%\<NAME>\receipts`
([Protocol](../ui/ARCHITECTURE.md#protocol)). The item succeeds when every
machine-scope placement does; a failed placement leaves the others alone and
is reported on its own.

For each placement, sofcat checks the item's tree in this order:

1. No tree: install it.
2. The tree equals the catalog's `tree_hash`: nothing to do. If it has no
   receipt, or one for another version, sofcat writes one (adoption).
3. The tree equals its receipt: replace it with the catalog version.
4. Anything else, including a tree with no receipt: a user edit. sofcat writes
   nothing and fails the item with `placement_conflict`.

To install, sofcat downloads the zip to its cache, checks its sha256 against
`hash`, and refuses an archive with an absolute path or `..`, a non-regular
entry, a duplicate or case-only duplicate path, a Windows reserved name or
forbidden character (on every OS), more than 10,000 entries, a file over
256 MiB, more than 1 GiB in total, or a compression ratio over 100:1. It
extracts into `.<slug>-staging-<item>` beside the tree, checks the tree hash,
and only then renames the tree into place.

A removal (`managed_uninstalls`, a self-service removal, or an item that left
every manifest) deletes a tree only while it equals its receipt; otherwise it
fails with `placement_conflict` and the tree stays. Items held by an unknown
`conditional_items` condition are never removed.

A zip item's installed status comes from its receipts and trees, never from a
`check` block.

### Marketplace and settings placements

A `marketplace` placement is a directory that Claude Code and Codex read as a
local marketplace. Each plugin's tree goes to `<placement path>/plugins/<item_name>`,
under the same rules as a `directory` placement: the check order above,
receipts, the exclusive marker at the placement path, and the refusals.

After every change to a marketplace (an install, an upgrade, an adoption, a
removal, or a receipt whose `managed` flag changed), sofcat rewrites its
generated files from that placement's receipts, not from the run. A held or
failed plugin keeps its entry. The files have no receipts: sofcat owns them,
and status and scope exit ignore them. Every run also rewrites the generated
files from the receipts, so a deleted or refused file is restored on the next
run. Every path in the block's
`manifest_files` (default `.claude-plugin/marketplace.json`), relative to the
placement path, gets the same content:

```json
{
  "name": "acme",
  "owner": {
    "name": "Acme IT"
  },
  "renames": {
    "old-plugin": "new-plugin"
  },
  "allowCrossMarketplaceDependenciesOn": [
    "other-marketplace"
  ],
  "plugins": [
    {
      "name": "hello-plugin",
      "source": "./plugins/hello-plugin",
      "version": "1.0.0",
      "defaultEnabled": true
    }
  ]
}
```

`owner`, `renames` and `allowCrossMarketplaceDependenciesOn` are copied from
the `marketplace` block. Map keys come out sorted, and the last two are left
out when the block does not set them. Plugins are sorted by name. `version` is
the catalog version the receipt records. Bump it when a plugin's files change,
because Codex may not reload them otherwise. `defaultEnabled` is the receipt's
`managed` flag: true when a manifest lists the item in `managed_installs`. A run
updates the flag only for items it decides. An item held by an unknown
`conditional_items` condition keeps its recorded flag. With no plugin left, the
manifest still lists `"plugins": []`.

A `settings` placement is one JSON file, never an item's target. A variant
that names one fails with `placement_refused`. Whenever sofcat regenerates the
marketplace that the file `registers`, it rewrites the file through a
temporary file in the same directory and creates the directory if needed:

```json
{
  "extraKnownMarketplaces": {
    "acme": {
      "source": {
        "source": "directory",
        "path": "C:\\Program Files\\Acme\\marketplace"
      }
    }
  },
  "enabledPlugins": {
    "hello-plugin@acme": true
  }
}
```

`path` is the marketplace's resolved path. `enabledPlugins` lists only the
managed plugins, keyed `<plugin>@<marketplace name>`. When the marketplace
holds no plugin, sofcat removes the file. The file's path passes the same
write-root and data and binary directory checks as a placement path. A refused
path fails the item that triggered the rewrite with `placement_refused`; the
per-run rewrite reports it under the marketplace placement's name, action
`generate`.
Generated files are written by the service only: marketplace and settings
placements cannot be `user` scope.

### Receipts

Each placed tree has a JSON receipt at
`<data dir>/receipts/<placement>/<item_name>.json` with `item`, `version`,
`placement`, `path`, `tree_hash`, `managed` (a manifest lists the item in
`managed_installs`) and `installed_at_utc`.

### Write roots

A build writes only under its write roots. A brand sets them in
`write-roots.yaml`, compiled into the binaries by `just brand=<dir> build`:

```yaml
darwin: [/Library/Application Support/Acme]
windows: ['${ProgramFiles}\Acme']
windows_user: ['${USERPROFILE}\.agents']
```

Without the file, a build uses `${ProgramFiles}\<NAME>` on Windows and
`/Library/Application Support/<NAME>` on macOS, and has no user roots. Runtime
config cannot change them. sofcat resolves symlinks in a placement path, then
refuses it (`placement_refused`) when it is outside every root for its scope,
inside sofcat's data directory, or inside, equal to or around a directory
holding sofcat's binaries. An `exclusive` placement that exists, is not empty
and lacks the marker file `.<slug>-owned` is refused too; a root sofcat
creates gets the marker. A repository without `harnesses.yaml` fails every
zip item with `placement_refused`.

### Zip-only builds

A brand whose `brand.env` sets `INSTALLERS=zip` builds both binaries with the
`zip_only` Go build tag. Such a binary has no code for the `msi`, `exe`, `ps1`,
`msix` and `nupkg` installers or for catalog scripts. It treats a catalog item
as invalid when it has an `installer.type` other than `zip`, an
`uninstaller.type` set to anything but `zip`, or any of `preinstall_script`,
`postinstall_script`, `preuninstall_script`, `postuninstall_script` and
`check.script`. It logs the reason (`rejected by this zip-only build`) once per
item and reason for the life of the process, the run neither installs nor
removes the item, `ListOptionalInstalls` leaves it out and `InstallItem` refuses it with `item_not_available`. Admin
fact scripts (`<data dir>/conditions`) still run.

## The archive

`makecatalogs pack` writes the same bytes for the same tree on every OS:

- Regular files only, sorted by their slash-separated relative path. No
  directory entries.
- Every entry is dated 1980-01-01 00:00:00 UTC, has mode 0644 or 0755, and
  is compressed with Deflate.
- A tree that contains a symlink or other special file is refused, and so is
  a tree with no files.

## The tree hash

The tree hash is `sha256:` followed by the lowercase hex sha256 of one record
per regular file, in byte order of the slash-separated relative path:

```
<path> NUL <755|644> NUL <lowercase hex sha256 of the content> LF
```

A file is `755` when any executable bit is set, otherwise `644`. Windows has
no executable bit, so for the `windows` target every file is `644`, whatever
OS computes the hash. Empty directories do not count.

## Commands

```sh
makecatalogs pack [--os darwin|windows] <dir> -o <file.zip>
makecatalogs tree-hash [--os darwin|windows] <dir>
makecatalogs zip-item --harnesses <harnesses.yaml> --item <item.yaml> \
  --rendered <dir> --location-prefix <prefix> -o <repo>
```

- `pack` writes the zip and prints `{"hash":"<hex>","tree_hash":"sha256:<hex>"}`.
- `tree-hash` prints the tree hash and writes nothing. Run it on a device to
  compare a placed tree with the catalog.
- Without `--os`, both use the OS they run on: executable bits are kept,
  except on Windows. Pack every `windows` tree with `--os windows`.
- `zip-item` turns rendered trees into a package-info file. `<item.yaml>` is
  a package-info file without `installer`. `<dir>` holds one tree per
  placement and OS, at `<dir>/<placement>/<darwin|windows>/`. sofcat does
  not render; use any template engine. Each tree is packed, `windows` trees
  with `--os windows`, to `<repo>/<prefix>/<item_name>-<version>-<12 hex of
  tree_hash>.zip`. Trees that hash the same share one zip. The package-info
  file, with `installer` filled in, goes to stdout.

`zip-item` checks everything before it writes a file. It fails, and writes
nothing, when:

- `<item.yaml>` already has an `installer`
- `<dir>` has no placement directories
- a placement is not in `harnesses.yaml`
- an OS directory is not `darwin` or `windows`
- the placement has no path for that OS
- a tree is empty or holds a symlink
- a tree's root is a single directory named after the item

[examples/zip-items/](../examples/zip-items) holds a rendered tree, the
input, and the output. To regenerate the output:

```sh
makecatalogs zip-item --harnesses examples/harnesses.yaml \
  --item examples/zip-items/hello-world.yaml \
  --rendered examples/zip-items/rendered/hello-world \
  --location-prefix packages/skills -o examples/zip-items/generated \
  > examples/zip-items/generated/hello-world.yaml
```

## harnesses.yaml

`harnesses.yaml` sits at the repository root, next to `catalogs/` and
`manifests/`. It names the placements. Unknown fields are rejected.
[examples/harnesses.yaml](../examples/harnesses.yaml) shows every field.

```yaml
placements:
  codex-skills:
    kind: directory
    paths:
      darwin: /etc/codex/skills
      windows: ${USERPROFILE}\.agents\skills
    scope: {windows: user}
    works_in: [Codex]
```

| Field | Meaning |
|---|---|
| key | The placement name. Variants and rendered trees use it. |
| `kind` | `directory`, `marketplace` or `settings`. |
| `paths` | The placement root, keyed by `darwin` and `windows`. |
| `scope` | `machine` or `user`, keyed like `paths`. An OS left out is `machine`. `darwin: user` is not supported, and `user` only on kind `directory`. |
| `works_in` | Harness display names, such as `[Claude Code]`. |
| `action_labels` | Optional label overrides. Keys: `install`, `installing`, `installed`, `remove`, `removing`, `update`, `cancel`. |
| `exclusive` | Optional. When true, sofcat does not write into an existing, non-empty root it did not create. |
| `marketplace` | `marketplace` kind only, and required there: `name`, `owner`, optional `renames`, `allowCrossMarketplaceDependenciesOn` and `manifest_files` (default `[.claude-plugin/marketplace.json]`). `owner`, `renames` and `allowCrossMarketplaceDependenciesOn` are copied into the manifest unchanged. |
| `registers` | `settings` kind only, and required there: the marketplace placement the drop-in registers. |

Windows paths may use `${ProgramFiles}` and `${ProgramData}`. A path may use
`${USERPROFILE}` only when its OS has `user` scope.

Where order matters, placements are taken in file order.

## makecatalogs -check

`makecatalogs` validates `harnesses.yaml` when the repository has one, and
checks every zip item. It reports:

- a zip item with no variants
- a variant without `location`, `hash` or `tree_hash`
- a variant OS other than `darwin` or `windows`
- a variant placement that `harnesses.yaml` does not define
- a zip item in a repository with no `harnesses.yaml`

A normal run warns about these; `makecatalogs -check` fails on them.
