---
title: Configuration
description: Where SofCat's config lives and the annotated example files for config, manifests, catalogs and package info.
---

The agent reads one file, `config.yaml`:

| Platform | Path |
| --- | --- |
| Windows | `C:\ProgramData\SofCat\config.yaml` |
| macOS | `/Library/Application Support/SofCat/config.yaml` (root-owned, `0600`) |

A config change takes effect when the service restarts. The minimum is the
repository URL, the machine's manifest and the catalogs to search:

```yaml
url: https://example.com/sofcat/
manifest: example_manifest
catalogs:
  - example_catalog
```

## Example files

The repository's [`examples/`](https://github.com/hurricanehrndz/sofcat/tree/main/examples)
directory has an annotated file for each format. They are the reference for
every key.

| File | What it shows |
| --- | --- |
| [`example_config.yaml`](https://github.com/hurricanehrndz/sofcat/blob/main/examples/example_config.yaml) | The agent's `config.yaml`: repository, credentials, service settings and the `branding:` block. |
| [`example_manifest.yaml`](https://github.com/hurricanehrndz/sofcat/blob/main/examples/example_manifest.yaml) | A manifest: managed, optional and default installs, updates, uninstalls and `conditional_items`. |
| [`example_catalog.yaml`](https://github.com/hurricanehrndz/sofcat/blob/main/examples/example_catalog.yaml) | A catalog as `makecatalogs` writes it: installers, checks, dependencies, action labels and required software. |
| [`example_package-info.yaml`](https://github.com/hurricanehrndz/sofcat/blob/main/examples/example_package-info.yaml) | One item's package info, the input to `makecatalogs`. |
| [`placements.yaml`](https://github.com/hurricanehrndz/sofcat/blob/main/examples/placements.yaml) | Placements for zip items. See [Zip items](/guides/zip-items/). |
| [`zip-items/`](https://github.com/hurricanehrndz/sofcat/tree/main/examples/zip-items) | A zip item's source, rendered tree and generated catalog entry. |
| [`brand/`](https://github.com/hurricanehrndz/sofcat/tree/main/examples/brand) | A rebrand: your own name, icon, service and installer. |

## Branding

The storefront's title, tagline, logo, help link, accent colour, list heading
and section tab order come from policy values or the `branding:` block in
`config.yaml`, with no rebuild. On Windows, policy values under
`HKLM\SOFTWARE\Policies\SofCat\Branding` override the config. The keys and
their limits are in the UI's
[branding reference](https://github.com/hurricanehrndz/sofcat/blob/main/ui/README.md#branding).

To ship SofCat under your own name, icon, service and installer instead,
build a rebrand from
[`examples/brand/`](https://github.com/hurricanehrndz/sofcat/tree/main/examples/brand).

## Conditions

Items can be limited to machines whose facts match an expression, in a
catalog's `installable_condition` or a manifest's `conditional_items`. See
[Installable conditions](/guides/conditions/).
