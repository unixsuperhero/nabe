package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/cli"
)

// Mounting turns each registry file into ochazuke state without running
// anything: a page titled by the extension name, one list per application
// noun (planOf), and on a nabe noun <ext>-<noun> one proxy verb per catalog
// verb. A proxy execs `<bin> NOUN VERB [--target ID] --json ARGS --yes` as
// pending work; its completion reloads the extension's lists. Rows of a
// noun with a row source come from `<bin> NOUN VERB --json {}` (loadRows),
// answering to the noun the verb's catalog rows names (else the noun's own);
// any other noun gets one row per verb, enter on the row running it. The
// catalog's binding rows for mounted nouns are loaded through LoadBindings;
// a row whose key a framework noun on the row's path already binds is
// dropped with a warning (a footer notice, Model.Notify).

// mounted is one extension on screen.
type mounted struct {
	name   string
	bin    string
	page   string
	lists  []*nounList
	status string // the last verb's result or error
}

// nounList is the list of one mounted noun.
type nounList struct {
	noun  string // the extension's noun
	local string // the nabe noun, <ext>-<noun>
	id    string
	list  *ochazuke.List
	// source is the verb whose answer is the list's rows ("" for verb
	// rows); rowNoun is the nabe noun those rows answer to.
	source  string
	rowNoun string
	err     string // why the last row load failed
}

// localNoun is the nabe noun of an extension's noun. A dot would split the
// operation key (noun.verb), so the separator is a hyphen.
func localNoun(ext, noun string) string { return ext + "-" + noun }

// rowSourceVerb is the verb whose JSON answer is a noun's own rows when no
// verb of the noun declares rows.
const rowSourceVerb = "list"

// idFields and labelFields are the row object fields read, first present
// wins, for a row's id and its label.
var (
	idFields    = []string{"id", "ID"}
	labelFields = []string{"label", "Label", "name", "Name", "title", "Title"}
)

// rowActivateKey runs a verb row's verb.
const rowActivateKey = "enter"

// mountAll mounts every registry file in name order; a file that cannot be
// read or mounted becomes a warning.
func (a *App) mountAll() error {
	names, err := a.reg.Names()
	if err != nil {
		return err
	}
	for _, name := range names {
		e, err := a.reg.Read(name)
		if err != nil {
			a.warn("%v", err)
			continue
		}
		if err := a.mount(name, e); err != nil {
			a.warn("%s: %v", name, err)
		}
	}
	return nil
}

// warn is a mount problem: a persistent notice in the footer.
func (a *App) warn(format string, args ...any) {
	a.m.Notify(fmt.Sprintf(format, args...))
}

