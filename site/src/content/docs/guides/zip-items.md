---
title: Zip items
description: Place directory trees such as AI skills and plugins from deterministic zips, per user or per machine.
---

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

- `variants` maps a placement name from `placements.yaml` to an OS, `darwin` or
  `windows`, to one zip.
- `location` is the zip's path in the repository, `hash` its sha256 and
  `tree_hash` the tree hash of its contents.
- The zip's root holds the item's files directly, such as `SKILL.md`, with
  no `<item>/` folder around them. The tree is placed at
  `<placement path>/<item_name>`, or `<placement path>/<items_dir>/<item_name>`
  when the placement sets `items_dir`.
- A zip item needs no `check` block and no `uninstaller`.

Do not write the `installer` block by hand. `makecatalogs zip-item` writes it.
## Installing

sofcat installs a zip item by placing one tree per placement for the OS it
runs on, in `placements.yaml` order. The service skips placements whose scope
on this OS is `user`; they neither fail nor succeed the item. Instead, after
each run that is not check-only, it stages their verified archives in
`user-artifacts` inside its data directory, the one entry there users may
read ([Data directory](/reference/data-directory/)), each named `<hash>.zip` after
its sha256 (never after its catalog `location`), removes archives no entry
references, and then stores the plan, built from that run's desired set
(dependencies and `update_for` riders included), as `user-plan.json` in its
data directory. An entry whose archive fails to stage is stored held, so the
helper leaves that item's trees as they are until the archive stages. The
per-user helper, `<slug>-useragent.exe`, fetches that stored plan, so it
needs no repository access, and places the trees in each user's profile under the
`windows_user` roots, with receipts in `%LOCALAPPDATA%\<NAME>\receipts`, and
writes the placements' `files` (Placement files)
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
can delete their executables.

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

### Placement files

A placement may list `files`: files sofcat renders from the placement's
receipts, such as a marketplace catalog or a settings drop-in that tells a
harness where the trees are. After every change to the placement (an
install, an upgrade, an adoption, a removal, or a receipt whose `managed`
flag changed), sofcat rewrites them from that placement's receipts, not from
the run, so a held or failed item keeps its entry. Every run also rewrites
them, so a deleted or refused file is restored on the next run. The files
have no receipts: sofcat owns them, and status and scope exit ignore them.

Each entry has a `template` (see Templates for placement files) and exactly
one of:

- `path`, relative to the placement root and slash-separated, written on
  every OS the placement has a path on. Every segment must be one Windows
  can hold. It must not reach into an item tree: with `items_dir` its first
  segment differs from `items_dir`'s, ignoring case, and without it the path
  starts with a `.` segment, which no item name has. A first segment
  starting with `.<slug>-` or `.sofcat-` is sofcat's own and refused.
- `paths`, one path per OS, like the placement's own `paths`, for a file
  outside the root. Its OSes must be among the placement's; an OS left out
  gets no file. A path that resolves inside the root is held to the rules
  for `path`. Two entries may not name the same file.

sofcat checks and renders every entry with a path on this OS, then writes
each through a temporary file in the same directory, creating the directory
if needed, or removes the file when the output is empty. Each path passes the
same write-root and data and binary directory checks as a placement path. An
entry whose path is refused or whose template fails to render keeps its file
as it was, and the other entries are still written. Set `files_atomic: true`
to write none of them unless every entry succeeds. Either way the item that
triggered the rewrite fails with `placement_refused`, and the per-run rewrite
reports it under the placement's name, action `generate`, until it is fixed.
Two entries naming the same file write none.

In `user` scope the per-user helper does all of this instead of the service:
the stored plan carries every user-scope placement's spec, so `exclusive`,
`items_dir`, `vars` and `files` work there as in machine scope. The helper
renders the files from this user's receipts, after each change to the
placement and on every sync for each placement the user holds a receipt in,
so a deleted file is back at the next sync. A
placement this user has no item in gets no file, even when its root exists.
A failure is reported under the placement's name with no item, in the
service log. A `paths` entry may use `${USERPROFILE}` only on an OS where
the placement's scope is `user`.

