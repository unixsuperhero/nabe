package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/cli"
)

// Pick-list sources. A catalog parameter's source names an operation of the
// extension (NOUN.VERB) whose answer lists the parameter's choices. ochazuke
// runs a source synchronously (Runtime.Choices, from the palette and from
// argument checks), and a mounted verb runs the bin, so nabe gives the
// framework a source two ways:
//
//   - the CLI (Options.Sync) names the mounted proxy of the source
//     (<ext>-<noun>.<verb>), so help says where the choices come from;
//   - the TUI never execs on the loop, so a parameter's source is a cache
//     written into its Enum: the rows of the list whose row source is the
//     source (no extra exec), else one quiet load of the source with the
//     rows (reload: startup, the timer, extension refresh; not after a
//     verb). Enum has no labels: the pick list shows the ids.
//
// Source-backed choices are suggestions only: every such parameter is
// Free, so nabe never refuses a value outside them (and the CLI never
// runs the source to check one). The catalog does not carry a parameter's
// free flag, and the bin validates its own arguments anyway (mtm's
// agent launch --model accepts a model id harness.models does not list).
// A static Enum without a source is still refused outside it.
//
// A source whose noun is not mounted is dropped with a notice.

// choiceSource is one source of an extension and the parameters it fills.
type choiceSource struct {
	key    string        // the extension's NOUN.VERB
	params []choiceParam // the parameters naming it
	list   bool          // a list's rows answer it (no load of its own)
	ids    []string      // the cached choices
	loaded bool          // ids came from an answer
	err    string        // why its last load failed (the page shows it)
}

// choiceParam is one parameter fed by a source: the operation key, its
// name and the catalog's parameter (Enum the static choices).
type choiceParam struct {
	op, name string
	base     ochazuke.Parameter
}

// mountSources points every parameter source of the mounted nouns at nabe:
// the proxy from the CLI, a cache in the TUI (see above).
func (a *App) mountSources(m *mounted, nouns []cli.CatalogNoun, mounted map[string]string) {
	for _, n := range nouns {
		for _, v := range n.Verbs {
			names := make([]string, 0, len(v.Params))
			for name := range v.Params {
				names = append(names, name)
			}
			slices.Sort(names)
			for _, name := range names {
				p := v.Params[name]
				if p.Source == "" {
					continue
				}
				opKey := mounted[n.Name] + "." + v.Name
				srcNoun, srcVerb, _ := strings.Cut(p.Source, ".")
				proxy := mounted[srcNoun] + "." + srcVerb
				if _, ok := a.r.Operations[proxy]; !ok || mounted[srcNoun] == "" {
					a.warn("%s: %s %s --%s: choices source %s is not mounted; no pick list", m.name, n.Name, v.Name, name, p.Source)
					a.setParam(opKey, name, func(q *ochazuke.Parameter) { q.Source = "" })
					continue
				}
				if a.sync {
					a.setParam(opKey, name, func(q *ochazuke.Parameter) { q.Source, q.Free = proxy, true })
					continue
				}
				src := m.sources[p.Source]
				if src == nil {
					src = &choiceSource{key: p.Source}
					m.sources[p.Source] = src
				}
				base := p
				base.Source = ""
				src.params = append(src.params, choiceParam{op: opKey, name: name, base: base})
			}
		}
	}
	for _, l := range m.lists {
		if src := m.sources[l.noun+"."+l.source]; src != nil && l.source != "" {
			l.choices, src.list = src, true
		}
	}
	for _, src := range m.sources {
		a.applyChoices(src)
	}
}

// setParam edits one parameter of a registered operation.
func (a *App) setParam(opKey, name string, edit func(*ochazuke.Parameter)) {
	op := a.r.Operations[opKey]
	p := op.Parameters[name]
	edit(&p)
	op.Parameters[name] = p
}

// setChoices caches a source's answer and writes it into its parameters.
func (a *App) setChoices(src *choiceSource, ids []string) {
	src.ids, src.loaded = ids, true
	a.applyChoices(src)
}

// applyChoices writes a source's cache into each parameter it feeds: the
// static Enum, then the cached ids; always Free (suggestions only).
func (a *App) applyChoices(src *choiceSource) {
	for _, cp := range src.params {
		p := cp.base
		p.Enum = slices.Clone(p.Enum)
		for _, id := range src.ids {
			if !slices.Contains(p.Enum, id) {
				p.Enum = append(p.Enum, id)
			}
		}
		p.Free = true
		a.setParam(cp.op, cp.name, func(q *ochazuke.Parameter) { *q = p })
	}
}

// loadSources loads every source of m no list answers, each a quiet job
// in a slot of its own on the page. A failed load keeps the cache.
func (a *App) loadSources(m *mounted, fresh bool) {
	keys := make([]string, 0, len(m.sources))
	for key, src := range m.sources {
		if !src.list {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	for _, key := range keys {
		src := m.sources[key]
		noun, verb, _ := strings.Cut(key, ".")
		args := m.args(key, fresh)
		a.loadIn(m.page, "choices "+key, func(ctx context.Context) (any, error) {
			out, err := execBin(ctx, m.bin, noun, verb, "--json", args)
			if err != nil {
				return nil, err
			}
			return decoded(out), nil
		}, func(v any, err error) {
			var choices []ochazuke.Choice
			if err == nil {
				choices, err = ochazuke.ParseChoices(v)
			}
			if err != nil {
				src.err = fmt.Sprintf("choices %s: %v", key, err)
				return
			}
			src.err = ""
			ids := make([]string, len(choices))
			for i, c := range choices {
				ids[i] = c.ID
			}
			a.setChoices(src, ids)
		})
	}
}

// sourceErrs are the failed source loads of m, by key.
func (m *mounted) sourceErrs() []string {
	var out []string
	for _, src := range m.sources {
		if src.err != "" {
			out = append(out, src.err)
		}
	}
	slices.Sort(out)
	return out
}
