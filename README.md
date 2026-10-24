# nabe

A host for ochazuke extensions. nabe knows one noun, `extension`; everything else on its screen comes from extension catalogs. Any executable that answers `BIN catalog` with the JSON `ochazuke/cli.Catalog` writes (schema 1) becomes a page: one list per application noun, its verbs as keys and palette commands, its confirm prompts and binding rows. mtm is the first tenant.

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
nabe ext reg mtm                   # the same, by prefixes
nabe extension list                # JSON, one row per registry file
nabe extension refresh [mtm]       # run catalog again (every extension without a name)
nabe extension unregister mtm      # remove the file (asks; --yes skips)
nabe                               # the TUI: one page per extension, then Extensions
nabe --smoke                       # one 80x24 frame with the rows loaded
```

`register` prints a summary such as `mtm: 11 nouns, 27 verbs, 3 pages (framework nouns skipped: 28)`. Run `MTM_SERVER=default nabe ...` to keep mtm from starting its own daemon.

`nabe help` lists the commands: oden's builtins, `catalog`, `extension`, and each mounted noun (`nabe mtm-projects list` runs `mtm projects list` through the proxy).

## The registry file

`$XDG_DATA_HOME/nabe/catalogs/<name>.json` (else `~/.local/share/nabe/catalogs`; `-data DIR` uses `DIR/catalogs`):

```js
entry = {
  bin: "/Users/me/go/bin/mtm",           // resolved absolute path
  registeredAt: "2026-10-24T18:08:50Z",
  catalog: {                             // the CatalogDoc the bin answered, verbatim
    schema: 1, name: "mtm", version: "v0.0.0-…",
    nouns: [{ name: "projects", verbs: [{ name: "list", kind: "application", result: "JSON value" }] }, /* … */],
    bindings: [{ noun: "task", mode: "normal", keys: "x", request: { noun: "task", verb: "done" } }, /* … */],
    pages: [{ id: "work", title: "Work" }, /* … */],
  },
}
```

## How a catalog mounts

```js
mount("mtm") = {
  page:     { id: "ext/mtm", title: "mtm" },
  nouns:    "every noun with an application verb, as mtm-<noun>; interaction-only nouns (the framework's) are skipped",
  rows:     "noun with a list verb: `mtm NOUN list --json {}` (pending), each element { id, label|name|title } -> a row",
  verbRows: "any other noun: one row per verb; enter runs it (not for verbs with required parameters: use the palette)",
  verbs:    "mtm-task.done -> `mtm task done --target <row id> --json '{}' --yes` (pending), then the lists reload",
  confirm:  "the catalog's template, unchanged ({{label}} reads the row label)",
  bindings: "the catalog rows of mounted nouns via LoadBindings; a key the list, page or app already binds is dropped with a warning",
}
```

## Limits

- Mounting reads the registry once at startup; restart nabe after `register`, `refresh` or `unregister`.
- A catalog does not say which noun a list's rows answer to, so rows of `projects list` are `mtm-projects` rows (verb `list` only) while `project open` and `project launch` sit on verb rows without a target.
- Parameter pick lists (`source`) name the extension's own operations and are dropped; enums stay.
- Pattern rows (`digit`, `any`) are not compared for collisions; only the first key of a sequence is.
- From nabe's CLI, a mounted verb runs without `--target` (nabe's instance ids are not the extension's); use the extension's own CLI to target a row.
- Exec only, one process per call, 60 s per call.