`exclusive` in a profile directory is strict: once the user puts a file of
their own in the root before the helper has created and marked it, the
helper refuses that placement on every sync, for that user, until the
directory is empty again. The user sees nothing; the only sign is a warning
in the service log. Point an exclusive user-scope placement at a directory
nothing else uses, not at a tool's own directory such as
`%USERPROFILE%\.codex`.

#### A Claude Code marketplace

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

### Templates for placement files

Every file a placement lists is rendered from its template in
`placements.yaml`. Each is rendered from the placement's receipts after every change
there and once per run, and removed when the output is empty. Templates are
Go [`text/template`](https://pkg.go.dev/text/template) with
`missingkey=error`, so a field the context lacks fails the render instead of
printing `<no value>`. A template cannot read files, include other files or
reach the network. Templates live inline in `placements.yaml` as block
scalars (`|`).

The context is:

| Field | Meaning |
|---|---|
| `.OS` | `windows` or `darwin`. |
| `.Scope` | `machine` or `user`: `user` when the per-user helper writes the file. |
| `.Placement` | The placement the file belongs to: `.Name`, `.Root` (the expanded placement root, whichever path the file has) and `.Vars`, the placement's `vars` as written in `placements.yaml` (`.Vars.owner.name` reads a key of a map; `index .Placement.Vars "key"` reads a key that may be missing). |
| `.Items` | The receipts of the placement, sorted by item: `.Name`, `.Version`, `.Managed`, `.Path`, `.TreeHash`, `.PlacedHash`. `{{ json .Items }}` emits them with the receipt's field names (`name`, `version`, `managed`, `path`, `tree_hash`, `placed_hash`). |
| `.User` | In user scope, the login name of the account the helper runs as, without domain, as for tree templates; empty in machine scope. |

`.PlacedHash` is the hash of the tree as placed, rendered files included, so
a catalog template can stamp a version that changes when a rendered file
does, for harnesses that reload a plugin only on a version change.

Facts are reached through functions, not a field. Three functions join
`text/template`'s own (`printf`, `slice`, `index`, `len`, `range`, `if`,
variables and the rest):

- `json v` encodes any value as a JSON literal: `{{ json .Items }}`,
  `{{ json (fact "ipv4_address") }}`, `{{ json .Placement.Vars.owner }}`.
- `fact "name"` returns that fact's value, of whatever type the fact has:
  a string, a number, a bool, a list, or the JSON value an admin script
  printed. `{{ fact "os" }}` prints `windows`; `{{ if fact "laptop" }}`
  branches on a bool; `{{ index (fact "ipv4_address") 0 }}` reads a list.
  A fact this machine does not have fails the render, naming it.
- `cond "expression"` evaluates an [`expr` condition](/guides/conditions/) against
  the facts, with exactly the semantics of `conditional_items`, `version()`
  and the comparison overloads included, and returns true or false:
  `{{ if cond "os == \"windows\" && os_build >= 22000" }}`. A syntax error,
  a runtime error, a non-boolean result, or a fact the machine does not
  have fails the render, naming the expression and the missing facts. An
  expression that calls a time-dependent builtin such as `now()` is rejected
  when the template is parsed, by `makecatalogs -check` and on the device:
  its result would not follow from the facts, so a re-render could never
  settle. That parse-time check covers a literal `cond "..."` call; an
  expression built at render time, from a variable or a pipeline, is
  rejected when it runs.

The facts are those of the run: the built-in facts and the admin scripts'
output, collected once per run, as `installable_condition` and
`conditional_items` see them. A render records which facts it read.

A render fails when the template does not parse, exceeds 256 KiB, the
output exceeds 1 MiB, the render takes longer than 5 seconds, or the
template returns an error. The error names the template and, when the
engine reports them, the line and column. A failed render refuses the
placement (`placement_refused`), writes nothing for that file, and is
reported like any other refusal, under the item that triggered the rewrite
or, for the per-run rewrite, under the placement's name with action
`generate`; the next run retries.

