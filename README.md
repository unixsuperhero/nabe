# nabe

A host for ochazuke extensions, installed as `o-nabe` and `o-tui` (`o nabe`, `o tui`). nabe knows one noun, `extension`; everything else on its screen comes from extension catalogs. Any executable that answers `BIN catalog` with the JSON `ochazuke/cli.Catalog` writes (schema 1) becomes a page: one list per application noun, its verbs as keys and palette commands, its confirm prompts and binding rows. mtm is the first tenant.

## Install

```sh
go build -o ~/bin/o-nabe . && ln -sf ~/bin/o-nabe ~/bin/o-tui   # `o nabe` and `o tui` both open it
go build -o ~/bin/nabe .                                        # optional bare name
```

nabe reads its own program name, so help and errors say `o-tui`, `o-nabe` or `nabe` as invoked. The `o` root (`~/proj/oden/cmd/o`) has no knowledge of nabe; it only finds `o-tui` on PATH.

Until ochazuke and oden are tagged, `go.mod` replaces them with local checkouts in `~/proj`.

## Register mtm

```sh
nabe extension register mtm        # resolves mtm on PATH, runs `mtm catalog`, writes the registry file
nabe extension register o-proj     # a family tool named o-<name> registers as <name> (here: proj)
nabe ext reg mtm                   # the same, by prefixes
nabe extension list                # JSON, one row per registry file
nabe extension refresh [mtm]       # run catalog again (every extension without a name)
nabe extension unregister mtm      # remove the file (asks; --yes skips)
nabe extension order ai 9          # place a page: pages sort by order (default 0), then registration
nabe ext reg o-ai ai 9             # register with an order (bare words: bin, name, order)
nabe                               # the TUI: one page per extension, then Extensions
nabe proj                          # the TUI on the proj page (o tui proj); --page proj is the same
nabe --page extensions             # the TUI on the Extensions page
nabe --smoke                       # one 80x24 frame with the rows loaded
```

