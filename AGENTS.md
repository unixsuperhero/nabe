# nabe: agent guide

The host app (`nabe`, installed as `o-nabe` and `o-tui` under the `o` root). An ochazuke app (`github.com/unixsuperhero/ochazuke`, Bubble Tea v2) with no nouns of its own beyond `extension`: it mounts extension catalogs as pages. Any executable answering `BIN catalog` with the JSON `ochazuke/cli.Catalog` produces (schema 1) becomes a page with lists and verbs; mtm is the first tenant. ochazuke stays a framework (no noun names), mtm stays thin (the slice), nabe hosts. Hot pot: everyone adds to one pot.

## Plans and todos

Family plans, todos and the roadmap live in `~/hub/o-plans/` (`README.md` is the index). Read it before planning work; add or update items there. Do not add a TODO.md or docs/plans/ to this repo.

## Read in this order

1. This file, then `README.md` for the registry shape and limits.
2. `../ochazuke/AGENTS.md`, section "Building an application on ochazuke", and `../ochazuke/cli/{shell.go,mount.go,catalog.go}` (the CLI shell, `Mount`, `CatalogDoc`).
3. `../oden/README.md` (inline commands, `<bin>-<sub>` externals, prefixes).
4. `~/hub/o-plans/plans/cli-extensions.md`: the extension contract this implements on the host side.

## Where does X live

| Concern | File |
|---|---|
| Registry files (`DATA/catalogs/<name>.json`), `register`/`refresh`/`unregister`/`list`, `BIN catalog` exec and validation, `planOf` (which nouns mount) | `registry.go` |
| Mounting: proxy verbs on `<ext>-<noun>`, which nouns get a list, the data page per extension (`cli.MountPage`, groups layout: the framework draws the groups rail, the lists, their columns and the verb rows, and decodes rows), the loads (startup, after a verb, `extension refresh`, the streamed state through `PageView.SetState`; the timer calls `PageView.Reload`), the page's status line, catalog bindings and collision warnings | `mount.go` |
| Parameter pick-list sources: the proxy from the CLI, a cache in each parameter's `Enum` in the TUI (a source list's rows, else a quiet load with the rows); suggestions only: every source-backed parameter is `Free`, whatever the catalog's `free` says, so a stale or partial source list never refuses a value the bin would accept (the bin still checks a non-free one) | `sources.go` |
| CLI `--target` on mounted verbs: a stand-in row for the target (`reveal`, wired as `cli.Shell.Reveal` in `main.go`) | `target.go` |
| `App`, `New`, the `extension` noun and the extensions page, `work` (pending or inline) | `app.go` |
| Flags (`-data`, `--smoke`, `--page`), the lone page word (`pageWord`), the oden App (`cli.Mount`) | `main.go` |
| Which page a name opens (`pageNamed`, `OpenOn`) | `app.go` |
| The fake extension the tests drive | `testdata/fake-ext`, `testdata/fake-catalog.json` |

## What nabe owns

- The registry: one JSON file per registered extension under `$XDG_DATA_HOME/nabe/catalogs` (else `~/.local/share/nabe/catalogs`; `-data DIR` uses `DIR/catalogs`). `register` and `refresh` are the only writers; mounting only reads.
- Mounting: turning a registry file into a page, lists, nouns, verbs and rows at startup, and the exec proxy each mounted verb runs.

## What the framework does for nabe

nabe keeps no copy of these; use them rather than rebuilding them.

| Mechanism | nabe uses it for |
|---|---|
| `ListItem.Data` -> `Instance.Data` | a row's extension id; a proxy reads `ctx.Instance.Data` for `--target` |
| `List.Len()`, `List.Items()` | list counts on the pages |
| `cli.MountPage`, `cli.PageView` (`SetRows`, `SetState`, `SetError`, `SetStatus`, `Reload`), `cli.PageConfig.OnRows` (the pick lists follow every landing of a list), `cli.DecodeRows`, `CatalogDoc.RowSource` | an extension's page: the groups rail, lists, columns, verb rows, row decoding, the `loading…`/`no rows` notes and the timed reload |
| `Model.Notify`, `Model.Notices`, `notice.list`/`notice.clear` | mount warnings, a persistent footer notice |
| `cli.Settle(r, before)` | `--smoke` and the tests settle pending work without a Bubble Tea loop |
| `cli.Shell` positional words (`Operation.ParameterOrder`), verb prefixes; `cli.Mount` noun prefixes | `nabe ext reg BIN [NAME]` |
| catalog `pin` on nouns, `rows` on verbs (`cli.CatalogNoun`, `cli.CatalogVerb`) | which nouns mount; which noun a list verb's rows answer to |
| catalog `history` on verbs (`cli.CatalogVerb.History`), `Operation.HistoryKind` | a mounted verb records the extension's own kind (`historyKind`; none is the default, change), so its fetches stay out of the user's history |
| Tables (`ochazuke.Column`, `Columns(...)`, `ListItem.Cells`); catalog `columns` on nouns, `cells` on rows (`cli.CatalogNoun.Columns`, `cli.Row.Cells`) | a mounted list draws its row noun's columns (`cli.MountPage` reads `CatalogDoc.ListColumns`); the extensions page is a table (`extensionColumns`: name, nouns, verbs, pages, refreshed, version, bin; `binCell` elides a long bin's middle so its name survives the cut) |