When the output path ends in `.json`, the output must parse as one JSON
value and sofcat writes it in canonical form: object keys sorted, two-space
indent, numbers kept as written, a trailing newline and no HTML escaping, so
the bytes do not depend on the template's whitespace. Any other path is
written as rendered. An output that is empty or only whitespace counts as
empty, and sofcat removes the file.

#### A Codex-format catalog

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

#### An index of a directory placement

A file can describe any placement. The template decides with
`{{ if .Items }}` whether the file persists with nothing placed; here the
file is an index of the skills in `claude-code-skills`, with the OS fact:

```yaml
  claude-code-skills:
    paths:
      darwin: /Library/Application Support/ClaudeCode/.claude/skills
      windows: ${ProgramData}\ClaudeCode\.claude\skills
    files:
      - paths:
          darwin: /Library/Application Support/ClaudeCode/skills-index.json
          windows: ${ProgramData}\ClaudeCode\skills-index.json
        template: |
          {{- if .Items }}
          {
            "os": {{ json (fact "os") }},
            "root": {{ json .Placement.Root }},
            "skills": {{ json .Items }}
          }
          {{- end }}
```

With one skill placed the file reads
`{"os": "windows", "root": "C:\\ProgramData\\ClaudeCode\\.claude\\skills", "skills": [{"name": "hello-world", "version": "1.0.0", "managed": true, …}]}`,
canonically indented; when the last skill is removed the template renders
nothing and sofcat removes the file.

#### check_facts

