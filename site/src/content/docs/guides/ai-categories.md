---
title: AI categories
description: Have makecatalogs categorize new catalog items with a language model behind OpenRouter, a Cloudflare AI Gateway or LiteLLM.
---

`makecatalogs` can fill in the `category` of every item that has none by
asking a language model. Set it up once; after that a new item gets its
category on the next `makecatalogs` run, and the storefront's category filter
shows it.

It runs where the catalogs are built, on your machine or in the repository's
CI. Devices never see the API key and never call the model.

## Set it up

1. Add `categorize.yaml` to the repository, beside `packages-info/`:

   ```yaml
   base_url: https://openrouter.ai/api/v1
   model: google/gemini-3.1-flash-lite
   ```

2. Give `makecatalogs` the API key in its environment:

   ```sh
   SOFCAT_AI_API_KEY=... makecatalogs <repo_path>
   ```

3. Commit `categories.lock.yaml`, which that run wrote.

The key is read only from `SOFCAT_AI_API_KEY`, never from a file, because the
repository is published. In CI, keep it as a secret.

## The endpoint

`base_url` is any endpoint that speaks the OpenAI chat completions API;
`makecatalogs` posts to `<base_url>/chat/completions` with the key as a
bearer token.

| Service | `base_url` | `model` |
| --- | --- | --- |
| OpenRouter (the default) | `https://openrouter.ai/api/v1` | An OpenRouter model name |
| Cloudflare AI Gateway | Your gateway's OpenAI-compatible URL, the one ending in `/compat` | `<provider>/<model>` |
| LiteLLM proxy | Your proxy's URL, such as `https://litellm.example.com/v1` | A model name from your proxy's config |

Categorizing is a small task, so choose one of the cheaper models. Items are
sent 25 to a request, with each item's name, display name, description and
developer, so a good `description` gives a good category.

## Settings

| Key | Default | Meaning |
| --- | --- | --- |
| `base_url` | `https://openrouter.ai/api/v1` | The endpoint. |
| `model` | none, required | The model's name as the endpoint knows it. |
| `mode` | `closed` | `closed` or `open`; see below. |
| `categories` | `Browsers`, `Communication`, `Development`, `Media`, `Productivity`, `Security`, `Utilities`, `AI` | The known categories. |
| `min_weight` | `0.5` | The least weight the best category needs. |

[`examples/categorize.yaml`](https://github.com/hurricanehrndz/sofcat/blob/main/examples/categorize.yaml)
is an annotated copy.

## Closed and open lists

The known categories are those in `categories` plus every category written by
hand in a package-info file.

- `mode: closed` lets the model choose only from the known categories. An
  answer outside them is dropped.
- `mode: open` also lets the model propose a new category when none fits. A
  category it coins becomes known, so later items can join it.

## How a category is chosen

The model gives each item up to three candidate categories with a weight for
each. `makecatalogs` then:

1. matches each name to a known category, whatever its case, so `browsers`
   becomes `Browsers`;
2. in closed mode drops the names that are not known, and in open mode
   multiplies their weight by 0.75, so a new category has to clearly beat the
   existing ones before it is coined;
3. scales the weights to a sum of 1 and takes the heaviest;
4. uses it if its weight reaches `min_weight` and no other category has the
   same weight, and otherwise leaves the item without a category rather than
   guess.

Items you categorized by hand are sent along as examples (up to 20), so the
model follows how your repository uses its categories.

## A hand-written category wins

An item with a `category` in its package-info file is never sent to be
categorized and never changed. To overrule the model, write the category you
want.

## The lock file

`categories.lock.yaml` keeps the model's answer for each item, with its
weights:

```yaml
items:
  GoogleChrome:
    category: Browsers
    weights:
      Browsers: 0.9
      Utilities: 0.1
    fingerprint: 5d1c0f6f8a3b2e47
```

Commit it. An item is asked about once; it is asked about again only when
its name, display name, description or developer changes, or when its
category is taken off a closed list. A run without the key, and
`makecatalogs --check`, never call the model: they use the lock file, so the
same repository always gives the same catalogs. Delete an item's entry to
have it asked about again.

When the model cannot be reached or gives no usable answer, `makecatalogs`
warns, writes the catalogs anyway and leaves those items without a category
until the next run. Only a `categorize.yaml` it cannot use (no `model`, an
unknown `mode`) stops it.

`makecatalogs build` reads `categorize.yaml` from the source directory and
keeps `categories.lock.yaml` there.