// mount adds one extension's page, nouns, lists and binding rows. Every
// mounted noun gets its verbs; a list is made for each noun with a row
// source (rowSource) and for each noun no other noun's source lists rows
// of (that one gets verb rows). A noun whose rows another noun lists has
// no list of its own: its verbs answer on those rows.
func (a *App) mount(name string, e Entry) error {
	r := a.r
	m := &mounted{name: name, bin: e.Bin, page: "ext/" + name}
	if _, err := r.AddPage(m.page, name, a.renderMounted(m)); err != nil {
		return err
	}
	p := planOf(e.Catalog)
	mountedNouns := map[string]string{}
	var nouns []cli.CatalogNoun
	for _, n := range p.Nouns {
		local := localNoun(name, n.Name)
		if r.Noun(local) != nil {
			a.warn("%s: noun %s skipped: nabe already has a noun %s", name, n.Name, local)
			continue
		}
		mountedNouns[n.Name] = local
		nouns = append(nouns, n)
		b := r.Define(local)
		for _, v := range n.Verbs {
			b.Verb(v.Name, a.proxy(m, n.Name, v.Name), v.Description)
			if v.Confirm != "" {
				b.Confirm(v.Confirm)
			}
			if len(v.Params) > 0 {
				op := r.Operations[local+"."+v.Name]
				op.Parameters = withoutSources(v.Params)
				r.Operations[op.Key()] = op
			}
		}
	}
	// listedBy maps a noun to the noun whose row source lists its rows.
	listedBy := map[string]string{}
	for _, n := range nouns {
		verb, rows := rowSource(n)
		if verb == "" || rows == n.Name {
			continue
		}
		if _, ok := mountedNouns[rows]; !ok {
			a.warn("%s: %s %s lists rows of %s, which is not mounted; its rows answer to %s", name, n.Name, verb, rows, n.Name)
			continue
		}
		if _, taken := listedBy[rows]; !taken {
			listedBy[rows] = n.Name
		}
	}
	homes := map[string]string{} // noun -> the list its instances live in
	for _, n := range nouns {
		verb, rows := rowSource(n)
		if _, ok := listedBy[n.Name]; ok && verb == "" {
			continue
		}
		local := mountedNouns[n.Name]
		l := &nounList{noun: n.Name, local: local, id: m.page + "/" + n.Name, source: verb, rowNoun: local}
		if other, ok := mountedNouns[rows]; ok && verb != "" {
			l.rowNoun = other
		}
		list, err := r.AddList(l.id, nil, ochazuke.Parent(m.page))
		if err != nil {
			return err
		}
		l.list = list
		a.owners[local] = l.id
		homes[n.Name] = l.id
		if l.source == "" {
			if err := a.verbRows(m, l, n.Verbs); err != nil {
				return err
			}
		}
		m.lists = append(m.lists, l)
	}
	for noun, source := range listedBy {
		if _, own := homes[noun]; !own {
			homes[noun] = homes[source]
		}
	}
	// Lists with rows first: they hold the extension's data.
	slices.SortStableFunc(m.lists, func(x, y *nounList) int {
		switch {
		case (x.source == "") == (y.source == ""):
			return 0
		case x.source != "":
			return -1
		}
		return 1
	})
	a.loadBindings(m, e.Catalog.Bindings, mountedNouns, homes)
	a.exts = append(a.exts, m)
	return nil
}

// rowSource is the verb whose JSON answer is a noun's rows and the noun
// those rows answer to: the first verb declaring rows (the catalog's rows),
// else a verb named list, whose rows are the noun's own. "" when the noun
// has neither.
func rowSource(n cli.CatalogNoun) (verb, rows string) {
	for _, v := range n.Verbs {
		if v.Rows != "" {
			return v.Name, v.Rows
		}
	}
	for _, v := range n.Verbs {
		if v.Name == rowSourceVerb {
			return v.Name, n.Name
		}
	}
	return "", ""
}

// withoutSources copies params without their pick-list sources: a source
// names an operation of the extension's runtime, not nabe's.
func withoutSources(params map[string]ochazuke.Parameter) map[string]ochazuke.Parameter {
	out := make(map[string]ochazuke.Parameter, len(params))
	for k, p := range params {
		p.Source = ""
		out[k] = p
	}
	return out
}

// verbRows fills a noun's list with one row per verb, each answering the
// activate key with its own verb. A verb with required parameters gets no
// key (a row cannot supply them); the palette runs it.
func (a *App) verbRows(m *mounted, l *nounList, verbs []cli.CatalogVerb) error {
	items := make([]ochazuke.ListItem, 0, len(verbs))
	for _, v := range verbs {
		label := v.Name
		if req := required(v.Params); len(req) > 0 {
			label += " (palette: " + strings.Join(req, ", ") + ")"
		}
		if v.Description != "" {
			label += "  " + v.Description
		}
		items = append(items, ochazuke.ListItem{ID: l.local + "/" + v.Name, Label: label, Noun: l.local})
	}
	if err := l.list.SetItems(items); err != nil {
		return err
	}
	for _, v := range verbs {
		if len(required(v.Params)) > 0 {
			continue
		}
		row := ochazuke.Binding{Instance: l.local + "/" + v.Name, Mode: normalMode, Keys: rowActivateKey, Request: ochazuke.Request{Noun: l.local, Verb: v.Name}}
		if err := a.r.Bind(row); err != nil {
			a.warn("%s: %s %s: no %s row: %v", m.name, l.noun, v.Name, rowActivateKey, err)
		}
	}
	return nil
}