## Invariants

- No noun names in nabe's own code except `extension`. Which nouns mount is decided by data (`planOf`): a noun mounts when at least one of its verbs is mountable (`mountable`: kind `application` and not a framework verb) and its catalog `pin` is not a modal mode (`pin` is `""` or `normal`). The framework's verbs are the operation keys a fresh `ochazuke.NewRuntime` and `NewModel` register (`frameworkVerbs`): most are interaction verbs, but the tree's `outline` verbs have application handlers and every ochazuke tool's catalog lists them as `application`, so kind alone would mount an `<ext>-outline` per extension. An extension's own application verb on a framework noun (mtm's `history.capture-hook`) still mounts. Nouns pinned to `insert` or `terminal` live inside the extension's own modals, so they are skipped too, without being named.
- A noun's rows come from its row source: the first verb declaring `rows: "<noun>"` (its rows answer to that noun's verbs, and that noun gets no list of its own), else a verb named `list` (rows of the noun itself; catalogs without `rows` mount as before).
- Every mounted thing comes from a catalog: pages, lists, nouns, verbs, confirm templates, parameters and binding rows. nabe adds only generic rows (enter on a verb row runs that verb).
- Exec is the only transport. A mounted verb runs `BIN NOUN VERB [--target ID] --json ARGS --yes`; rows come from the catalog's state source (`BIN NOUN VERB --json {}`, every list at once, keyed by noun) or, without one, `BIN NOUN list --json {}` per list; a parameter's pick-list source no list answers is `BIN NOUN VERB --json {}` loaded with the rows (not after a verb); nothing else reaches the extension. Mounting never execs.
- Pages follow `Registry.Ordered` (order, then registeredAt, then name); register and refresh keep a file's registeredAt, order and refreshEvery. A state source answers JSON Lines (`cli.Row`), read in batches (`stateLoad`): the first load fills lists per batch, a refresh swaps at the end.
- The rows are a cache: loaded at startup for every extension in the background, on the refresh timer (`refreshEvery`, default 60s), after a verb of the extension (the state, else only that noun's lists) and on `extension refresh` (with `fresh` when the source declares it). A key, a focus move or a page switch never execs; `TestExecBudget` counts it. Loads are quiet jobs; the page says `refreshing…` while one runs.
- The TUI never blocks on an extension: proxies and row loads return `Pending` (row loads in a `rows` slot per list); completions refresh the lists with `SetItems`. The CLI (`Options.Sync`) runs the same work inline so it can print the value.
- Mounted nouns are `<ext>-<noun>` (a dot would split `noun.verb`); row instance ids are prefixed with their list id, and each row's `Data` is the extension's own id for `--target`.
- A catalog binding row whose first key a noun on the row's path (the list its noun's rows live in, the page, app) already binds is dropped with a footer notice, never silently. Every problem while mounting is a notice, not a failed start.
- No key-name switches, no verb-specific branches, handlers return Results; `ValidateBindings` runs in `New` and in the tests. Never bind function keys.

## Checks

```sh
gofmt -l . && go vet ./... && timeout 300 go test -race -count=2 ./... && timeout 60 go run . -data /tmp/nabe-smoke --smoke
```

ochazuke (`v0.7.0`) and oden (`v0.1.1`) are required at their tags; an untracked `go.work` (`go work init . && go work use ../oden ../ochazuke`) builds against the sibling checkouts, and `GOWORK=off` builds from the tags as a fresh clone does. Commit after every step; do not amend.
