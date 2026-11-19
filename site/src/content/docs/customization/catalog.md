---
title: Catalog presentation
description: The catalog keys that group, name and label items in the storefront, and the three branding keys that order its tabs and rename its list heading and filter.
---

The repository decides how items are grouped and worded. You set most of it
on the item, in its package-info file, and three
`branding` keys shape the tabs, the heading and the filter around them. None
of it needs a rebuild.

This item sits under an "Apps" tab, in the Browsers category, with its own
button wording:

```yaml
Firefox:
  display_name: Mozilla Firefox
  description: Fast, private and safe web browser
  section: Apps
  category: Browsers
  developer: Mozilla
  custom_action_labels:
    install: Add to my Mac
    installing: Adding…
    installed: Added
```

The branding keys come from `config.yaml` or managed settings; see
[Delivering settings](/customization/delivery/).

```yaml
branding:
  sections: [Apps, Tools]
  list_heading: Software for your team
  applies_to_filter_label: Works with
```

## Item keys

### `section`

The storefront tab the item appears under. It is free text, like
`category`, and matching ignores case and surrounding spaces. Every
distinct section in the offered items becomes a tab, so one install can hold
an "Apps" store and an "AI" store.

An item with no `section` appears under a "Software" tab. With no sections
at all, "Software" is the only tab. Give every item a section to hide that
tab. The order of the tabs comes from [`sections`](#sections) below.

### `category`

The group an item belongs to within a tab. People pick a category with the
Category select beside the search field, or from the left bar when
`category_filter` is `list`; see [Layout and behaviour](/customization/layout/).
The card and the detail page show the category, and search matches it.

A category is free text. The storefront shows a small built-in glyph for
Browsers, Communication, Development, Media, Productivity, Security and
Utilities, and a generic one for any other.

Leave `category` out and `makecatalogs` can choose one with a model. See
[AI categories](/guides/ai-categories/).

### `applies_to`

`applies_to` is a key of a placement in `placements.yaml`, not of a catalog
item. It holds display names, such as `[Desktop]`, and applies only to zip
items. The storefront shows an item's names as a line such as "Applies to:
Desktop". The line lists the union of the item's placements that have a
variant for the machine's OS, in `placements.yaml` order, without repeats.

The names are display text and never limit where an item is placed. They
also feed the applies-to filter; see
[`applies_to_filter_label`](#applies_to_filter_label). The placement rules
are on [Placements](/zip-items/placements/#storefront-labels).

### `custom_action_labels`

A map that replaces the storefront's button and status words for one item.
Each key is optional, and the value is shown as plain text. The service
accepts these ten keys and ignores any other. `makecatalogs` warns about an
unknown key, and `makecatalogs -check` fails on it.

| Key | Replaces | Built-in label |
|---|---|---|
| `install` | The install button | Install |
| `installing` | The button while the install runs | Installing… |
| `installed` | The status of an installed item | Installed |
| `remove` | The remove button | Remove |
| `removing` | The button while the removal runs | Removing… |
| `update` | The update button | Update |
| `cancel` | The Cancel button | Cancel |
| `pending_install` | The status of an install waiting for the next run | Will be installed |
| `pending_remove` | The status of a removal waiting for the next run | Will be removed |
| `applies_to` | The prefix of the applies-to line | Applies to |

A key left out falls back to the `action_labels` of the item's placements,
the first in `placements.yaml` order that sets it, and then to the built-in
label. Set `pending_install` and `pending_remove` together with `install`
and `remove`, so a queued request reads in the words of the button that made
it.

### `display_name`

The name the card and the detail page show. It
defaults to the item's name. The card's monogram is made from its first two
words, and search matches both the display name and the item name.

### `developer`

The publisher, shown beside the category on the card and on the detail page.
Search matches it.

### `icon_name`

The catalog carries `icon_name` through to the item list, but the storefront
does not fetch or serve icons. It draws each item's icon locally, as a
monogram of one or two initials in a tinted tile that stays the same for the
item, and falls back to the category's glyph when the name has no letters.
SofCat has no remote icon transport, so an `icon_name` has no effect on the
look today.

## Branding keys

### `sections`

A list of up to 50 entries of plain text, up to 120 characters each. It sets
the order of the storefront tabs: the sections you list come first, in your
order, and the rest follow alphabetically. A listed section that no item
uses shows no tab, and a tab takes the spelling you list. The "Software" tab
for items without a `section` comes last.

As a Windows policy, `sections` is a `REG_MULTI_SZ` value. On macOS it is
an array of strings in the `branding` dict.

### `list_heading`

Plain text, up to 120 characters. It replaces the "Available software"
heading above the list. The "My items" heading is unchanged. With
`title_size` set, `list_heading` follows it like the other headings; see
[Branding](/customization/branding/).

### `applies_to_filter_label`

Plain text, up to 60 characters. It replaces "Works in", the label of the
applies-to filter. The filter is the select between the search field and the
Category select, or the group above the categories in the left bar. An empty
value keeps "Works in".

The filter shows only when the current list's items name two or more
distinct `applies_to` names. Its first option is "All". Picking a name hides
the items that do not list it. Search matches the names too, whatever the
filter says.
