# Categorize fixture

A build source for the AI categories guide
(`site/src/content/docs/guides/ai-categories.md`): sixteen zip items that
only place files. Three have a hand-written `category`; `makecatalogs` asks
the model in `categorize.yaml` about the other thirteen. `acme-starter-pack`
says nothing about itself: the model may split its weight evenly, which
leaves the item without a category.

It calls a real endpoint (OpenRouter by default) and needs a key:

```sh
go build -o build/makecatalogs ./cmd/makecatalogs
SOFCAT_AI_API_KEY=... build/makecatalogs build test/manual/fixtures/categorize -o build/manual-test/categorize/server-root
```

The run prints `Categorized 13 item(s) with <model>`, writes
`categories.lock.yaml` here (ignored by git, so every run of the fixture
starts by asking) and the catalog in
`build/manual-test/categorize/server-root/catalogs/categorize.yaml`. A second
run asks nothing. Set `mode: open` in `categorize.yaml` and delete the lock
file to see the model propose categories of its own.
