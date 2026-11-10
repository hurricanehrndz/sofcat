---
title: Build an AI store
description: Use sofcat to put skills, plugins and config files for Claude Code, Codex and Claude Desktop on every device, managed or self-service.
---

An AI store is a sofcat repository whose items are skills, plugins and config
files for coding agents. A device's manifest decides which ones every user
gets and which ones they can add from the self-service app. Everything in it
is a [zip item](/guides/zip-items/): sofcat places files and renders
templates, and runs no installer.

[sofcat-harness-example](https://github.com/hurricanehrndz/sofcat-harness-example)
is a complete store with one item of each kind. Fork it, or read it beside
these pages. Its values are made up.

| To ship | Read |
|---|---|
| A skill for Claude Code or Codex | [Skills](/ai-store/skills/) |
| A plugin, and the marketplace catalog that lists it | [Plugins and marketplaces](/ai-store/plugins/) |
| A tool's machine-wide config file | [Config files](/ai-store/config/) |

The paths on these pages are examples. Check each tool's documentation for
the directories it reads before you copy one.

## The repository

A store is an ordinary sofcat repository with a `placements.yaml` at its
root. The example keeps its sources like this:

```
placements.yaml       where items land, and the files sofcat renders beside them
templates/            one template per rendered file
items/<item>.yaml     a package-info file without an installer block
rendered/<item>/<placement>/<darwin|windows>/
                      the tree an item places
manifests/            which items are managed and which are optional
conditions/           the fact script the config templates need
```

`items/` and `rendered/` are inputs. The build turns them into the zips,
`packages-info/` and `catalogs/` a device reads.

## Build and publish

For each item, `makecatalogs zip-item` packs its trees and prints its
package-info file with the `installer` block filled in. Then `makecatalogs`
compiles the catalogs, and `makecatalogs -check` validates the result:

```sh
out=public
mkdir -p "$out/manifests" "$out/packages-info"
cp placements.yaml "$out/"
cp -R templates "$out/"
cp manifests/*.yaml "$out/manifests/"
for item in items/*.yaml; do
  name=$(basename "$item" .yaml)
  makecatalogs zip-item --placements placements.yaml --item "$item" \
    --rendered "rendered/$name" --location-prefix packages/harness \
    -o "$out" > "$out/packages-info/$name.yaml"
done
makecatalogs "$out"
makecatalogs -check "$out"
```

`placements.yaml` and `templates/` go into the published directory too. A
device fetches both.

`-check` renders every template without a device. It needs a value for each
fact your own scripts provide, under `check_facts` in `placements.yaml`
([check_facts](/guides/zip-items/#check_facts)). Run the build in CI on every
change and publish the output directory to any static host. The example
publishes to GitHub Pages from its
[workflow](https://github.com/hurricanehrndz/sofcat-harness-example/blob/main/.github/workflows/build.yml).

## Write roots

sofcat writes only under its write roots, and a stock build has one: its own
directory. A store that writes to `/etc/codex` or
`/Library/Application Support/ClaudeCode` needs those roots added, either in
the brand's `write-roots.yaml` at build time or by managed policy:

```yaml
darwin:
  - /Library/Application Support/Acme
  - /Library/Application Support/ClaudeCode
  - /etc/codex
windows:
  - ${ProgramData}\Acme
  - ${ProgramData}\ClaudeCode
windows_user:
  - ${USERPROFILE}\.agents
```

A placement outside every root fails with `placement_refused`. See
[Write roots](/guides/zip-items/#write-roots).

A store that ships nothing but zip items can be a
[zip-only build](/guides/zip-items/#zip-only-builds), which has no code for
installers or catalog scripts.

## Point a device at it

```yaml
# config.yaml
url: https://repo.example.com/
url_packages: https://repo.example.com/
manifest: site_default
catalogs:
  - harness
```

The manifest sorts the items:

```yaml
name: site_default
catalogs:
  - harness
managed_installs:
  - example-skill
  - claude-code-config
  - codex-models
optional_installs:
  - example-plugin
```

Every device gets a managed item and keeps it. An optional item shows in the
self-service app until a user adds it. Templates see the difference as
`.Managed`, which is how a marketplace catalog enables a managed plugin by
default and only offers an optional one.
