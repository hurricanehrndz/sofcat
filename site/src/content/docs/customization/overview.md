---
title: Make it yours
description: What you can change about SofCat's storefront and product, and whether each change takes a setting, a catalog edit or your own build.
sidebar:
  label: Overview
---

Most self-service catalogs let you set a logo and a colour. SofCat lets you
change the storefront's name, banner, navigation, wording and the way items
open, and most of it takes a setting from your MDM. Nobody rebuilds or
re-signs anything, and the storefront keeps its look offline.

When settings are not enough, you build SofCat under your own name. The
result is your product, with its own service, installer and policy key, and
it installs beside stock SofCat without sharing anything with it.

Compare the two stores in the [live demo](/demo/?store=apps) with its
"Unbranded" view. Both run the same build.

## Three layers

| Layer | What it changes | What it takes |
| --- | --- | --- |
| Settings | The storefront's look, layout and behaviour | A `branding:` key in `config.yaml`, MDM or Group Policy |
| Catalog | Tabs, categories, button labels and filters | An edit to your repository |
| Rebrand | The product's name, icon, service, installer and write roots | Your own build |

Each layer works without the other two. A stock install with a three-line
policy is a branded storefront. A rebrand with no settings at all is a
product with your name on it and the default look.

## Settings

Every key is optional, and a storefront with none of them looks like stock
SofCat.

- [Branding](/customization/branding/) covers the title, tagline, logo and
  accent colour, the banner's background image, style, alignment and height,
  and a link button for your help desk or intranet.
- [Layout and behaviour](/customization/layout/) covers where the navigation
  sits, how people pick a category, how wide the content runs, whether an
  item opens as a page or a dialog, and where progress shows.

This block gives a storefront with its pages and categories down the left
side, items that open over the list and progress inside each button:

```yaml
branding:
  title: Acme Software Center
  accent: "#0a66c2"
  nav_placement: left
  category_filter: list
  item_view: dialog
  progress: button
```

[Delivering settings](/customization/delivery/) explains where the keys can
live and which source wins. In short, managed policy beats `config.yaml`
field by field, so you can set the repository URL in one place and the look
in another.

## Catalog

The repository decides how items are grouped and worded. A `section` on an
item becomes a storefront tab, so one install can hold an "Apps" store and
an "AI" store. Categories, the applies-to filter and each item's button
labels come from the repository too. See
[Catalog presentation](/customization/catalog/).

## Rebrand

A [rebranded build](/customization/rebrand/) carries your name in the Start
menu, Add or remove programs, `/Applications`, the service list and the
installer. It can also fix parts of the look at build time, which no policy
or config on the machine can then change, and it sets where zip items may
write.

## Beyond the storefront

The same idea runs through the rest of SofCat. Your own scripts can supply
the facts that [installable conditions](/guides/conditions/) test, and
[placements](/zip-items/placements/) decide where each zip item's files land
on a machine or in a user's profile.
