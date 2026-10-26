package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/cli"
)

// Mounting turns each registry file into ochazuke state without running
// anything: a page titled by the extension name, one list per application
// noun (planOf), and on a nabe noun <ext>-<noun> one proxy verb per catalog
// verb. A proxy execs `<bin> NOUN VERB [--target ID] --json ARGS --yes` as
// pending work; its completion refreshes the extension's rows (afterVerb).
// An extension whose catalog names a state source (state, NOUN.VERB) has
// every list's rows from one `<bin> NOUN VERB --json {}`, an object keyed
// by list noun (reload); without one, or for a noun the answer omits, a
// noun with a row source loads from `<bin> NOUN VERB --json {}` (loadList).
// Rows answer to the noun the verb's catalog rows names (else the noun's own);
// any other noun gets one row per verb, enter on the row running it. The
// catalog's binding rows for mounted nouns are loaded through LoadBindings;
// a row whose key a framework noun on the row's path already binds is
// dropped with a warning (a footer notice, Model.Notify).

// mounted is one extension on screen. Its lists' rows are the cached
// state: loaded once at startup, refreshed every `every`, after a verb of
// the extension and on extension refresh, and drawn as they are (stale
// rows show at once) in between. Keys, focus and page switches never exec.
type mounted struct {
	name   string
	bin    string
	page   string
	lists  []*nounList
	groups *ochazuke.List
	status string // the last verb's result or error
	// state is the catalog's state source, NOUN.VERB ("" loads list by
	// list); stateErr why its last load failed.
	state    string
	stateErr string
	loaded   bool            // the first load landed; until then the page says loading
	stream   *stateLoad      // the running state load, nil when none
	fresh    map[string]bool // extension noun.verb -> declares a boolean fresh
	every    time.Duration   // the refresh interval; 0 never
	// sources are the pick-list sources (extension noun.verb) the TUI
	// serves from a cache (sources.go); nil from the CLI, whose parameters
	// name the mounted proxy instead.
	sources map[string]*choiceSource
}

// Refreshing.
const (
	// defaultRefreshEvery is the refresh interval without a registry
	// file's refreshEvery.
	defaultRefreshEvery = 60 * time.Second
	// refreshVerb and timerVerb are the verbs background loads and the
	// refresh timer run under: no operation has them, so the jobs ticker
	// leaves them off; the page says "refreshing" while a load runs.
	refreshVerb = "refresh"
	timerVerb   = "refresh-timer"
	// freshParam is the boolean an explicit refresh passes to a row or
	// state source that declares it (an extension's own cache bypass).
	freshParam = "fresh"
)

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
	loaded  bool   // its first load landed (lists loaded on their own)
	// choices is the pick-list source this list's rows answer (its row
	// source is some parameter's source), nil when none.
	choices *choiceSource
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