// required lists a verb's required parameters, sorted.
func required(params map[string]ochazuke.Parameter) []string {
	var out []string
	for name, p := range params {
		if p.Required {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// loadBindings installs the catalog rows of the mounted nouns, renamed to
// the nabe nouns; homes is the list each noun's instances live in.
func (a *App) loadBindings(m *mounted, rows []ochazuke.Binding, nouns, homes map[string]string) {
	for _, row := range rows {
		local, ok := nouns[row.Noun]
		if !ok {
			continue // a row of a noun nabe does not mount
		}
		where := fmt.Sprintf("%s: key %s%s on %s", m.name, row.Keys, row.Pattern, row.Noun)
		if row.Instance != "" {
			a.warn("%s dropped: instance rows name the extension's own instances", where)
			continue
		}
		if req, ok := nouns[row.Request.Noun]; ok {
			row.Request.Noun = req
		} else if _, ok := a.r.Operations[row.Request.Key()]; !ok {
			a.warn("%s dropped: %s is not mounted", where, row.Request.Key())
			continue
		}
		listID := homes[row.Noun]
		row.Noun = local
		if owner, taken := a.taken(listID, row); taken {
			a.warn("%s dropped: taken by %s", where, owner)
			continue
		}
		data, err := json.Marshal([]ochazuke.Binding{row})
		if err == nil {
			_, err = a.r.LoadBindings(data)
		}
		if err != nil {
			a.warn("%s dropped: %v", where, err)
		}
	}
}

// taken reports the framework or nabe row a mounted row would shadow: a
// row with the same first key, in the same mode or in any mode, on a noun
// of the row's path (its list, the page, app; nabe's own rows there too).
// Pattern rows (digit, any) are not compared.
func (a *App) taken(listID string, row ochazuke.Binding) (string, bool) {
	if row.Keys == "" {
		return "", false
	}
	first := strings.Fields(row.Keys)[0]
	var nouns []string
	for _, id := range a.r.Ancestry(listID) {
		nouns = append(nouns, a.r.Instances[id].Noun)
	}
	for _, name := range nouns {
		n := a.r.Noun(name)
		if n == nil {
			continue
		}
		modes := make([]string, 0, len(n.Bindings))
		for mode := range n.Bindings {
			modes = append(modes, mode)
		}
		slices.Sort(modes)
		for _, mode := range modes {
			if mode != row.Mode && mode != "" && row.Mode != "" {
				continue
			}
			for _, b := range n.Bindings[mode] {
				if b.Keys != "" && strings.Fields(b.Keys)[0] == first {
					return fmt.Sprintf("%s (%s)", name, b.Request.Key()), true
				}
			}
		}
	}
	return "", false
}

// proxy is the handler of one mounted verb.
func (a *App) proxy(m *mounted, noun, verb string) ochazuke.AppHandler {
	return func(c ochazuke.Ctx) ochazuke.Result {
		args := []string{noun, verb}
		if c.Instance != nil {
			if target, ok := c.Instance.Data.(string); ok && target != "" {
				args = append(args, "--target", target)
			}
		}
		js, err := json.Marshal(c.Args)
		if err != nil {
			return ochazuke.Failed(err)
		}
		if len(c.Args) == 0 {
			js = []byte("{}")
		}
		args = append(args, "--json", string(js), "--yes")
		return a.work(func(ctx context.Context) (any, error) {
			out, err := execBin(ctx, m.bin, args...)
			if err != nil {
				return nil, err
			}
			return decoded(out), nil
		}, func(v any, err error) {
			m.status = resultLine(noun+" "+verb, v, err)
			if !a.sync { // the CLI shows no lists
				a.loadRows(m)
			}
		})
	}
}

// decoded is stdout as JSON, else as trimmed text.
func decoded(out []byte) any {
	var v any
	if err := json.Unmarshal(out, &v); err != nil {
		return strings.TrimSpace(string(out))
	}
	return v
}

// resultLine is the page's status after a verb.
func resultLine(what string, v any, err error) string {
	if err != nil {
		return what + ": " + err.Error()
	}
	data, _ := json.Marshal(v)
	return what + ": " + string(data)
}

// LoadRows schedules the row loads of every mounted extension.
func (a *App) LoadRows() {
	for _, m := range a.exts {
		a.loadRows(m)
	}
}

// loadRows reloads every list of m with a row source, each as a
// job in the list's rows slot (a newer load replaces an older one).
func (a *App) loadRows(m *mounted) {
	for _, l := range m.lists {
		if l.source == "" {
			continue
		}
		fetch := func(ctx context.Context) (any, error) {
			out, err := execBin(ctx, m.bin, l.noun, l.source, "--json", "{}")
			if err != nil {
				return nil, err
			}
			return decoded(out), nil
		}
		apply := func(v any, err error) {
			if err == nil {
				err = a.setRows(l, v)
			}
			l.err = ""
			if err != nil {
				l.err = err.Error()
			}
		}
		if a.sync {
			apply(fetch(context.Background()))
			continue
		}
		res := ochazuke.Pending(fetch).Replacing("rows").Then(func(_ ochazuke.Ctx, v any, err error) { apply(v, err) })
		if _, err := a.r.Launch(l.id, l.local+"."+l.source, res, ochazuke.Ctx{Runtime: a.r}); err != nil {
			l.err = err.Error()
		}
	}
}

// setRows turns a list answer (a JSON array) into rows of the list's row
// noun.
// Row instance ids are prefixed with the list id, so two extensions'
// rows never meet; each row's Data is the extension's own id (--target).
func (a *App) setRows(l *nounList, v any) error {
	values, ok := v.([]any)
	if !ok {
		if v == nil {
			values = nil
		} else {
			return fmt.Errorf("%s %s: want a JSON array, got %T", l.noun, l.source, v)
		}
	}
	items := make([]ochazuke.ListItem, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		id, label := rowOf(value)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		items = append(items, ochazuke.ListItem{ID: l.id + "/" + id, Label: label, Noun: l.rowNoun, Data: id})
	}
	return l.list.SetItems(items)
}

// rowOf reads a row's id and label: an object's first id and label fields
// (either stands in for the other), a scalar as both.
func rowOf(value any) (string, string) {
	obj, ok := value.(map[string]any)
	if !ok {
		s := fmt.Sprint(value)
		return s, s
	}
	pick := func(fields []string) string {
		for _, f := range fields {
			if v, ok := obj[f]; ok && v != nil && fmt.Sprint(v) != "" {
				return fmt.Sprint(v)
			}
		}
		return ""
	}
	id, label := pick(idFields), pick(labelFields)
	if id == "" {
		id = label
	}
	if label == "" {
		label = id
	}
	return id, label
}

// renderMounted is an extension page: each list under its heading and the
// last verb's status (mount warnings are footer notices). Taller content
// scrolls (pgup and pgdown).
func (a *App) renderMounted(m *mounted) func(*ochazuke.Runtime, int, int) ochazuke.Block {
	return func(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
		s := ochazuke.NewStyles(*a.m.Appearance)
		var parts []string
		for _, l := range m.lists {
			n := l.list.Len()
			head := fmt.Sprintf("%s (%d)", l.noun, n)
			if l.err != "" {
				head = l.noun + ": " + l.err
			}
			parts = append(parts, s.Muted.Render(ansi.Truncate(head, width, "…")))
			if n > 0 {
				parts = append(parts, strings.TrimRight(l.list.View(width, n), "\n"))
			}
		}
		if m.status != "" {
			parts = append(parts, s.Muted.Render(ansi.Truncate(m.status, width, "…")))
		}
		return ochazuke.Text(strings.Join(parts, "\n"))
	}
}
