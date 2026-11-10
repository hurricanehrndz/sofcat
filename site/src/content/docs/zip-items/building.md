---
title: Building zip items
description: Pack deterministic zips, compute tree hashes, generate package-info files and check a repository.
---

These are the formats and commands that turn an item tree into a catalog entry, and the checks that guard it.

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
makecatalogs zip-item --placements <placements.yaml> --item <item.yaml> \
  --trees <dir> --location-prefix <prefix> -o <repo>
```

- `pack` writes the zip and prints `{"hash":"<hex>","tree_hash":"sha256:<hex>"}`.
- `tree-hash` prints the tree hash and writes nothing. Run it on a device to
  compare a placed tree with the catalog.
- Without `--os`, both use the OS they run on: executable bits are kept,
  except on Windows. Pack every `windows` tree with `--os windows`.
- `zip-item` turns item trees into a package-info file. `<item.yaml>` is
  a package-info file without `installer`. `<dir>` holds one tree per
  placement and OS, at `<dir>/<placement>/<darwin|windows>/`. Write the
  trees by hand, or generate them in CI with any template engine; a `.tmpl`
  file a tree ships is rendered on the device instead ([Tree templates](/zip-items/tree-templates/)), and
  `zip-item` test-renders it. Each tree is packed, `windows` trees with
  `--os windows`, to `<repo>/<prefix>/<item_name>-<version>-<12 hex of
  tree_hash>.zip`. Trees that hash the same share one zip. The package-info
  file, with `installer` filled in, goes to stdout.

`zip-item` checks everything before it writes a file. It fails, and writes
nothing, when:

- `<item.yaml>` already has an `installer`
- `<dir>` has no placement directories
- a placement is not in `placements.yaml`
- an OS directory is not `darwin` or `windows`
- the placement has no path for that OS
- a tree is empty or holds a symlink
- a tree's root is a single directory named after the item
- a tree ships `x` beside `x.tmpl`, a file named just `.tmpl`, or an
  `x.tmpl.tmpl`
- a `.tmpl` fails to render with the sample facts and `check_facts`, with
  `.Item.Managed` either way or with the sample user ([Tree templates, Validation](/zip-items/tree-templates/#validation)); the error names the item, the OS and the template

[examples/zip-items/](https://github.com/hurricanehrndz/sofcat/tree/main/examples/zip-items) holds an item tree, the
input, and the output. To regenerate the output:

```sh
makecatalogs zip-item --placements examples/placements.yaml \
  --item examples/zip-items/acme-wallpapers.yaml \
  --trees examples/zip-items/item-trees/acme-wallpapers \
  --location-prefix packages/wallpapers -o examples/zip-items/generated \
  > examples/zip-items/generated/acme-wallpapers.yaml
```

## makecatalogs -check

`makecatalogs` validates `placements.yaml` when the repository has one, and
checks every zip item. It reports:

- a zip item with no variants
- a variant without `location`, `hash` or `tree_hash`
- a variant OS other than `darwin` or `windows`
- a variant placement that `placements.yaml` does not define
- a zip item in a repository with no `placements.yaml`
- a `custom_action_labels` key other than the ten above, on any item
- a `files` template that fails to render: for each OS the entry has a
  path on, the template is rendered with `.Items` built from the catalog
  items that target the placement, managed both ways, against a sample of every
  built-in fact of that OS overlaid with `check_facts` (see [Templates for
  placement files](/zip-items/placement-files/#templates-for-placement-files)). A missing fact, a `cond` naming a fact no sample
  provides, a `cond` calling `now()`, and output that is not JSON on a
  `.json` path all fail.
- a tree template in a zip that fails to render: every zip a zip item names
  that the repository holds is opened, and each `.tmpl` entry is rendered
  per variant as `zip-item` renders it ([Tree templates, Validation](/zip-items/tree-templates/#validation)); a zip
  that ships `x` beside `x.tmpl`, a bare `.tmpl` or an `x.tmpl.tmpl` fails
  too. A zip hosted outside the repository is not checked.

A normal run warns about these; `makecatalogs -check` fails on them.

## Zip-only builds

A brand whose `brand.env` sets `INSTALLERS=zip` builds all three binaries with the
`zip_only` Go build tag. Such a binary has no code for the `msi`, `exe`, `ps1`,
`msix` and `nupkg` installers. It treats a catalog item as invalid when it has
an `installer.type` other than `zip` or an `uninstaller.type` set to anything
but `zip`. Catalog scripts are in every build, so `depends_check` scripts run
in a zip-only build. It logs the reason (`rejected by this zip-only build`) once per
item and reason for the life of the process, the run neither installs nor
removes the item, `ListOptionalInstalls` leaves it out and `InstallItem` refuses it with `item_not_available`. Admin
fact scripts still run, both the ones in `<data dir>/conditions` and the ones
[the repository delivers](/guides/conditions/#scripts-from-the-repository).
