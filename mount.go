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
	"sync/atomic"
	"time"

	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/cli"
)

// Mounting turns each registry file into ochazuke state without running
// anything: on a nabe noun <ext>-<noun> one proxy verb per catalog verb,
// then a data page (cli.MountPage, groups layout) titled by the extension
// name with one list per application noun (planOf). A proxy execs
// `<bin> NOUN VERB [--target ID] --json ARGS --yes` as pending work; its
// completion refreshes the extension's rows (afterVerb). An extension
// whose catalog names a state source (state, NOUN.VERB) has every list's
// rows from one `<bin> NOUN VERB --json {}`, JSON Lines tagged by list noun
// (reloadRows; a catalog declaring stateDelta is sent the page's list
// hashes and answers only what changed, applied by PageView.ApplyState);
// without one, a noun with a row source loads from
// `<bin> NOUN VERB --json {}` (loadList). The framework draws the page, the
// groups rail, the lists, their columns and the verb rows of a noun
// without a row source (enter running the verb). The catalog's binding
// rows for mounted nouns are loaded through LoadBindings; a row whose key
// a framework noun on the row's path already binds is dropped with a
// warning (a footer notice, Model.Notify).

// mounted is one extension on screen. Its lists' rows are the cached
// state: loaded once at startup, refreshed every `every`, after a verb of
// the extension and on extension refresh, and drawn as they are (stale
// rows show at once) in between. Keys, focus and page switches never exec.
type mounted struct {
	name string
	bin  string
	page string
	// doc is the catalog as mounted: application verbs only, and a state
	// source that is not in the catalog cleared.
	doc    cli.CatalogDoc
	nouns  map[string]string // catalog noun -> the nabe noun its rows answer to
	view   *cli.PageView     // nil when no noun mounted
	groups *ochazuke.List
	status string // the last verb's result or error
	// loaded is whether the first load landed (until then the page says
	// loading); listsLoaded are the lists loaded on their own that did.
	loaded      bool
	listsLoaded map[string]bool
	stream      *stateLoad      // the running state load, nil when none
	fresh       map[string]bool // extension noun.verb -> declares a boolean fresh
	every       time.Duration   // the refresh interval; 0 never
	// sources are the pick-list sources (extension noun.verb) the TUI
	// serves from a cache (sources.go); nil from the CLI, whose parameters
	// name the mounted proxy instead.
	sources map[string]*choiceSource
	// freshNext asks the next state fetch through FetchFlags (an explicit
	// refresh's PageView.Refresh) for {"fresh":true}; read off the loop.
	freshNext atomic.Bool
	// refreshAsked is an explicit refresh running through the page's own
	// reload, so the status line says refreshing while its job runs.
	refreshAsked bool
}

