---
title: Skills
description: Ship a skill to Claude Code and Codex as one zip item, per machine or per user.
---

A skill is a directory with a `SKILL.md` in it. As a zip item it needs a
placement for each directory a tool reads skills from, and one tree that
goes to all of them.

## Placements

```yaml
# placements.yaml
placements:
  claude-code-skills:
    paths:
      darwin: /Library/Application Support/ClaudeCode/.claude/skills
      windows: ${ProgramData}\ClaudeCode\.claude\skills
    applies_to: [Claude Code]
  codex-skills:
    paths:
      darwin: /etc/codex/skills
      windows: ${USERPROFILE}\.agents\skills
    scope:
      windows: user
    applies_to: [Codex]
```

Each skill lands at `<path>/<item_name>`. `applies_to` is the text of the
storefront's "Applies to" line and does not limit where an item goes.

`codex-skills` is per machine on macOS and per user on Windows. In user
scope the service does not write the tree. It stages the archive, and the
per-user helper places it in each signed-in user's profile, under a
`windows_user` or `darwin_user` write root. See
[Installing](/zip-items/overview/#installing).

## The item

The item's source is a package-info file with no `installer` block, and
`placements` naming where it goes:

```yaml
# items/example-skill.yaml
item_name: example-skill
display_name: Example Skill
section: Skills
description: A skill for Claude Code and Codex
catalog: harness
version: 1.0.0
placements: [claude-code-skills, codex-skills]
```

and one tree:

```
item-trees/example-skill/
  SKILL.md
```

One item covers both tools. The whole tree goes to every placement the item
lists, on every OS the placement has a path for. To word a skill differently
for each tool or OS, ship `SKILL.md.tmpl`, a
[tree template](/zip-items/tree-templates/) that reads `.Placement.Name` and
`.OS`. The tree holds the skill's files directly, with no `example-skill/`
folder around them.

`makecatalogs build` packs the tree and writes the `installer` block
([Build and publish](/ai-store/overview/#build-and-publish)).

## Changing a skill

Edit the tree, raise `version` and rebuild. The tree's hash changes, and
each device replaces its copy on the next run.

sofcat replaces or removes a tree only while it still matches what sofcat
placed. If a user edits a placed skill, the item fails with
`placement_conflict` and the edited tree stays until someone restores or
deletes it.

A file in a tree whose name ends in `.tmpl` is rendered on the device, with
the device's facts and, in user scope, the user's login name. See
[Tree templates](/zip-items/tree-templates/).
