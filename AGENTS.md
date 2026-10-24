# nabe: agent guide

The host app. An ochazuke app (`github.com/unixsuperhero/ochazuke`, Bubble Tea v2) with no nouns of its own beyond `extension`: it mounts extension catalogs as pages. Any executable answering `BIN catalog` with the JSON `ochazuke/cli.Catalog` produces (schema 1) becomes a page with lists and verbs; mtm is the first tenant. ochazuke stays a framework (no noun names), mtm stays thin (the slice), nabe hosts. Hot pot: everyone adds to one pot.

## Read in this order

1. This file, then `README.md` for the registry shape and limits.
2. `../ochazuke/AGENTS.md`, section "Building an application on ochazuke", and `../ochazuke/cli/{shell.go,mount.go,catalog.go}` (the CLI shell, `Mount`, `CatalogDoc`).
3. `../oden/README.md` (inline commands, `<bin>-<sub>` externals, prefixes).
4. `../matomeru/docs/plans/cli-extensions.md`: the extension contract this implements on the host side.

## Where does X live

| Concern | File |
|---|---|
| Registry files (`DATA/catalogs/<name>.json`), `register`/`refresh`/`unregister`/`list`, `BIN catalog` exec and validation, `planOf` (which nouns mount) | `registry.go` |
| Mounting: page per extension, list per noun, proxy verbs on `<ext>-<noun>`, rows from `NOUN list`, verb rows, catalog bindings and collision warnings, the extension page render | `mount.go` |
| `App`, `New`, the `extension` noun and the extensions page, `work` (pending or inline), `Settle` | `app.go` |
| Flags (`-data`, `--smoke`), the oden App (`cli.Mount`), positional parameters | `main.go` |
| The fake extension the tests drive | `testdata/fake-ext`, `testdata/fake-catalog.json` |

## What nabe owns

- The registry: one JSON file per registered extension under `$XDG_DATA_HOME/nabe/catalogs` (else `~/.local/share/nabe/catalogs`; `-data DIR` uses `DIR/catalogs`). `register` and `refresh` are the only writers; mounting only reads.
- Mounting: turning a registry file into a page, lists, nouns, verbs and rows at startup.

## Invariants

- No noun names in nabe's own code except `extension`. Which nouns mount is decided by data: a noun mounts when at least one of its verbs has kind `application` (`planOf`); the framework's nouns (app, list, page, palette, input, mode, focus, history, bindings, jobs, terminal, ...) are interaction-only, so they are skipped without being named.
- Every mounted thing comes from a catalog: pages, lists, nouns, verbs, confirm templates, parameters and binding rows. nabe adds only generic rows (enter on a verb row runs that verb).
- Exec is the only transport. A mounted verb runs `BIN NOUN VERB [--target ID] --json ARGS --yes`; rows come from `BIN NOUN list --json {}`; nothing else reaches the extension. Mounting never execs.
- The TUI never blocks on an extension: proxies and row loads return `Pending` (row loads in a `rows` slot per list); completions refresh the lists with `SetItems`. The CLI (`Options.Sync`) runs the same work inline so it can print the value.
- Mounted nouns are `<ext>-<noun>` (a dot would split `noun.verb`); row instance ids are prefixed with their list id and mapped back to the extension's id for `--target`.
- A catalog binding row whose first key a noun on the row's path (its list, the page, app) already binds is dropped with a warning on the page and in the footer, never silently. Every problem while mounting is a warning, not a failed start.
- No key-name switches, no verb-specific branches, handlers return Results; `ValidateBindings` runs in `New` and in the tests. Never bind function keys.

## Checks

```sh
gofmt -l . && go vet ./... && timeout 300 go test -race -count=2 ./... && timeout 60 go run . -data /tmp/nabe-smoke --smoke
```

ochazuke and oden are local `replace` lines; drop them once both are tagged. Commit after every step; do not amend.