// deltas reports whether m's state source answers what changed since the
// rows the page holds (the catalog's stateDelta; cli.StateDeltaVersion).
func (m *mounted) deltas() bool {
	return m.view != nil && m.doc.State != "" && m.doc.StateDelta >= 1
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

// localNoun is the nabe noun of an extension's noun. A dot would split the
// operation key (noun.verb), so the separator is a hyphen.
func localNoun(ext, noun string) string { return ext + "-" + noun }

// local is the nabe noun a catalog noun's rows answer to.
func (m *mounted) local(noun string) string {
	if l, ok := m.nouns[noun]; ok {
		return l
	}
	return localNoun(m.name, noun)
}

// specs are the page's lists, in group order.
func (m *mounted) specs() []cli.CatalogList {
	if m.view == nil {
		return nil
	}
	return m.view.Spec().Lists
}

// source is a list's row source verb ("" for a list of verb rows).
func (m *mounted) source(l cli.CatalogList) string {
	verb, _ := m.doc.RowSource(l.Noun)
	return verb
}

// rowNoun is the nabe noun a list's rows answer to.
func (m *mounted) rowNoun(l cli.CatalogList) string {
	if _, rows := m.doc.RowSource(l.Noun); rows != "" {
		return m.local(rows)
	}
	return m.local(l.Noun)
}

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

// mount adds one extension's nouns, page, lists and binding rows. Every
// mounted noun gets its verbs; a list is made for each noun with a row
// source (CatalogDoc.RowSource) and for each noun no other noun's source
// lists rows of (that one gets verb rows). A noun whose rows another noun
// lists has no list of its own: its verbs answer on those rows.
func (a *App) mount(name string, e Entry) error {
	r := a.r
	m := &mounted{name: name, bin: e.Bin, page: "ext/" + name, doc: applicationDoc(e.Catalog), nouns: map[string]string{}, listsLoaded: map[string]bool{}, fresh: map[string]bool{}, every: defaultRefreshEvery, sources: map[string]*choiceSource{}}
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
			stateFound = stateFound || key == m.doc.State
			if v.Params[freshParam].Type == "boolean" {
				m.fresh[key] = true
			}
		}
	}
	if m.doc.State != "" && !stateFound {
		a.warn("%s: state source %s is not in the catalog; loading list by list", name, m.doc.State)
		m.doc.State = ""
	}
	p := planOf(e.Catalog)
	var nouns []cli.CatalogNoun
	for _, n := range p.Nouns {
		local := localNoun(name, n.Name)
		if r.Noun(local) != nil {
			a.warn("%s: noun %s skipped: nabe already has a noun %s", name, n.Name, local)
			continue
		}
		m.nouns[n.Name] = local
		nouns = append(nouns, n)
		b := r.Define(local)
		for _, v := range n.Verbs {
			b.Verb(v.Name, a.proxy(m, n.Name, v.Name), v.Description)
			if v.Confirm != "" {
				b.Confirm(v.Confirm)
			}
			op := r.Operations[local+"."+v.Name]
			if len(v.Params) > 0 {
				op.Parameters = maps.Clone(v.Params)
			}
			op.HistoryKind = a.historyKind(name, n.Name, v)
			r.Operations[op.Key()] = op
		}
	}
	mounted := maps.Clone(m.nouns)
	// listedBy maps a noun to the noun whose row source lists its rows.
	listedBy := map[string]string{}
	for _, n := range nouns {
		verb, rows := m.doc.RowSource(n.Name)
		if verb == "" || rows == n.Name {
			continue
		}
		if _, ok := mounted[rows]; !ok {
			a.warn("%s: %s %s lists rows of %s, which is not mounted; its rows answer to %s", name, n.Name, verb, rows, n.Name)
			if _, aliased := m.nouns[rows]; !aliased {
				m.nouns[rows] = mounted[n.Name]
			}
			continue
		}
		if _, taken := listedBy[rows]; !taken {
			listedBy[rows] = n.Name
		}
	}
	homes := map[string]string{} // noun -> the list its instances live in
	var lists []cli.CatalogList
	for _, n := range nouns {
		if _, ok := listedBy[n.Name]; ok {
			if verb, _ := m.doc.RowSource(n.Name); verb == "" {
				continue
			}
		}
		l := cli.CatalogList{ID: m.page + "/" + n.Name, Noun: n.Name}
		lists = append(lists, l)
		a.owners[mounted[n.Name]] = l.ID
		homes[n.Name] = l.ID
	}
	for noun, source := range listedBy {
		if _, own := homes[noun]; !own {
			homes[noun] = homes[source]
		}
	}
	for noun, list := range homes {
		a.homes[mounted[noun]] = list
	}
	if err := a.mountPage(m, lists); err != nil {
		return err
	}
	a.mountSources(m, nouns, mounted)
	a.loadBindings(m, e.Catalog, e.Catalog.Bindings, mounted, homes)
	a.exts = append(a.exts, m)
	return nil
}