`makecatalogs -check` renders every placement template, and every tree
template in the repository's zips, without a machine (see
[makecatalogs -check](#makecatalogs--check)); `makecatalogs zip-item` renders
the tree templates it packs the same way. Their facts are a sample value for
every built-in fact of the OS, the example values of the
[conditions guide's table](/guides/conditions/#built-in-facts), overlaid with the
top-level `check_facts` map in `placements.yaml`: the admin facts the
templates read, with values to check them against.

```yaml
check_facts:
  assigned_user: jdoe
placements:
  …
```

A template that reads a fact no sample and no `check_facts` entry provides
fails the check, as it would fail on a device without that fact.

### Tree templates

A file inside a tree whose name ends in `.tmpl` is a template for the file
of the same name without the suffix, rendered on the device. sofcat renders
it after the archive's hash checks and before the swap, writes the output
beside it, and leaves the `.tmpl` in the placed tree, so a later run can
re-render in place without the archive (Installing, steps 2 and 3). The
device reads templates from the tree it is placing and nowhere else. The
engine, its three functions, the limits, the error positions and the `.json`
canonicalization are those of Templates for placement files; a failed render
refuses the tree with `placement_refused`, writes nothing, and the next run
retries. A template whose output is empty or only whitespace writes an empty
file for a path that does not end in `.json`, and is refused for a `.json`
path. A tree that ships both `x` and `x.tmpl`, a file named just `.tmpl`,
or an `x.tmpl.tmpl` is refused by `makecatalogs zip-item`, by
`makecatalogs -check` and by the device.

The context is:

| Field | Meaning |
|---|---|
| `.OS` | `windows` or `darwin`. |
| `.Scope` | `machine` or `user`. |
| `.Placement` | The tree's placement: `.Name`, `.Root` (the expanded placement root) and `.Vars`, as for placement files. |
| `.Item` | The item whose tree this is: `.Name`, `.Version`, `.Managed` (true when a manifest lists it in `managed_installs`), `.Path` (the tree's directory) and `.TreeHash` (the catalog's hash). There is no `.PlacedHash`: it is the hash of the output. |
| `.User` | In user scope, the login name of the account the helper runs as, without domain (`alice`, not `CORP\alice` or `alice@corp.example.com`); empty in machine scope. |

`.Items` is absent. Facts come through `fact` and `cond`,
with the run's facts: the service renders machine-scope trees with the facts
it collected for the run, and hands the helper the same map with the
user-placement plan, so a user-scope tree renders with the same values and
the helper runs no script. A fact value a machine-scope template writes
lands in a machine-wide directory that every user of the device can read.
Put a per-user value in a user-scope placement, where the helper writes it
into that user's profile, and keep anything a standard user may not see out
of the admin scripts altogether ([Conditions](/guides/conditions/#admin-provided-facts)).

#### What the receipt records

Each rendered file gets a `rendered` record in the receipt (Receipts):

```json
{
  "file": ".mcp.json",
  "template": ".mcp.json.tmpl",
  "inputs": {
    "os": "windows",
    "scope": "machine",
    "user": "",
    "item": {"name": "acme-desktop", "version": "1.0.0", "managed": true},
    "facts": {"assigned_user": "jdoe", "os": "windows"}
  }
}
```

`facts` holds only the facts the render read, through `fact` or inside a
`cond` expression, with their values. On every run sofcat compares each
record's inputs with the current `os`, `scope`, `user` and item fields and,
for each fact the record names, that fact's current value, as canonical
JSON. A difference re-renders that file in place (Installing, step 2) and
leaves every other file alone, so a fact the template never read is not an
input: a new IP address or hostname changes nothing. A record whose fact the
machine no longer has re-renders and fails, naming the fact, until the fact
is back.

#### A Claude Desktop plugin

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

#### Rendering in CI

A brand repository that renders its own variants of a tree in CI with a
`{{ }}` engine (Go templates, Jinja, Mustache) runs over the device
templates too and would consume them, shipping a `.tmpl` with nothing left
for the device to render. Keep the device's actions out of that pass: a raw
block (`{{ "{{" }}` in Go templates, `{% raw %}` in Jinja), another
delimiter for the CI pass, or a CI step that copies `.tmpl` files past the
engine.

#### Validation

`makecatalogs zip-item` renders every `.tmpl` in each tree it packs, per OS
variant, with `.Item.Managed` true and false, `.User` a sample name in user
scope, and the sample built-in facts of that OS overlaid with `check_facts`
(the `.mcp.json` above needs `check_facts: {assigned_user: jdoe}`). A
failure fails the pack, naming the item, the OS and the template, and
nothing is written. `makecatalogs -check` opens every zip the repository
holds and renders its `.tmpl` entries the same way.

### Receipts

Each placed tree has a JSON receipt at
`<data dir>/receipts/<placement>/<item_name>.json`:

```json
{
  "item": "hello",
  "version": "1.0.0",
  "placement": "claude-skills",
  "path": "C:\\ProgramData\\Acme\\skills\\hello",
  "tree_hash": "sha256:ffaa66a8…",
  "placed_hash": "sha256:ffaa66a8…",
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
render used (Tree templates, What the receipt records). With nothing
rendered, `placed_hash` equals `tree_hash` and
`rendered` is `[]`, present rather than omitted. `managed` is true when a
manifest lists the item in `managed_installs`. A receipt written before
`placed_hash` existed is read as if `placed_hash` were its `tree_hash`. A
run that finds nothing to do leaves the receipt untouched, while a `managed`
flip or a catalog version bump with the same contents rewrites only the
receipt.

### Write roots

A build writes only under its write roots. A brand sets them in
`write-roots.yaml`, compiled into the binaries by `just brand=<dir> build`:

```yaml
darwin: [/Library/Application Support/Acme]
windows: ['${ProgramData}\Acme']
windows_user: ['${USERPROFILE}\.agents']
```

Without the file, a build uses `${ProgramData}\<NAME>` on Windows and
`/Library/Application Support/<NAME>` on macOS, the directory holding its
config and `Data`, and has no user roots. Runtime config cannot change them.
sofcat resolves symlinks in a placement path, then refuses it
(`placement_refused`) when it is outside every root for its scope, inside
sofcat's data directory, or inside, equal to or around a directory holding
sofcat's binaries, so the stock root never overlaps `Data` or
`${ProgramFiles}\<NAME>`. An `exclusive` placement that exists, is not empty
and lacks the marker file `.<slug>-owned` is refused too; a root sofcat
creates gets the marker. A repository without `placements.yaml` fails every
zip item with `placement_refused`.

### Zip-only builds

A brand whose `brand.env` sets `INSTALLERS=zip` builds all three binaries with the
`zip_only` Go build tag. Such a binary has no code for the `msi`, `exe`, `ps1`,
`msix` and `nupkg` installers or for catalog scripts. It treats a catalog item
as invalid when it has an `installer.type` other than `zip`, an
`uninstaller.type` set to anything but `zip`, or any of `preinstall_script`,
`postinstall_script`, `preuninstall_script`, `postuninstall_script` and
`check.script`. It logs the reason (`rejected by this zip-only build`) once per
item and reason for the life of the process, the run neither installs nor
removes the item, `ListOptionalInstalls` leaves it out and `InstallItem` refuses it with `item_not_available`. Admin
fact scripts (`<data dir>/conditions`) still run.

## Required software

Any catalog item, a zip item or not, can list software it needs and sofcat
never installs:

```yaml
requires_software:
  - display_name: Demo Tool
    chocolatey: demo-tool
    munki: DemoTool
```

- `display_name` is required. It names the software in the deferral message.
- `chocolatey` is a package id, checked on Windows: present when
  `%ProgramData%\chocolatey\lib\<id>` is a directory. A nupkg item that
  sofcat installed does not count ([Nupkg items](/guides/nupkg-items/#required-software)).
- `munki` is a Munki item name, checked on macOS: present when it is listed
  under `InstalledItems` in `/Library/Managed Installs/ManagedInstallReport.plist`.
  A missing, unreadable or binary report counts as missing. The macOS smoke
  points sofcat at its own report with the `SOFCAT_TEST_MUNKI_REPORT`
  environment variable in the daemon's plist; it is a test hook, not a
  setting.
- An entry with no key for the OS sofcat runs on is skipped.

Before an install or update does anything (download, placement, scripts),
sofcat checks the entries. If any is missing, it defers the item with code
`missing_software` and the message
`Needs Demo Tool. Install it first, then try again.` (several names are
joined with `, `). Check-only runs report the same deferral. Items that
depend on it are deferred with the same code and message. Nothing is
recorded as failed, and the next run checks again. Removals are never
checked.

`makecatalogs -check` reports an entry without `display_name` or with neither
`chocolatey` nor `munki`.

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
  --rendered <dir> --location-prefix <prefix> -o <repo>
```

- `pack` writes the zip and prints `{"hash":"<hex>","tree_hash":"sha256:<hex>"}`.
- `tree-hash` prints the tree hash and writes nothing. Run it on a device to
  compare a placed tree with the catalog.
- Without `--os`, both use the OS they run on: executable bits are kept,
  except on Windows. Pack every `windows` tree with `--os windows`.
- `zip-item` turns rendered trees into a package-info file. `<item.yaml>` is
  a package-info file without `installer`. `<dir>` holds one tree per
  placement and OS, at `<dir>/<placement>/<darwin|windows>/`. CI renders
  those variants with any template engine; a `.tmpl` file a tree ships is
  rendered on the device instead (Tree templates), and `zip-item`
  test-renders it. Each tree is packed, `windows` trees with
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
  `.Item.Managed` either way or with the sample user (Tree templates,
  Validation); the error names the item, the OS and the template

[examples/zip-items/](https://github.com/hurricanehrndz/sofcat/tree/main/examples/zip-items) holds a rendered tree, the
input, and the output. To regenerate the output:

```sh
makecatalogs zip-item --placements examples/placements.yaml \
  --item examples/zip-items/hello-world.yaml \
  --rendered examples/zip-items/rendered/hello-world \
  --location-prefix packages/skills -o examples/zip-items/generated \
  > examples/zip-items/generated/hello-world.yaml
```

## placements.yaml

`placements.yaml` sits at the repository root, next to `catalogs/` and
`manifests/`. It names the placements. Unknown fields are rejected.
[examples/placements.yaml](https://github.com/hurricanehrndz/sofcat/blob/main/examples/placements.yaml) shows every field.

```yaml
placements:
  codex-skills:
    paths:
      darwin: /etc/codex/skills
      windows: ${USERPROFILE}\.agents\skills
    scope: {windows: user}
    applies_to: [Codex]
```

| Field | Meaning |
|---|---|
| key | The placement name. Variants and rendered trees use it. |
| `paths` | The placement root, keyed by `darwin` and `windows`. Every placement is a directory: one tree per item at `<root>/<item_name>`. |
| `scope` | `machine` or `user`, keyed like `paths`. An OS left out is `machine`. `darwin: user` is not supported. In `user` scope the per-user helper places the trees and writes the `files`. |
| `applies_to` | Display names, such as `[Claude Code]`. The storefront shows a line such as "Applies to: Claude Code" for an item (see Storefront labels). Display only: it does not limit where an item is placed. |
| `action_labels` | Optional storefront label overrides for the placement's items. Keys: `install`, `installing`, `installed`, `remove`, `removing`, `update`, `cancel`, `pending_install`, `pending_remove`, `applies_to`. |
| `exclusive` | Optional. When true, sofcat does not write into an existing, non-empty root it did not create. |
| `items_dir` | Optional. Item trees go to `<root>/<items_dir>/<item_name>` instead. A clean relative path, slash-separated, each segment one Windows can hold, whose first segment does not start with `.`. |
| `vars` | Optional. Any mapping, read by the placement's templates as `.Placement.Vars`. |
| `files` | Optional. Files rendered from the placement's receipts, each a `template` with a relative `path` or per-OS `paths` (Placement files). |
| `files_atomic` | Optional, with `files`. When true, no file is written unless every one renders and passes its checks; otherwise each is written on its own. |
| `check_facts` | Top level, beside `placements`: facts for `makecatalogs -check` over the built-in samples, for the admin facts templates read. |

Windows paths may use `${ProgramFiles}` and `${ProgramData}`. A path may use
`${USERPROFILE}` only when its OS has `user` scope.

Where order matters, placements are taken in file order.

### Storefront labels

Each label key resolves from the item's own `custom_action_labels` (the same
ten keys), then from the first of the item's placements, in file order,
whose `action_labels` sets it, then from the storefront's built-in label.
Only placements with a variant for the machine's OS count, for labels and for
`applies_to`. An item's applies-to line lists the union of those placements'
`applies_to`, in file order, without repeats, after the `applies_to` label
("Applies to" unless a layer sets it). The line is only text: it names where
the item is meant to be used and never scopes the item, so a label such as
"Works in" may read better. For example:

```yaml
# placements.yaml
placements:
  tapas-marketplace:
    applies_to: [Claude Code]
    action_labels: {install: Make available, remove: Withdraw, applies_to: Works in}
    # paths, items_dir, vars and files as above

# packages-info/tapas-optional-plugin.yaml
custom_action_labels: {install: Enable for me}
```

The storefront shows "Enable for me" from the item, "Withdraw" from the
placement, and the built-in "Cancel", since neither sets `cancel`. The remove
label stays "Remove" unless a layer sets `remove`. The applies-to line reads
"Works in: Claude Code", from the placement.

`pending_install` and `pending_remove` word the status of a request waiting
for the next run ("Will be installed", "Will be removed"), which the card
shows beside its Cancel button. Set them along with `install` and `remove`,
so the status reads in the same words as the button that made the request.

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
  built-in fact of that OS overlaid with `check_facts` (see Templates for
  placement files). A missing fact, a `cond` naming a fact no sample
  provides, a `cond` calling `now()`, and output that is not JSON on a
  `.json` path all fail.
- a tree template in a zip that fails to render: every zip a zip item names
  that the repository holds is opened, and each `.tmpl` entry is rendered
  per variant as `zip-item` renders it (Tree templates, Validation); a zip
  that ships `x` beside `x.tmpl`, a bare `.tmpl` or an `x.tmpl.tmpl` fails
  too. A zip hosted outside the repository is not checked.

A normal run warns about these; `makecatalogs -check` fails on them.
