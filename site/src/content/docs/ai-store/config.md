---
title: Config files
description: Write the machine-wide config files of Claude Code and Codex from templates and the device's facts.
---

sofcat can own a tool's machine-wide config file, such as Claude Code's
`managed-settings.json` or Codex's `/etc/codex/config.toml`. The file is a
[placement file](/zip-items/placement-files/) rendered from a
template, so its values can depend on the device's facts: who the device is
assigned to, or which groups they are in.

These placements run on macOS in sofcat's own test loop. Nobody has tested
the Windows config paths, so add `windows` paths at your own risk.

## One placement and one item per tool

A placement file exists while its placement holds an item. So each tool gets
a placement that lists its config files, and a small item whose only job is
to be placed there:

```yaml
# placements.yaml
check_facts:
  assigned_user: jdoe
  memberof: [staff, harness-beta]
placements:
  harness-claude-code:
    paths:
      darwin: /Library/Application Support/Acme/harness/claude-code
    applies_to: [Claude Code]
    files:
      - paths:
          darwin: /Library/Application Support/ClaudeCode/managed-settings.json
        template_file: templates/managed-settings.json.tmpl
  harness-codex:
    paths:
      darwin: /Library/Application Support/Acme/harness/codex
    applies_to: [Codex]
    files:
      - paths:
          darwin: /etc/codex/config.toml
        template_file: templates/codex-config.toml.tmpl
      - paths:
          darwin: /etc/codex/managed_config.toml
        template_file: templates/codex-managed-config.toml.tmpl
```

The placement's own path is a directory under the brand's root, where the
item's tree lands. The config files are `paths` entries outside it, which
the build's [write roots](/ai-store/overview/#write-roots) must allow.

The items are ordinary zip items. `claude-code-config` holds a README, and
`codex-models` holds Codex's model catalog. Put both in `managed_installs`.

Every template starts with `{{ if .Items }}`. With the item placed, the
template renders and sofcat writes the file. Take the item out of the
manifest and the template renders nothing, so sofcat removes the file.

## A template that picks a profile

```
{{- /* One profile per group, in priority order: labs, beta, early. A user
       in none of them gets the default. */ -}}
{{- if .Items }}
{{- $profile := "default" }}
{{- $baseURL := "https://ai.example.com/claude" }}
{{- $model := "example-standard" }}
{{- if cond "\"harness-labs\" in memberof" }}
{{- $profile = "labs" }}
{{- $baseURL = "https://labs.ai.example.com/claude" }}
{{- $model = "example-preview" }}
{{- else if cond "\"harness-beta\" in memberof" }}
{{- $profile = "beta" }}
{{- $baseURL = "https://beta.ai.example.com/claude" }}
{{- $model = "example-next" }}
{{- else if cond "\"harness-early\" in memberof" }}
{{- $profile = "early" }}
{{- $model = "example-next" }}
{{- end }}
{
  "env": {
    "ANTHROPIC_BASE_URL": {{ json $baseURL }},
    "ANTHROPIC_CUSTOM_HEADERS": {{ json (printf "x-example-user: %s\nx-example-profile: %s" (fact "assigned_user") $profile) }}
  },
  "model": {{ json $model }},
  "permissions": {
    "deny": ["Read(./.env)"]
  }
}
{{- end }}
```

`cond` evaluates a [condition expression](/guides/conditions/#expressions)
against the device's facts, and `fact` returns one fact's value. The first
branch that matches wins, so a user in both `harness-early` and
`harness-labs` gets the labs profile. With the `check_facts` above, the
device gets:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://beta.ai.example.com/claude",
    "ANTHROPIC_CUSTOM_HEADERS": "x-example-user: jdoe\nx-example-profile: beta"
  },
  "model": "example-next",
  "permissions": {
    "deny": [
      "Read(./.env)"
    ]
  }
}
```

sofcat rewrites a `.json` output in canonical form, so the template's
whitespace and key order do not reach the file.

## TOML, and a file too big to render

```
{{- if .Items -}}
# Written by sofcat. Do not edit.
model = "example-codex"
model_provider = "example-gateway"
model_catalog_json = {{ json (printf "%s/%s/model_catalog.json" .Placement.Root (index .Items 0).Name) }}

[model_providers.example-gateway]
name = "Example gateway"
base_url = "https://ai.example.com/codex/v1"
http_headers = { "x-example-user" = {{ json (fact "assigned_user") }} }
{{ end -}}
```

sofcat writes any file that is not `.json` exactly as the template emits it,
so whitespace counts. The `{{-` and `-}}` trims above keep the file from
starting or ending with a blank line. `json` quotes a string the way TOML
accepts for these values.

A rendered file is limited to 1 MiB, and a real model catalog is larger. It
ships in the `codex-models` tree instead, and the template builds the path
to the placed copy from `.Placement.Root` and the item's name:

```toml
model_catalog_json = "/Library/Application Support/Acme/harness/codex/codex-models/model_catalog.json"
```

## Facts

sofcat has no built-in `assigned_user` or `memberof`. A script on the device
prints them as one JSON object:

```json
{"assigned_user": "jdoe", "memberof": ["staff", "harness-beta"]}
```

Install it in the `conditions` folder of sofcat's data directory with your
MDM or config management. The repository does not deliver it. The
[example's script](https://github.com/hurricanehrndz/sofcat-harness-example/blob/main/conditions/10-example-facts)
is a sketch for macOS, and
[Admin-provided facts](/guides/conditions/#admin-provided-facts) has the
rules a script must follow.

A template that reads a fact the device does not have fails to render.
sofcat then leaves the existing file as it was and reports
`placement_refused` until the fact is back. Facts are not secret: sofcat
hands them to every signed-in user's helper, and a machine-wide config file
is readable by every user of the device.

## Before you ship it

- Give each target file to one placement. sofcat does not stop two
  placements from naming the same path.
- If other management tooling writes the same file today, turn that off
  first.
- Add every admin fact a template reads to `check_facts`, so
  `makecatalogs -check` can render it in CI.
- Edits made on the device do not last. sofcat rewrites placement files on
  every run.