// historyKind is the history kind a mounted verb records: the catalog
// verb's own (cli.CatalogVerb.History), so an extension's fetches stay out
// of the user's entries here as they do in the extension. A verb without
// one keeps the framework default (change, for an application verb); a
// kind the framework does not know is a notice and the default.
func (a *App) historyKind(ext, noun string, v cli.CatalogVerb) string {
	switch v.History {
	case "", ochazuke.HistoryAction, ochazuke.HistoryChange, ochazuke.HistoryFetch:
		return v.History
	}
	a.warn("%s: %s %s declares history %q, not action, change or fetch; recording it as a change", ext, noun, v.Name, v.History)
	return ""
}

// applicationDoc is a catalog with only its mountable verbs (planOf's
// rule), so row sources and verb rows are what nabe mounts; nouns keep
// their columns.
func applicationDoc(doc cli.CatalogDoc) cli.CatalogDoc {
	keep := mountableVerbs(doc)
	nouns := make([]cli.CatalogNoun, 0, len(doc.Nouns))
	for _, n := range doc.Nouns {
		n.Verbs = slices.DeleteFunc(slices.Clone(n.Verbs), func(v cli.CatalogVerb) bool { return !keep[n.Name+"."+v.Name] })
		nouns = append(nouns, n)
	}
	doc.Nouns = nouns
	return doc
}

// mountPage mounts the extension's data page: a groups rail over one list
// per mounted noun (cli.MountPage). The framework's timed reload fetches
// with execBin; the page's status line is nabe's (statusLine). An
// extension with no list gets a page saying so.
func (a *App) mountPage(m *mounted, lists []cli.CatalogList) error {
	r := a.r
	if len(lists) == 0 {
		_, err := r.AddPage(m.page, m.name, func(*ochazuke.Runtime, int, int) ochazuke.Block {
			return ochazuke.Text("no groups")
		})
		return err
	}
	spec := cli.CatalogPage{ID: m.page, Title: m.name, Layout: cli.LayoutGroups, Lists: lists}
	v, err := cli.MountPage(a.m, m.doc, spec, cli.PageConfig{
		Noun: m.local,
		Fetch: func(ctx context.Context, noun, verb string) ([]byte, error) {
			return execBin(ctx, m.bin, noun, verb, "--json", "{}")
		},
		// The timer's reload asks a delta-speaking state source what
		// changed since the rows the page holds (--since); an unchanged
		// answer touches no row. Off the loop: freshNext is atomic.
		FetchFlags: func(ctx context.Context, noun, verb string, flags []string) ([]byte, error) {
			args := "{}"
			if m.freshNext.Swap(false) {
				args = m.args(noun+"."+verb, true)
			}
			return execBin(ctx, m.bin, append([]string{noun, verb, "--json", args}, flags...)...)
		},
		OnRows: func(list string, rows []cli.Row) { a.listChoices(m, list, rows) },
	})
	if err != nil {
		return err
	}
	m.view, m.groups = v, r.List(m.page+"/groups")
	page := r.PageOf(m.page)
	render := page.Render
	page.Render = func(rt *ochazuke.Runtime, width, height int) ochazuke.Block {
		v.SetStatus(a.statusLine(m))
		return render(rt, width, height)
	}
	return nil
}

// statusLine is the muted line under the page's list: a running load, the
// failed pick-list sources and the last verb's result.
func (a *App) statusLine(m *mounted) string {
	var parts []string
	if m.refreshAsked && !a.pageLoading(m) {
		m.refreshAsked = false
	}
	if a.refreshing(m) || m.refreshAsked {
		parts = append(parts, map[bool]string{false: "loading…", true: "refreshing…"}[m.loaded])
	}
	parts = append(parts, m.sourceErrs()...)
	if m.status != "" {
		parts = append(parts, m.status)
	}
	return strings.Join(parts, " · ")
}

