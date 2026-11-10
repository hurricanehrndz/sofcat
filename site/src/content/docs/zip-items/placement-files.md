---
title: Placement files
description: Files sofcat renders from a placement's receipts, and the templates that produce them.
---

These are the files a placement's `files` list writes, such as a marketplace catalog or a settings drop-in, and the templates that render them.

## Placement files

A placement may list `files`: files sofcat renders from the placement's
receipts, such as a marketplace catalog or a settings drop-in that tells a
harness where the trees are. After every change to the placement (an
install, an upgrade, an adoption, a removal, or a receipt whose `managed`
flag changed), sofcat rewrites them from that placement's receipts, not from
the run, so a held or failed item keeps its entry. Every run also rewrites
them, so a deleted or refused file is restored on the next run. The files
have no receipts: sofcat owns them, and status and scope exit ignore them.

Each entry has a template (see Templates for placement files): either
`template`, the text inline, or `template_file`, the path of a file in the
repository that holds it. Set exactly one of the two. Each entry also has
exactly one of:

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
service log. A `paths` entry may use the user's home, `${USERPROFILE}` on Windows or
`${HOME}` on macOS, only on an OS where the placement's scope is `user`.

`exclusive` in a profile directory is strict: once the user puts a file of
their own in the root before the helper has created and marked it, the
helper refuses that placement on every sync, for that user, until the
directory is empty again. The user sees nothing; the only sign is a warning
in the service log. Point an exclusive user-scope placement at a directory
nothing else uses, not at a tool's own directory such as
`%USERPROFILE%\.codex`.

[Plugins and marketplaces](/ai-store/plugins/) has complete placements that
write a Claude Code marketplace catalog, its settings drop-in and a
Codex-format catalog. [Config files](/ai-store/config/) writes a tool's
machine-wide config the same way.

## Templates for placement files

Every file a placement lists is rendered from its template. Each is
rendered from the placement's receipts after every change there and once
per run, and removed when the output is empty. Templates are
Go [`text/template`](https://pkg.go.dev/text/template) with
`missingkey=error`, so a field the context lacks fails the render instead of
printing `<no value>`. A template cannot read files, include other files or
reach the network.

A template lives inline in `placements.yaml`, as a block scalar (`|`) under
`template`, or in a file of its own that the entry names with
`template_file`:

```yaml
files:
  - path: .claude-plugin/marketplace.json
    template_file: templates/marketplace.json.tmpl
```

`template_file` is a path relative to the repository root, the directory
that holds `placements.yaml`. It is slash-separated, with no leading `/`, no
`\` and no `.` or `..` segment, and every segment must be one Windows can
hold. Publish the file with the repository: a device fetches
`<url><template_file>` each time it reads `placements.yaml`, once per
distinct path, and `makecatalogs` reads it from the repository on disk. The
file's text is the entry's template, under every rule on this page, and the
same text inline writes the same bytes.

The reference is resolved when the table is read, not at render time, so a
template still cannot read or include a file. A template file that cannot be
fetched, a 404 included, that is empty or that exceeds 256 KiB makes the
whole table invalid, as an invalid `placements.yaml` does: the run fails and
no placement file changes. The error names the placement, the entry and the
URL. `makecatalogs -check` reports a missing or unreadable template file the
same way and renders a template file with the same samples as an inline
template. In user scope the service resolves the reference and the stored
plan carries the text, so the per-user helper never fetches.

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

### An index of a directory placement

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

### check_facts

`makecatalogs -check` renders every placement template, and every tree
template in the repository's zips, without a machine (see
[makecatalogs -check](/zip-items/building/#makecatalogs--check)); `makecatalogs zip-item` renders
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
