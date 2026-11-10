---
title: Plugins and marketplaces
description: Ship plugins as zip items and let sofcat write the marketplace catalogs for Claude Code, Codex and Claude Desktop.
---

A plugin is a zip item like a skill. What it adds is a catalog: the tool
reads a marketplace file that lists the plugins, and sofcat writes that file
from whatever plugins are placed. The catalog is a
[placement file](/guides/zip-items/#placement-files), rendered again after
every install and removal.

## A Claude Code marketplace

Claude Code reads a local marketplace from a directory: a catalog at
`.claude-plugin/marketplace.json` and one tree per plugin, here under
`plugins/`. A managed-settings drop-in registers the marketplace and enables
its managed plugins. This placement writes both; copy it and change the
names and paths:

```yaml
placements:
  acme-marketplace:
    paths:
      darwin: /Library/Application Support/Acme/marketplace
      windows: ${ProgramData}\Acme\marketplace
    applies_to: [Claude Code]
    exclusive: true
    items_dir: plugins
    vars:
      name: acme
      owner: {name: Acme IT}
      # renames: {old-plugin: new-plugin}
      # allowCrossMarketplaceDependenciesOn: [other-marketplace]
    files:
      - path: .claude-plugin/marketplace.json
        template: |
          {
            "name": {{ json .Placement.Vars.name }},
            "owner": {{ json .Placement.Vars.owner }},
            {{- if ne (index .Placement.Vars "renames") nil }}
            "renames": {{ json .Placement.Vars.renames }},
            {{- end }}
            {{- if ne (index .Placement.Vars "allowCrossMarketplaceDependenciesOn") nil }}
            "allowCrossMarketplaceDependenciesOn": {{ json .Placement.Vars.allowCrossMarketplaceDependenciesOn }},
            {{- end }}
            "plugins": [
              {{- range $i, $p := .Items }}{{ if $i }},{{ end }}
              {
                "name": {{ json $p.Name }},
                "source": {{ json (printf "./plugins/%s" $p.Name) }},
                "version": {{ json $p.Version }},
                "defaultEnabled": {{ $p.Managed }}
              }
              {{- end }}
            ]
          }
      - paths:
          darwin: /Library/Application Support/ClaudeCode/managed-settings.d/50-acme.json
          windows: ${ProgramData}\ClaudeCode\managed-settings.d\50-acme.json
        template: |
          {{- if .Items }}
          {
            "extraKnownMarketplaces": {
              {{ json .Placement.Vars.name }}: {"source": {"source": "directory", "path": {{ json .Placement.Root }}}}
            },
            "enabledPlugins": {
              {{- $first := true }}
              {{- range .Items }}{{ if .Managed }}{{ if not $first }},{{ end }}{{ $first = false }}
              {{ json (printf "%s@%s" .Name $.Placement.Vars.name) }}: true
              {{- end }}{{ end }}
            }
          }
          {{- end }}
```

`vars` is free data the templates read as `.Placement.Vars`. The catalog
copies `owner`, and `renames` and `allowCrossMarketplaceDependenciesOn` when
`vars` sets them. The output is canonical JSON, so map keys come out sorted.
Plugins are sorted by name. `version` is the catalog version the receipt
records. Bump it when a plugin's files change, because a harness may not
reload them otherwise. `defaultEnabled` is the receipt's `managed` flag:
true when a manifest lists the item in `managed_installs`. A run updates the
flag only for items it decides. An item held by an unknown
`conditional_items` condition keeps its recorded flag. With no plugin left,
the catalog still lists `"plugins": []`. The `source` of each plugin names
`./plugins/`, the `items_dir`: change both together.

In the drop-in, `path` is `.Placement.Root`, the marketplace's resolved
path. `enabledPlugins` lists only the managed plugins, keyed
`<plugin>@<name>`. When the marketplace holds no plugin, the template
renders nothing and sofcat removes the file.

The templates can also live in files of their own, named by `template_file`
([Templates for placement files](/guides/zip-items/#templates-for-placement-files)).
The [example repository](https://github.com/hurricanehrndz/sofcat-harness-example)
does that.

A plugin's tree goes under the placement like any item's:

```
rendered/example-plugin/acme-marketplace/darwin/
  .claude-plugin/plugin.json
  skills/release-notes/SKILL.md
```

## A Codex-format catalog

Codex reads `.agents/plugins/marketplace.json` from a local marketplace in
its own format. This entry writes it beside the Claude catalog above,
with each plugin's version stamped from its placed hash so Codex reloads a
plugin whose rendered files changed (the fixture in
`test/manual/fixtures/tapas/placements.yaml` and `examples/placements.yaml`
carry the same template):

```yaml
    files:
      - path: .agents/plugins/marketplace.json
        template: |
          {
            "name": {{ json .Placement.Vars.name }},
            "interface": {"displayName": {{ json .Placement.Vars.owner.name }}},
            "plugins": [
              {{- range $i, $p := .Items }}{{ if $i }},{{ end }}
              {
                "name": {{ json $p.Name }},
                "version": "{{ $p.Version }}+{{ slice $p.PlacedHash 7 19 }}",
                "source": {"source": "local", "path": "./plugins/{{ $p.Name }}"},
                "policy": {
                  "installation": {{ if $p.Managed }}"INSTALLED_BY_DEFAULT"{{ else }}"AVAILABLE"{{ end }},
                  "authentication": "ON_INSTALL"
                }
              }
              {{- end }}
            ]
          }
```

`slice $p.PlacedHash 7 19` takes twelve hex digits after the `sha256:`
prefix. With two plugins placed, one managed, the file comes out as:

```json
{
  "interface": {
    "displayName": "Acme IT"
  },
  "name": "acme",
  "plugins": [
    {
      "name": "hello-plugin",
      "policy": {
        "authentication": "ON_INSTALL",
        "installation": "INSTALLED_BY_DEFAULT"
      },
      "source": {
        "path": "./plugins/hello-plugin",
        "source": "local"
      },
      "version": "1.0.0+ffaa66a85382"
    }
  ]
}
```

`codex plugin marketplace add <placement root>` then lists the plugins with
those policies and versions.

## A Claude Desktop plugin

Claude Desktop reads each organization plugin's install behaviour from
`installationPreference` in its `.claude-plugin/plugin.json`. One template
serves managed and self-service plugins alike (the Tapas fixture's
`tapas-desktop-managed` and `tapas-desktop-optional` carry it):

```json
{
  "name": {{ json .Item.Name }},
  "version": {{ json .Item.Version }},
  "description": "A managed Claude Desktop plugin",
  "installationPreference": {{ if .Item.Managed }}"auto_install"{{ else }}"available"{{ end }}
}
```

Moving the item between `optional_installs` and `managed_installs` flips
`.Item.Managed`, and the next run re-renders `plugin.json` in place with the
same `tree_hash` and a new `placed_hash`. sofcat knows nothing of Claude
Desktop's values; the template does.

An MCP server configuration that attributes requests to the device's user
reads a fact an admin script provides and branches on a built-in
(`.mcp.json.tmpl`):

```json
{
  "mcpServers": {
    "chatproxy": {
      "type": "http",
      "url": "https://chatproxy.example.com/mcp",
      "headers": {
        "x-litellm-api-key": "Bearer {{ fact "assigned_user" }}/claudecode",
        "x-platform": {{ if cond "os == \"windows\"" }}"windows"{{ else }}"macos"{{ end }}
      }
    }
  }
}
```

Its record's `facts` is `{"assigned_user": "jdoe", "os": "windows"}`; a new
`assigned_user` from the script re-renders `.mcp.json` and nothing else. In
user scope, `{{ .User }}` gives the login name without a script.

Both files are [tree templates](/guides/zip-items/#tree-templates): they
ship in the plugin's tree with a `.tmpl` suffix and sofcat renders them on
the device.
