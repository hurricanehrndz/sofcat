---
title: Tree templates
description: Render files inside a zip item's tree on the device, with the facts of the run.
---

A file inside a tree whose name ends in `.tmpl` is a template for the file
of the same name without the suffix, rendered on the device. sofcat renders
it after the archive's hash checks and before the swap, writes the output
beside it, and leaves the `.tmpl` in the placed tree, so a later run can
re-render in place without the archive ([Installing](/zip-items/overview/#installing), steps 2 and 3). The
device reads templates from the tree it is placing and nowhere else. The
engine, its three functions, the limits, the error positions and the `.json`
canonicalization are those of [Templates for placement files](/zip-items/placement-files/#templates-for-placement-files); a failed render
refuses the tree with `placement_refused`, writes nothing, and the next run
retries. A template whose output is empty or only whitespace writes an empty
file for a path that does not end in `.json`, and is refused for a `.json`
path. A tree that ships both `x` and `x.tmpl`, a file named just `.tmpl`,
or an `x.tmpl.tmpl` is refused by `makecatalogs build` and `zip-item`, by
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

### What the receipt records

Each rendered file gets a `rendered` record in the receipt ([Receipts](/zip-items/overview/#receipts)):

```json
{
  "file": "pack.json",
  "template": "pack.json.tmpl",
  "inputs": {
    "os": "windows",
    "scope": "machine",
    "user": "",
    "item": {"name": "acme-wallpapers", "version": "1.0.0", "managed": true},
    "facts": {"assigned_user": "jdoe", "os": "windows"}
  }
}
```

`facts` holds only the facts the render read, through `fact` or inside a
`cond` expression, with their values. On every run sofcat compares each
record's inputs with the current `os`, `scope`, `user` and item fields and,
for each fact the record names, that fact's current value, as canonical
JSON. A difference re-renders that file in place ([Installing](/zip-items/overview/#installing), step 2) and
leaves every other file alone, so a fact the template never read is not an
input: a new IP address or hostname changes nothing. A record whose fact the
machine no longer has re-renders and fails, naming the fact, until the fact
is back.

### Rendering in CI

A brand repository that renders its own variants of a tree in CI with a
`{{ }}` engine (Go templates, Jinja, Mustache) runs over the device
templates too and would consume them, shipping a `.tmpl` with nothing left
for the device to render. Keep the device's actions out of that pass: a raw
block (`{{ "{{" }}` in Go templates, `{% raw %}` in Jinja), another
delimiter for the CI pass, or a CI step that copies `.tmpl` files past the
engine.

### Validation

`makecatalogs build` and `zip-item` render every `.tmpl` in each tree they pack, per OS
variant, with `.Item.Managed` true and false, `.User` a sample name in user
scope, and the sample built-in facts of that OS overlaid with `check_facts`
(a template that reads `fact "assigned_user"` needs
`check_facts: {assigned_user: jdoe}`). A
failure fails the pack, naming the item, the OS and the template, and
nothing is written. `makecatalogs -check` opens every zip the repository
holds and renders its `.tmpl` entries the same way.