// mountAll mounts every registry file in page order (Registry.Ordered:
// order, then registration); a file that cannot be read or mounted becomes
// a warning.
func (a *App) mountAll() error {
	names, err := a.reg.Ordered()
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
	m := &mounted{name: name, bin: e.Bin, page: "ext/" + name, state: e.Catalog.State, fresh: map[string]bool{}, every: defaultRefreshEvery, sources: map[string]*choiceSource{}}
	if _, err := r.AddPage(m.page, name, a.renderMounted(m)); err != nil {
		return err
	}
	if e.RefreshEvery != "" {
		if d, err := time.ParseDuration(e.RefreshEvery); err != nil || d < 0 {
			a.warn("%s: refreshEvery %q is not a duration (0s or more); refreshing every %s", name, e.RefreshEvery, defaultRefreshEvery)
		} else {
			m.every = d
		}
	}
	stateFound := false
	for _, n := range e.Catalog.Nouns {
		for _, v := range n.Verbs {
			key := n.Name + "." + v.Name
			stateFound = stateFound || key == m.state
			if v.Params[freshParam].Type == "boolean" {
				m.fresh[key] = true
			}
		}
	}
	if m.state != "" && !stateFound {
		a.warn("%s: state source %s is not in the catalog; loading list by list", name, m.state)
		m.state = ""
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
				op.Parameters = maps.Clone(v.Params)
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
		opts := []ochazuke.InstanceOption{ochazuke.Parent(m.page), ochazuke.NoSpill()}
		if cols := columnsOf(e.Catalog, n.Name, rows, verb); len(cols) > 0 {
			opts = append(opts, ochazuke.Columns(cols...))
		}
		list, err := r.AddList(l.id, nil, opts...)
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
	for noun, list := range homes {
		a.homes[mountedNouns[noun]] = list
	}
	groups := make([]ochazuke.ListItem, 0, len(m.lists))
	for i, l := range m.lists {
		groups = append(groups, ochazuke.ListItem{ID: m.page + "/groups/" + l.noun, Label: l.noun, Noun: "page", Data: i})
	}
	var err error
	m.groups, err = r.AddList(m.page+"/groups/", groups, ochazuke.Parent(m.page), ochazuke.NoSpill())
	if err != nil {
		return err
	}
	for _, item := range groups {
		l := m.lists[item.Data.(int)]
		for _, key := range []string{"enter", "l", "right"} {
			if err := r.Bind(ochazuke.Binding{Instance: item.ID, Mode: normalMode, Keys: key, Request: ochazuke.Request{Noun: "focus", Verb: "next"}}); err != nil {
				return err
			}
		}
		for _, key := range []string{"h", "left"} {
			if err := r.Bind(ochazuke.Binding{Instance: l.id, Mode: normalMode, Keys: key, Request: ochazuke.Request{Noun: "focus", Verb: "set", Args: ochazuke.Arguments{"id": item.ID}}}); err != nil {
				return err
			}
		}
	}
	a.mountSources(m, nouns, mountedNouns)
	a.loadBindings(m, e.Catalog.Bindings, mountedNouns, homes)
	a.exts = append(a.exts, m)
	return nil
}

// columnsOf is the table a mounted list draws: the catalog columns of the
// noun its rows belong to (the row source's rows noun, else the list's own
// noun). A verb-row list, or a noun without columns, draws its labels.
func columnsOf(doc cli.CatalogDoc, noun, rows, verb string) []ochazuke.Column {
	if verb == "" {
		return nil
	}
	if rows == "" {
		rows = noun
	}
	for _, n := range doc.Nouns {
		if n.Name == rows {
			return n.Columns
		}
	}
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
				a.afterVerb(m, noun)
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

// LoadRows schedules the first load of every mounted extension: one
// state exec for an extension with a state source, else one per list.
func (a *App) LoadRows() {
	for _, m := range a.exts {
		a.reload(m, false)
	}
}

// StartRefresh arms every extension's refresh timer (the TUI only: the
// CLI and --smoke settle their jobs and would wait on it).
func (a *App) StartRefresh() {
	for _, m := range a.exts {
		a.arm(m)
	}
}

// arm schedules m's next timed refresh: a quiet job that waits m.every,
// then reloads m and arms again (tick).
func (a *App) arm(m *mounted) {
	if m.every <= 0 {
		return
	}
	every := m.every
	wait := func(ctx context.Context) (any, error) {
		select {
		case <-time.After(every):
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	res := ochazuke.Pending(wait).Replacing("timer").Then(func(_ ochazuke.Ctx, _ any, err error) {
		if err == nil {
			a.tick(m)
		}
	})
	if _, err := a.r.Launch(m.page, timerVerb, res, ochazuke.Ctx{Runtime: a.r}); err != nil {
		m.stateErr = err.Error()
	}
}

// tick is one timed refresh: reload, then wait again.
func (a *App) tick(m *mounted) {
	a.reload(m, false)
	a.arm(m)
}

// afterVerb refreshes what a verb of noun may have changed: the state,
// else only the lists that hold the noun's rows.
func (a *App) afterVerb(m *mounted, noun string) {
	if m.state != "" {
		a.reloadRows(m, false)
		return
	}
	local := localNoun(m.name, noun)
	for _, l := range m.lists {
		if l.source != "" && (l.local == local || l.rowNoun == local) {
			a.loadList(m, l, false)
		}
	}
}

// reload refreshes every list of m (reloadRows) and the pick-list sources
// no list answers (loadSources). fresh asks a source that declares it to
// skip its cache.
func (a *App) reload(m *mounted, fresh bool) {
	a.reloadRows(m, fresh)
	a.loadSources(m, fresh)
}

// reloadRows refreshes every list of m: one state exec whose answer fills
// each list by its noun (a noun the answer omits loads on its own), else
// one row-source exec per list.
func (a *App) reloadRows(m *mounted, fresh bool) {
	if m.state == "" {
		for _, l := range m.lists {
			if l.source != "" {
				a.loadList(m, l, fresh)
			}
		}
		return
	}
	noun, verb, _ := strings.Cut(m.state, ".")
	args := []string{noun, verb, "--json", m.args(m.state, fresh)}
	if m.stream != nil {
		m.stream.stop() // a newer load replaces it
	}
	st := &stateLoad{items: map[string][]ochazuke.ListItem{}, seen: map[string]bool{}}
	m.stream = st
	if a.sync {
		out, err := execBin(context.Background(), m.bin, args...)
		if err == nil {
			st.rr = cli.NewRowReader(bytes.NewReader(out))
		}
		for err == nil {
			var b stateBatch
			if b, err = st.next(); err == nil {
				a.stateBatch(m, st, b)
				if b.done {
					break
				}
			}
		}
		if err != nil {
			a.stateFailed(m, st, err)
		}
		return
	}
	st.ctx, st.cancel = context.WithTimeout(context.Background(), execTimeout)
	a.nextBatch(m, st, m.bin, args)
}

// Streaming a state load: the exec's stdout is read as JSON Lines in
// batches (stateBatchRows rows, or stateBatchWait after a batch's first
// line), each batch a quiet job whose completion files the rows by noun and
// launches the next. The first load pushes each batch to its lists, so a
// page fills while the extension still prints; a refresh swaps the lists
// when the stream ends, so the old rows stay whole until then.
const (
	stateBatchRows = 200
	stateBatchWait = 100 * time.Millisecond
)

// stateLoad is one running state exec.
type stateLoad struct {
	ctx    context.Context
	cancel context.CancelFunc
	cmd    *exec.Cmd
	stderr bytes.Buffer
	rr     *cli.RowReader
	items  map[string][]ochazuke.ListItem // rows so far by list noun
	seen   map[string]bool                // noun/id already filed
}

// stateBatch is one batch of a stream.
type stateBatch struct {
	rows []cli.Row
	done bool
}

func (st *stateLoad) stop() {
	if st.cancel != nil {
		st.cancel()
	}
}

// start runs the exec with stdout read as rows.
func (st *stateLoad) start(bin string, args []string) error {
	st.cmd = exec.CommandContext(st.ctx, bin, args...)
	st.cmd.Stderr = &st.stderr
	out, err := st.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := st.cmd.Start(); err != nil {
		return err
	}
	st.rr = cli.NewRowReader(out)
	return nil
}

// next reads one batch; at the end of the stream it waits for the exec,
// whose failure (with its stderr) fails the load.
func (st *stateLoad) next() (stateBatch, error) {
	rows, done, err := st.rr.Next(stateBatchRows, stateBatchWait)
	if done && st.cmd != nil {
		if werr := st.cmd.Wait(); werr != nil {
			if msg := strings.TrimSpace(st.stderr.String()); msg != "" {
				werr = fmt.Errorf("%w: %s", werr, msg)
			}
			err = werr
		}
	}
	return stateBatch{rows, done}, err
}

// nextBatch launches the job reading the stream's next batch (the first
// starts the exec).
func (a *App) nextBatch(m *mounted, st *stateLoad, bin string, args []string) {
	words := filepath.Base(bin) + " " + strings.Join(args[:2], " ")
	a.load(m.page, func(context.Context) (any, error) {
		if st.rr == nil {
			if err := st.start(bin, args); err != nil {
				return nil, err
			}
		}
		b, err := st.next()
		if err != nil {
			return nil, fmt.Errorf("%s failed: %w", words, err)
		}
		return b, nil
	}, func(v any, err error) {
		if m.stream != st {
			return // replaced by a newer load
		}
		if err != nil {
			a.stateFailed(m, st, err)
			return
		}
		b := v.(stateBatch)
		a.stateBatch(m, st, b)
		if !b.done {
			a.nextBatch(m, st, bin, args)
		}
	})
}

// stateBatch files a batch's rows under their lists; the first load shows
// them at once, and the stream's end sets every list (a noun without lines
// has no rows).
func (a *App) stateBatch(m *mounted, st *stateLoad, b stateBatch) {
	touched := map[string]bool{}
	for _, row := range b.rows {
		if st.seen[row.Noun+"\x00"+row.ID] {
			continue
		}
		st.seen[row.Noun+"\x00"+row.ID] = true
		st.items[row.Noun] = append(st.items[row.Noun], ochazuke.ListItem{ID: row.ID, Label: row.Label, Cells: row.Cells})
		touched[row.Noun] = true
	}
	for _, l := range m.lists {
		if l.source == "" || !(b.done || !m.loaded && touched[l.noun]) {
			continue
		}
		l.err = ""
		if err := a.setItems(l, st.items[l.noun]); err != nil {
			l.err = err.Error()
		}
	}
	if b.done {
		m.loaded, m.stateErr, m.stream = true, "", nil
		st.stop()
	}
}

// stateFailed is a load that failed entirely: the rows stay, the page
// says why.
func (a *App) stateFailed(m *mounted, st *stateLoad, err error) {
	st.stop()
	m.stateErr, m.stream = err.Error(), nil
}

// args is the --json of a source exec: {"fresh": true} on an explicit
// refresh of a source that declares fresh, else {}.
func (m *mounted) args(key string, fresh bool) string {
	if fresh && m.fresh[key] {
		return `{"` + freshParam + `":true}`
	}
	return "{}"
}

// loadList reloads one list from its own row source.
func (a *App) loadList(m *mounted, l *nounList, fresh bool) {
	args := m.args(l.noun+"."+l.source, fresh)
	a.load(l.id, func(ctx context.Context) (any, error) {
		out, err := execBin(ctx, m.bin, l.noun, l.source, "--json", args)
		if err != nil {
			return nil, err
		}
		return decoded(out), nil
	}, func(v any, err error) {
		if err == nil {
			err = a.setRows(l, v)
		}
		l.err = ""
		if err != nil {
			l.err = err.Error()
		}
		l.loaded = true
		m.loaded = !slices.ContainsFunc(m.lists, func(l *nounList) bool { return l.source != "" && !l.loaded })
	})
}

// load runs fetch as a quiet job in owner's rows slot (a newer load
// replaces an older one) and apply on its completion; inline in sync mode.
func (a *App) load(owner string, fetch func(context.Context) (any, error), apply func(any, error)) {
	a.loadIn(owner, "rows", fetch, apply)
}

// loadIn is load in a slot of its own.
func (a *App) loadIn(owner, slot string, fetch func(context.Context) (any, error), apply func(any, error)) {
	if a.sync {
		apply(fetch(context.Background()))
		return
	}
	res := ochazuke.Pending(fetch).Replacing(slot).Then(func(_ ochazuke.Ctx, v any, err error) { apply(v, err) })
	if _, err := a.r.Launch(owner, refreshVerb, res, ochazuke.Ctx{Runtime: a.r}); err != nil {
		apply(nil, err)
	}
}

// refreshing reports whether a load of m runs.
func (a *App) refreshing(m *mounted) bool {
	for _, j := range a.r.Jobs() {
		if j.State != ochazuke.JobRunning || j.Verb != refreshVerb {
			continue
		}
		if j.Owner == m.page || slices.ContainsFunc(m.lists, func(l *nounList) bool { return l.id == j.Owner }) {
			return true
		}
	}
	return false
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
	rows := make([]ochazuke.ListItem, 0, len(values))
	for _, value := range values {
		id, label := rowOf(value)
		rows = append(rows, ochazuke.ListItem{ID: id, Label: label, Cells: cellsOf(value)})
	}
	return a.setItems(l, rows)
}

// setItems shows rows (the extension's id and label) in l: instance ids
// prefixed with the list id, so two extensions' rows never meet; each
// row's Data is the extension's own id (--target); a label stands in for a
// missing one, an empty or repeated id is dropped.
func (a *App) setItems(l *nounList, rows []ochazuke.ListItem) error {
	items := make([]ochazuke.ListItem, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		if row.ID == "" || seen[row.ID] {
			continue
		}
		seen[row.ID] = true
		label := row.Label
		if label == "" {
			label = row.ID
		}
		items = append(items, ochazuke.ListItem{ID: l.id + "/" + row.ID, Label: label, Noun: l.rowNoun, Data: row.ID, Cells: row.Cells})
	}
	if err := l.list.SetItems(items); err != nil {
		return err
	}
	if l.choices != nil {
		ids := make([]string, len(items))
		for i, item := range items {
			ids[i] = item.Data.(string)
		}
		a.setChoices(l.choices, ids)
	}
	return nil
}

// cellsOf reads a row object's cells (an array of strings), else none.
func cellsOf(value any) []string {
	obj, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	list, ok := obj["cells"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, len(list))
	for i, v := range list {
		if v != nil {
			out[i] = fmt.Sprint(v)
		}
	}
	return out
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

// renderMounted draws the catalog's groups beside the selected group's cached
// rows. Only that content list participates in focus navigation.
func (a *App) renderMounted(m *mounted) func(*ochazuke.Runtime, int, int) ochazuke.Block {
	return func(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
		if width <= 0 || height <= 0 {
			return ochazuke.Block{}
		}
		s := ochazuke.NewStyles(*a.m.Appearance)
		layout := ochazuke.Layout{Styles: s}
		leftWidth := min(28, max(1, (width-1)/3))
		rightWidth := max(0, width-leftWidth-1)
		lw, lh := ochazuke.PanelContentSize(leftWidth, height)
		rw, rh := ochazuke.PanelContentSize(rightWidth, height)
		selected, ok := m.groups.Selected()
		var current *nounList
		if ok {
			index := selected.Data.(int)
			current = m.lists[index]
			// Explicit focus (for example from the palette) reveals its group.
			for i, l := range m.lists {
				if l.list.Focused() {
					m.groups.Move(i - index)
					current = l
					break
				}
			}
		}
		for _, l := range m.lists {
			a.r.Instances[l.id].Unfocusable = l != current
		}
		left := layout.Panel("Groups", m.groups.View(lw, lh), leftWidth, height, m.groups.Focused())
		title, body := "Contents", "no groups"
		busy := ""
		if a.refreshing(m) {
			busy = "refreshing…"
			if !m.loaded {
				busy = "loading…"
			}
		}
		var status []string
		for _, line := range append([]string{busy, m.stateErr}, append(m.sourceErrs(), m.status)...) {
			if line != "" {
				status = append(status, s.Muted.Render(ansi.Truncate(line, rw, "…")))
			}
		}
		if current != nil {
			title = fmt.Sprintf("%s (%d)", current.noun, current.list.Len())
			if current.err != "" {
				status = append(status, s.Muted.Render(ansi.Truncate(current.err, rw, "…")))
			}
			body = current.list.View(rw, max(0, rh-len(status)))
			if current.list.Len() == 0 && rh > len(status) {
				body = s.Muted.Render("no rows")
			}
		}
		if len(status) > 0 {
			body = strings.TrimRight(body, "\n") + "\n" + strings.Join(status, "\n")
		}
		right := layout.Panel(title, body, rightWidth, height, current != nil && current.list.Focused())
		return ochazuke.Text(lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right))
	}
}
