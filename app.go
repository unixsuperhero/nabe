package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/unixsuperhero/ochazuke"
)

// Options are the command line.
type Options struct {
	Data string // the nabe data directory; the registry is Data/catalogs
	// Sync runs application work inline instead of as pending jobs: the
	// CLI has no loop to come back to, and prints the value it returns.
	Sync bool
}

// App owns the registry and what it mounted; the runtime owns interaction
// state.
type App struct {
	reg  Registry
	sync bool

	exts   []*mounted        // one per registry file that mounted
	owners map[string]string // noun -> the instance a CLI command of it acts for

	r       *ochazuke.Runtime
	m       *ochazuke.Model
	extList *ochazuke.List
}

// extensionNoun is nabe's one noun of its own.
const extensionNoun = "extension"

// The extensions page and its list.
const (
	extensionsPage = "extensions"
	extensionsList = "extensions/list"
)

// New builds the runtime: one page per registry file (mount.go), then the
// extensions page. The first extension page opens when there is one.
func New(opts Options) (*App, error) {
	r := ochazuke.NewRuntime()
	a := &App{reg: RegistryIn(opts.Data), sync: opts.Sync, owners: map[string]string{}, r: r, m: ochazuke.NewModel(r)}
	a.m.Title = "nabe"
	a.defineExtension()
	if err := a.mountAll(); err != nil {
		return nil, err
	}
	if _, err := r.AddPage(extensionsPage, "Extensions", a.renderExtensions); err != nil {
		return nil, err
	}
	list, err := r.AddList(extensionsList, nil, ochazuke.Parent(extensionsPage))
	if err != nil {
		return nil, err
	}
	a.extList = list
	a.owners[extensionNoun] = extensionsList
	if err := a.refreshExtensions(); err != nil {
		return nil, err
	}
	if _, err := r.ValidateBindings(); err != nil {
		return nil, err
	}
	first := extensionsPage
	if len(a.exts) > 0 {
		first = a.exts[0].page
	}
	if err := r.OpenPage(first); err != nil {
		return nil, err
	}
	return a, a.focusPage(first)
}

// focusPage focuses a page's first list: an extension page's lists with
// rows come first, and an empty list hands the focus to its first row when
// the rows arrive.
func (a *App) focusPage(page string) error {
	for _, e := range a.exts {
		if e.page == page && len(e.lists) > 0 {
			return e.lists[0].list.Focus()
		}
	}
	if page == extensionsPage {
		return a.extList.Focus()
	}
	return nil
}

// work runs fn as a pending job whose completion runs then on the loop,
// or inline in sync mode.
func (a *App) work(fn func(context.Context) (any, error), then func(any, error)) ochazuke.Result {
	if a.sync {
		v, err := fn(context.Background())
		if then != nil {
			then(v, err)
		}
		if err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(v)
	}
	return ochazuke.Pending(fn).Then(func(_ ochazuke.Ctx, v any, err error) {
		if then != nil {
			then(v, err)
		}
	})
}

// defineExtension is the extension noun: plain application verbs over the
// registry, the same from the CLI, the palette and the extensions page.
func (a *App) defineExtension() {
	a.r.Define(extensionNoun).
		Verb("register", a.register, "Run BIN catalog, validate it and write the registry file (restart nabe to mount it)").
		Verb("unregister", a.unregister, "Remove an extension's registry file").Confirm("Unregister {{id}}?").
		Verb("refresh", a.refresh, "Run catalog again for one extension, or every one").
		Verb("list", a.list, "List the registry files as JSON").
		Mode("normal").
		Bind("r", ochazuke.Do("extension.refresh")).
		Bind("d", ochazuke.Do("extension.unregister"))
	params := map[string]map[string]ochazuke.Parameter{
		"register":   {"bin": {Type: "string", Required: true, Description: "A path, or a name on PATH"}, "name": {Type: "string", Description: "The registry name (default: the catalog's name)"}},
		"unregister": {"name": {Type: "string", Description: "Omit on an extension row"}},
		"refresh":    {"name": {Type: "string", Description: "Omit to refresh every extension"}},
	}
	for verb, p := range params {
		op := a.r.Operations[extensionNoun+"."+verb]
		op.Parameters = p
		a.r.Operations[op.Key()] = op
	}
}

// extName is the name argument, else the extension row the verb runs on.
func extName(c ochazuke.Ctx) string {
	if name := ochazuke.StringArg(c.Args, "name", ""); name != "" {
		return name
	}
	if c.Instance != nil && c.Instance.Noun == extensionNoun {
		return c.Instance.ID
	}
	return ""
}

func (a *App) register(c ochazuke.Ctx) ochazuke.Result {
	bin, name := ochazuke.StringArg(c.Args, "bin", ""), ochazuke.StringArg(c.Args, "name", "")
	return a.work(func(ctx context.Context) (any, error) {
		return a.reg.Register(ctx, bin, name)
	}, a.afterRegistry)
}

func (a *App) unregister(c ochazuke.Ctx) ochazuke.Result {
	name := extName(c)
	if name == "" {
		return ochazuke.Failed(errors.New("unregister needs a name"))
	}
	if err := a.reg.Unregister(name); err != nil {
		return ochazuke.Failed(err)
	}
	a.afterRegistry(nil, nil)
	return ochazuke.Done(map[string]string{"unregistered": name})
}

func (a *App) refresh(c ochazuke.Ctx) ochazuke.Result {
	name := extName(c)
	return a.work(func(ctx context.Context) (any, error) {
		names := []string{name}
		if name == "" {
			var err error
			if names, err = a.reg.Names(); err != nil {
				return nil, err
			}
		}
		out := []Summary{}
		var errs []error
		for _, n := range names {
			s, err := a.reg.Refresh(ctx, n)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			out = append(out, s)
		}
		return out, errors.Join(errs...)
	}, a.afterRegistry)
}

func (a *App) list(ochazuke.Ctx) ochazuke.Result {
	rows, err := a.reg.List()
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(rows)
}

// afterRegistry reloads the extensions list after a registry change.
func (a *App) afterRegistry(any, error) {
	if a.extList != nil {
		if err := a.refreshExtensions(); err != nil {
			a.m.Status = err.Error()
		}
	}
}

// refreshExtensions pushes the registry files to the extensions list.
func (a *App) refreshExtensions() error {
	rows, err := a.reg.List()
	if err != nil {
		return err
	}
	items := make([]ochazuke.ListItem, 0, len(rows))
	for _, l := range rows {
		label := fmt.Sprintf("%s  %s  %s  %d nouns, %d verbs", l.Name, l.Version, l.Bin, l.Nouns, l.Verbs)
		if l.Error != "" {
			label = l.Name + "  error: " + l.Error
		}
		items = append(items, ochazuke.ListItem{ID: l.Name, Label: label, Noun: extensionNoun})
	}
	return a.extList.SetItems(items)
}

func (a *App) renderExtensions(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	s := ochazuke.NewStyles(*a.m.Appearance)
	n := a.extList.Len()
	head := s.Muted.Render(fmt.Sprintf("registry %s (%d)", a.reg.Dir, n))
	if n == 0 {
		return ochazuke.Text(head + "\n" + "no extensions registered: nabe extension register BIN")
	}
	return ochazuke.Text(head + "\n" + strings.TrimRight(a.extList.View(width, max(1, min(n, height-3))), "\n"))
}