A lone word that names a page (a registered extension's name, or `extensions`) opens the TUI there, unless a command is named exactly that word: commands win, and `--page NAME` always means the page. It works with `--smoke` too.

`register` prints a summary such as `mtm: 11 nouns, 27 verbs, 3 pages (framework nouns skipped: 28)`. Run `MTM_SERVER=default nabe ...` to keep mtm from starting its own daemon.

A mounted verb takes `--target` from the CLI as the extension's own id (`nabe mtm-project open --target p1` runs `mtm project open --target p1 --json {} --yes`) or as the row's nabe id (`--target ext/mtm/projects/p1`). No rows are loaded from the CLI, so nabe stands in a row for the target (`cli.Shell.Reveal`); an id that is also one of nabe's own instance ids (a page, a list, `app`) needs the nabe-id form.

`nabe help` lists the commands: oden's builtins, `catalog`, `extension`, and each mounted noun (`nabe mtm-projects list` runs `mtm projects list` through the proxy). Bare words after NOUN VERB fill the verb's parameters, required first, then by name (`nabe ext reg mtm other` registers mtm as `other`).

`catalog` prints nabe's own catalog under the name it was invoked by: `o-nabe catalog` and `o-tui catalog` answer `"name": "o-nabe"` and `"name": "o-tui"`, so the o family can mount nabe like any other extension.

## The registry file

`$XDG_DATA_HOME/nabe/catalogs/<name>.json` (else `~/.local/share/nabe/catalogs`; `-data DIR` uses `DIR/catalogs`):

```js
entry = {
  bin: "/Users/me/go/bin/mtm",           // resolved absolute path
  registeredAt: "2026-10-24T18:08:50Z",
  refreshEvery: "5m",                    // optional, hand-edited: how often the rows reload ("0s" never; default 60s)
  order: 0,                              // optional: pages sort by order, then registeredAt, then name (extension order NAME N, register --order N)
  // register and refresh keep registeredAt, refreshEvery and order: re-registering never moves a page
  catalog: {                             // the CatalogDoc the bin answered, verbatim
    schema: 1, name: "mtm", version: "v0.0.0-…",
    state: "projects.state",             // optional state source: every list's rows in one call
    nouns: [
      { name: "projects", verbs: [{ name: "list", kind: "application", result: "JSON value", rows: "project" }] },
      { name: "project",  verbs: [{ name: "open", kind: "application" }] },
      { name: "field", pin: "insert", verbs: [/* … */] },   // pinned to a modal mode: skipped
      /* … */
    ],
    bindings: [{ noun: "task", mode: "normal", keys: "x", request: { noun: "task", verb: "done" } }, /* … */],
    pages: [{ id: "work", title: "Work" }, /* … */],
  },
}
```

## How a catalog mounts

```js
mount("mtm") = {
  page:     { id: "ext/mtm", title: "mtm" },
  nouns:    "every noun with an application verb and no modal pin (pin is \"\" or \"normal\"), as mtm-<noun>; the rest are skipped",
  state:    "with a catalog state source: `mtm projects state --json {}` once, answering JSON Lines {noun, id, label} tagged by list noun; each list takes its noun's lines",
  rows:     "without one, or for a noun the state omits, per noun with a row source (the verb declaring rows, else list): `mtm NOUN VERB --json {}`",
  row:      { id: "ext/mtm/projects/p1", label: "pone", noun: "mtm-project", data: "p1" }, // from { id: "p1", name: "pone" }; noun is the verb's rows, else the list's own
  columns:  "a list whose row noun declares catalog columns is a table (ochazuke.Columns): a header, each row's cells (state lines' or list objects' cells), columns dropped by priority when narrow; without columns, the labels; empty, the header over no rows",
  verbRows: "any other noun no row source lists: one row per verb; enter runs it (not for verbs with required parameters: use the palette)",
  verbs:    "mtm-project.open -> `mtm project open --target <row data> --json '{}' --yes` (pending), then the state reloads (without a state source: only the lists holding that noun's rows)",
  confirm:  "the catalog's template, unchanged ({{label}} reads the row label)",
  bindings: "the catalog rows of mounted nouns via LoadBindings; a key the list, page or app already binds is dropped with a footer notice",
}
```

## Two-panel navigation

Each extension page has a left-hand **Groups** list in catalog order and a right-hand panel showing only the selected group's rows or verbs. `j`/`k` (or the arrow keys) select a group without running the extension. `enter`, `l` or `right` moves into its contents; `h` or `left` returns to the group list. `tab`/`shift+tab` and `}`/`{` move between the two visible lists, never the hidden groups. Each content list keeps its selection when switching groups. Extension bindings still take precedence over the content list's navigation bindings.

Both panels stay side by side; their lists scroll independently within the available terminal height. Extension tabs and the Extensions registry page are unchanged.

## When nabe execs

The rows on screen are a cache. nabe runs an extension's bin only:

```js
execs = {
  startup:           "every extension at once, in the background: one state exec each with a state source, else one per list",
  everyInterval:     "the same load, on a quiet timer (refreshEvery, default 60s)",
  afterVerb:         "the verb itself, then the state (or only the verb's own list)",
  extensionRefresh:  "catalog, then the state with --json '{\"fresh\":true}' when the source declares fresh",
  choiceSources:     "with every load above but afterVerb: each parameter source no list's rows answer (BIN NOUN VERB --json {})",
  keysFocusPages:    "never: j/k, }, ctrl+j and page switches draw the cached rows",
}
```

Pages follow registration order (`registeredAt`, then `order`), so the first tab is the first extension registered; the others load behind it while it is read. Loads are quiet jobs (off the jobs ticker). Until a page's first rows land its lists are empty and its last line says `loading…`; later loads say `refreshing…` and keep the old rows until the new ones are complete. A load that fails entirely (the exec fails, exits non-zero, or prints nothing decodable) shows its error there and keeps the rows. `--smoke` waits for every load before drawing. A list draws at most a screen of rows and scrolls to its selection, so a frame formats only what can show.

The state source is the ochazuke/cli catalog convention (`NounBuilder.State`, the catalog's top-level `state`, `"NOUN.VERB"`). It answers every list in one process as JSON Lines, one row per line tagged with the list noun whose row source it replaces:

```js
// o-ai status state
{"noun":"agent","id":"agent/agent:claude:/Users/me/.claude/agents/ada.md","label":"ada [claude]"}
{"noun":"skill","id":"skill/skill:claude:/Users/me/.claude/skills/review/SKILL.md","label":"review [claude]"}
```

nabe reads the stream as it arrives (`cli.NewRowReader`: batches of 200 rows or 100 ms): on the first load each batch goes to its list at once, so a page fills while the extension still prints; a refresh swaps the lists when the stream ends. A malformed line is skipped silently. A noun with no lines has no rows. o-ai (`status state`) and mtm (`projects state`) have one; an extension without it keeps per-list loads (`NOUN list --json {}`, a JSON array).

## Limits

- Mounting reads the registry once at startup; restart nabe after `register`, `refresh`, `order` or `unregister`.
- A catalog without `rows` keeps the old shape: rows of `projects list` are `mtm-projects` rows, and a noun such as `project` sits on verb rows without a target.
- Parameter pick lists (`source`, an extension `NOUN.VERB`) reach the palette and the CLI only when the source's noun is mounted (else a notice drops it). From the CLI the parameter names the mounted proxy (`help` says `choices from: mtm-projects.list`). In the TUI the choices are a cache: the rows of the list that source fills, else one quiet `BIN NOUN VERB --json {}` with every row load (startup, the timer, `extension refresh`; not after a verb); the pick list shows ids, not labels. Source choices are suggestions: nabe never refuses a value outside them, even where the catalog marks the parameter not `free`, so a stale or partial source list never refuses a value the bin would accept (mtm's `--model` takes custom ids); a non-free source is still checked by the bin itself. A static `enum` without a source is still enforced.
- Pattern rows (`digit`, `any`) are not compared for collisions; only the first key of a sequence is.
- Exec only, one process per call, 60 s per call.
