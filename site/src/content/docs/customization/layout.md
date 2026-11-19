---
title: Layout and behaviour
description: Choose where the navigation sits, how items open, and where progress shows.
---

These `branding:` keys change how the storefront is arranged and how it behaves,
not how it is named or coloured. For the name, logo and banner see
[Branding](/customization/branding/). For how the settings reach a machine see
[Delivering settings](/customization/delivery/).

```yaml
branding:
  nav_placement: left
  category_filter: list
  content_width: full
  item_view: dialog
  progress: button
```

You can see a branded storefront in the [live demo](/demo/?store=apps).

## Summary

| Key | Accepted values | Default |
|---|---|---|
| `nav_placement` | `top`, `left` | `top` |
| `category_filter` | `select`, `list` | `select` |
| `filter_placement` | `right`, `left` | `right` |
| `content_width` | `wide`, `full`, `compact` | `wide` |
| `app_bar_brand` | `full`, `mark`, `none` | `full` |
| `item_view` | `page`, `dialog` | `page` |
| `progress` | `strip`, `button` | `strip` |

For every key, any other value falls back to the default.

## Navigation and filters

### `nav_placement`

Where the app bar sits. The app bar holds the product's mark and name (as
`app_bar_brand` shows them), the list tabs, My items and Activity, the
`link_placement: app_bar` button and the connection status.

- `top` is the default. The bar runs across the window, under the banner.
- `left` makes the bar a column down the window's left, under the banner. The
  mark and name are at its top, the pages sit one under another, and the link
  button and connection status are at its foot. The bar stays in place while
  the content scrolls, and the operation strip keeps to the content's side of
  it.

A window narrower than 40rem (640pt) shows the bar across the top for either
value. The key only moves the bar. `category_filter` decides what else the bar
holds. Any other value is `top`.

### `category_filter`

How a list's category is chosen.

- `select` is the default. A Category select sits beside the search field.
- `list` works with `nav_placement: left`. The bar lists the current list's
  categories as buttons under the pages, and the select disappears from beside
  the search field. When the list has an applies-to filter, it becomes a second
  button group in the bar, under the categories. Activity and an item's detail
  page show no categories.

With the bar across the top, `list` acts as `select`. That includes a narrow
window. Any other value is `select`.

The applies-to filter itself is described on
[the catalog page](/customization/catalog/).

### `filter_placement`

Where the search field and the Category select sit above a list and My items.
The applies-to filter, when the list has one, sits with the Category select.
While `category_filter: list` shows the categories in a left bar, only the
search field remains.

- `right` is the default. The controls sit on the heading's row, at its right
  edge. When the row is too narrow for both, they move under the heading.
- `left` puts the controls above the heading, at the left.

In a window narrower than 40rem (640pt) both values stretch the controls to the
full width, one under the other. They sit under the heading for `right` and
above it for `left`.

With either value the keyboard reaches the search field first, then the Category
select, then the applies-to filter, then the cards. Any other value is `right`.

## Content and app bar

### `content_width`

How much of the window the lists, My items, Activity and the detail page use.
The banner, app bar and operation strip span the window in all three values.

- `wide` is the default. It centres the content in a column of up to 96rem
  (1536pt) and lets the cards of a short row share its width. A row of one or
  two cards shows each as a single line, with glyph, name and buttons.
- `full` drops the column's cap, so the content runs to the window's edges on
  any screen.
- `compact` uses a column of up to 76rem (1216pt). Its cards keep one column's
  width, which leaves the rest of a short row empty.

Any other value is `wide`.

### `app_bar_brand`

What the app bar shows of the product beside the tabs.

- `full` is the default. It shows the mark and the name.
- `mark` shows the mark only.
- `none` shows neither.

Use `mark` or `none` when the banner already names the organisation. While no
banner shows, the value is `full`. Screen readers still get the name, as the
page's heading, in all three. With `none` and a banner that shows neither a
title nor a logo, no name is visible anywhere. Any other value is `full`.

## Items and progress

### `item_view`

How a card opens its item.

`page` is the default. The item's detail page replaces the list, with a Back
button, and each card has a Details button.

`dialog` shows the same details in a dialog over the dimmed list. The dialog
grows out of the card and shrinks back into it. When the card is not wholly on
screen, the dialog just appears and disappears over a backdrop that still fades
in. Under reduced motion neither moves. The close button, `Escape` or a click
outside the dialog closes it.

With `dialog`, the cards have no Details button. The item's name is the button
that opens its dialog. The list behind the dialog and the dialog itself keep
updating.

The operation strip stays at the window's bottom, dimmed and out of reach like
the rest of the list. A click on it closes the dialog. While an operation on
the open item runs, the dialog shows a Cancel button beside its action, on a row
of its own under the title. It follows the strip's rules.

Any other value is `page`.

### `progress`

Where running work shows.

`strip` is the default. The bottom strip shows the operation being worked on,
with its bar, "n of N" and Cancel.

`button` gives each item's action button its own progress, on its card and its
detail page.

- While the request is with the service or queued, the button shows "Waiting…"
  with a spinner.
- Then it shows a bar with the phase. The bar fills to the percentage only when
  the service measured the phase. Otherwise it moves back and forth.
- A success fills the bar. A failure, cancel or deferral drops it and shows the
  action again.
- As under `strip`, the button is freed when the refreshed list arrives.

A Cancel button sits beside the action button while the operation runs. It
follows the strip's rules and is placed so the action button never moves when it
appears.

With `button`, the strip shows only while two or more operations run. It shows
a count, such as "Installing 1 of 3", and is a button. It opens a list of the
operations above it, one row each, with the working one first. Each row shows
its state and its own Cancel. `Escape` or a click outside closes the list.

Screen readers hear each new phase once per operation, never a percentage. Any
other value is `strip`.

## Side navigation recipe

The four keys `nav_placement`, `category_filter`, `item_view` and `progress` are
independent, and each has the default shown above. Together they give a
storefront with its pages and categories down the side and items that open over
the list.

```yaml
branding:
  nav_placement: left
  category_filter: list
  item_view: dialog
  progress: button
```