// loadBindings installs the catalog rows of the mounted nouns, renamed to
// the nabe nouns; homes is the list each noun's instances live in.
//
// A catalog lists every row the extension's own runtime holds, the
// framework's included, so some rows are skipped quietly, by data:
//   - a row in a mode the host does not define (the extension's own modal
//     mode, such as a copy mode's pending find);
//   - a row whose request is a verb of the catalog that nabe does not
//     mount (an interaction or builtin verb, a verb of a noun skipped for
//     its modal pin) and the host does not register: it runs only inside
//     the extension's own process;
//   - a row the host already holds: its first key is bound on the row's
//     path to the same request (the framework's own rows echoed back,
//     ctrl+c app.quit).
//
// Every other problem is a notice: an instance row, a request naming
// nothing the catalog or the host knows, a key the host binds to another
// request, an unknown guard.
func (a *App) loadBindings(m *mounted, catalog cli.CatalogDoc, rows []ochazuke.Binding, nouns, homes map[string]string) {
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
		if row.Mode != "" && !a.r.Modes[row.Mode] && !a.r.IsPendingMode(row.Mode) {
			continue // the extension's own mode
		}
		if req, ok := nouns[row.Request.Noun]; ok && a.mountedVerb(req, row.Request.Verb) {
			row.Request.Noun = req
		} else if _, ok := a.r.Operation(row.Request.Key()); ok {
			// a verb of the host (the framework's)
		} else if catalogVerb(catalog, row.Request.Noun, row.Request.Verb) {
			continue // the extension's own process runs it
		} else {
			a.warn("%s dropped: %s is not mounted", where, row.Request.Key())
			continue
		}
		listID := homes[row.Noun]
		row.Noun = local
		if owner, taken := a.taken(listID, row); taken {
			if a.sameRequest(owner.Request, row.Request) {
				continue // the host already answers the key so
			}
			a.warn("%s dropped: taken by %s (%s)", where, a.r.DisplayNoun(owner.Noun), a.r.DisplayKey(owner.Request.Key()))
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

// mountedVerb reports whether the mounted noun has the verb.
func (a *App) mountedVerb(noun, verb string) bool {
	_, ok := a.r.Operations[noun+"."+verb]
	return ok
}

// catalogVerb reports whether the catalog declares noun.verb (noun by its
// name or an alias the catalog publishes).
func catalogVerb(doc cli.CatalogDoc, noun, verb string) bool {
	noun = doc.CanonicalNoun(noun)
	for _, n := range doc.Nouns {
		if n.Name != noun {
			continue
		}
		for _, v := range n.Verbs {
			if v.Name == verb {
				return true
			}
		}
	}
	return false
}

// sameRequest reports two requests naming one operation (aliases
// resolved) with the same arguments.
func (a *App) sameRequest(x, y ochazuke.Request) bool {
	x, y = a.r.CanonicalRequest(x), a.r.CanonicalRequest(y)
	if x.Key() != y.Key() {
		return false
	}
	xa, errX := json.Marshal(x.Args)
	ya, errY := json.Marshal(y.Args)
	return errX == nil && errY == nil && string(xa) == string(ya)
}

// taken reports the framework or nabe row a mounted row would shadow: an
// unguarded row with the same first key, in the same mode or in any mode,
// on a noun of the row's path (its list, the page, app; nabe's own rows
// there too); owner.Noun is that noun. Pattern rows (digit, any) are not
// compared. A guarded row does not take its key: it answers only while
// its guard holds and falls through otherwise, and the row's own noun
// answers before it (UX-RULES R5.11: a row noun's own h or l answers
// before a table's scroll-then-focus rows).
func (a *App) taken(listID string, row ochazuke.Binding) (owner ochazuke.Binding, taken bool) {
	if row.Keys == "" {
		return owner, false
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
				if b.Keys != "" && b.When == "" && strings.Fields(b.Keys)[0] == first {
					b.Noun = name
					return b, true
				}
			}
		}
	}
	return owner, false
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
	if m.every <= 0 || m.view == nil {
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
		m.status = err.Error()
	}
}

// tick is one timed refresh: the page's rows (PageView.Reload) and the
// pick-list sources no list answers, then wait again.
func (a *App) tick(m *mounted) {
	if err := m.view.Reload(); err != nil {
		m.status = err.Error()
	}
	a.loadSources(m, false)
	a.arm(m)
}

// afterVerb refreshes what a verb of noun may have changed: the state,
// else only the lists that hold the noun's rows.
func (a *App) afterVerb(m *mounted, noun string) {
	if m.doc.State != "" {
		a.reloadRows(m, false)
		return
	}
	local := localNoun(m.name, noun)
	for _, l := range m.specs() {
		if m.source(l) != "" && (m.local(l.Noun) == local || m.rowNoun(l) == local) {
			a.loadList(m, l, false)
		}
	}
}

// reload refreshes every list of m (reloadRows) and the pick-list sources
// no list answers (loadSources). fresh asks a source that declares it to
// skip its cache. An explicit refresh (fresh) of a delta-speaking state
// source is PageView.Refresh: the page forgets its list hashes and asks
// for every list in full ({"fresh":true} when the source declares it).
func (a *App) reload(m *mounted, fresh bool) {
	if fresh && m.deltas() {
		m.freshNext.Store(true)
		m.refreshAsked = true
		if err := m.view.Refresh(); err != nil {
			m.freshNext.Store(false)
			m.refreshAsked = false
			a.listErr(m, err)
		}
	} else {
		a.reloadRows(m, fresh)
	}
	a.loadSources(m, fresh)
}

// reloadRows refreshes every list of m: one state exec whose answer fills
// each list by its noun, else one row-source exec per list.
func (a *App) reloadRows(m *mounted, fresh bool) {
	if m.view == nil {
		return
	}
	if m.doc.State == "" {
		for _, l := range m.specs() {
			if m.source(l) != "" {
				a.loadList(m, l, fresh)
			}
		}
		return
	}
	a.streamState(m, fresh, false)
}

// streamState runs the state exec, its stdout read in batches (nextBatch).
// A delta-speaking source is sent the page's list hashes
// (PageView.SinceFlags), so it answers only what changed; retry is the one
// full refetch of lists whose hashes did not match after applying.
func (a *App) streamState(m *mounted, fresh, retry bool) {
	noun, verb, _ := strings.Cut(m.doc.State, ".")
	args := []string{noun, verb, "--json", m.args(m.doc.State, fresh)}
	if m.deltas() {
		args = append(args, m.view.SinceFlags()...)
	}
	if m.stream != nil {
		m.stream.stop() // a newer load replaces it
	}
	st := newStateLoad()
	st.retry = retry
	m.stream = st
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
	rows   []cli.Row       // rows so far, every noun
	seen   map[string]bool // noun/id already filed
	retry  bool            // the refetch after a hash mismatch
}

// whole reports whether every list the answer has named so far is
// answered in full, so the rows so far replace lists (SetState): a plain
// answer, or a delta answer of full lists (the first load: no hashes).
func (st *stateLoad) whole() bool {
	a := st.answer()
	return !a.Delta || !slices.ContainsFunc(a.Lists, func(l cli.StateList) bool { return l.Answer != cli.AnswerFull })
}

// answer is the stream's answer so far (RowReader.Answer); a plain one
// before the exec starts.
func (st *stateLoad) answer() cli.StateAnswer {
	if st.rr == nil {
		return cli.StateAnswer{}
	}
	return st.rr.Answer()
}

func newStateLoad() *stateLoad { return &stateLoad{seen: map[string]bool{}} }

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

// stateBatch files a batch's rows under their lists (PageView.SetState);
// the first load shows them at once, and the stream's end sets every list
// (a noun without lines has no rows). A delta answer is applied whole at
// the stream's end (PageView.ApplyState): an unchanged list is not
// touched, a changed one patched; lists whose hash then does not match
// are fetched again in full, once.
func (a *App) stateBatch(m *mounted, st *stateLoad, b stateBatch) {
	touched := false
	for _, row := range b.rows {
		if st.seen[row.Noun+"\x00"+row.ID] {
			continue
		}
		st.seen[row.Noun+"\x00"+row.ID] = true
		st.rows = append(st.rows, row)
		touched = true
	}
	if !b.done && !m.loaded && touched && st.whole() {
		if err := m.view.SetState(st.rows); err != nil {
			a.listErr(m, err)
		}
	}
	if !b.done {
		return
	}
	m.loaded, m.stream = true, nil
	st.stop()
	answer := st.answer()
	if !answer.Delta {
		if err := m.view.SetState(st.rows); err != nil {
			a.listErr(m, err)
		}
		return
	}
	bad, err := m.view.ApplyState(answer)
	if err != nil {
		a.listErr(m, err)
	}
	if len(bad) > 0 && !st.retry {
		a.streamState(m, false, true)
	}
}

// stateFailed is a load that failed entirely: the rows stay, each list
// says why.
func (a *App) stateFailed(m *mounted, st *stateLoad, err error) {
	st.stop()
	m.stream = nil
	a.listErr(m, err)
}

// listErr shows err on every list of rows.
func (a *App) listErr(m *mounted, err error) {
	for _, l := range m.specs() {
		if m.source(l) != "" {
			m.view.SetError(l.ID, err)
		}
	}
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
func (a *App) loadList(m *mounted, l cli.CatalogList, fresh bool) {
	verb := m.source(l)
	args := m.args(l.Noun+"."+verb, fresh)
	a.load(l.ID, func(ctx context.Context) (any, error) {
		return execBin(ctx, m.bin, l.Noun, verb, "--json", args)
	}, func(v any, err error) {
		if err == nil {
			out, _ := v.([]byte)
			err = a.listRows(m, l, out)
		}
		if err != nil {
			m.view.SetError(l.ID, err)
		}
		m.listsLoaded[l.ID] = true
		m.loaded = !slices.ContainsFunc(m.specs(), func(l cli.CatalogList) bool { return m.source(l) != "" && !m.listsLoaded[l.ID] })
	})
}

// listRows shows a list answer (a JSON array or JSON Lines of rows) in l.
func (a *App) listRows(m *mounted, l cli.CatalogList, out []byte) error {
	rows, err := cli.DecodeRows(l.Noun, out)
	if err != nil {
		return fmt.Errorf("%s %s: %w", l.Noun, m.source(l), err)
	}
	return m.view.SetRows(l.ID, rows)
}

// load runs fetch as a quiet job in owner's rows slot (a newer load
// replaces an older one) and apply on its completion.
func (a *App) load(owner string, fetch func(context.Context) (any, error), apply func(any, error)) {
	a.loadIn(owner, "rows", fetch, apply)
}

// loadIn is load in a slot of its own.
func (a *App) loadIn(owner, slot string, fetch func(context.Context) (any, error), apply func(any, error)) {
	res := ochazuke.Pending(fetch).Replacing(slot).Then(func(_ ochazuke.Ctx, v any, err error) { apply(v, err) })
	if _, err := a.r.Launch(owner, refreshVerb, res, ochazuke.Ctx{Runtime: a.r}); err != nil {
		apply(nil, err)
	}
}

// pageLoading reports whether the page's own reload (PageView.Reload or
// Refresh, under cli.LoadVerb) runs.
func (a *App) pageLoading(m *mounted) bool {
	return slices.ContainsFunc(a.r.Jobs(), func(j ochazuke.JobInfo) bool {
		return j.State == ochazuke.JobRunning && j.Verb == cli.LoadVerb && j.Owner == m.page
	})
}

// refreshing reports whether a load of m runs.
func (a *App) refreshing(m *mounted) bool {
	for _, j := range a.r.Jobs() {
		if j.State != ochazuke.JobRunning || j.Verb != refreshVerb {
			continue
		}
		if j.Owner == m.page || slices.ContainsFunc(m.specs(), func(l cli.CatalogList) bool { return l.ID == j.Owner }) {
			return true
		}
	}
	return false
}
