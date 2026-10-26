package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/cli"
)

// deltaEnv makes the test binary the delta-speaking fake extension
// (deltaExt) instead of running the tests.
const deltaEnv = "NABE_DELTA_EXT"

func TestMain(m *testing.M) {
	if os.Getenv(deltaEnv) != "" {
		os.Exit(deltaExt(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// deltaExt is a fake extension built on ochazuke's cli.Shell, so its state
// verb answers the state delta protocol (stateDelta: --since hashes, its
// StateCache under $XDG_CACHE_HOME). Like testdata/fake-ext its items are
// $FAKE_STATE/items ("id name" lines) and each call is a line of
// $FAKE_STATE/calls.
func deltaExt(args []string) int {
	state := os.Getenv("FAKE_STATE")
	if f, err := os.OpenFile(filepath.Join(state, "calls"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintln(f, strings.Join(args, " "))
		f.Close()
	}
	items := func() (out cli.Lines, err error) {
		data, err := os.ReadFile(filepath.Join(state, "items"))
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if id, name, ok := strings.Cut(line, " "); ok {
				out = append(out, cli.Row{Noun: "item", ID: id, Label: name})
			}
		}
		return out, err
	}
	r := ochazuke.NewRuntime()
	r.Define("item").
		Verb("list", func(ochazuke.Ctx) ochazuke.Result {
			rows, err := items()
			if err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(rows)
		}, "List the items").Fetch().
		Verb("state", func(ochazuke.Ctx) ochazuke.Result {
			rows, err := items()
			if err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(append(rows, cli.Row{Noun: "projects", ID: "p1", Label: "pone"}))
		}, "Every list").State().Fetch().
		Verb("touch", func(c ochazuke.Ctx) ochazuke.Result {
			return ochazuke.Done(map[string]string{"touched": "x"})
		}, "Touch an item")
	r.Operations["item.state"] = withParam(r.Operations["item.state"], freshParam, ochazuke.Parameter{Type: "boolean"})
	r.Define("projects").Verb("list", func(ochazuke.Ctx) ochazuke.Result {
		return ochazuke.Done(cli.Lines{cli.Row{Noun: "projects", ID: "p1", Label: "pone"}})
	}, "List the projects").Fetch()
	sh := cli.New(r, "fake-delta", os.Stdin, os.Stdout, os.Stderr)
	if len(args) == 1 && args[0] == "catalog" {
		data, err := cli.Catalog(r, "fake-delta", "0.1.0")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		os.Stdout.Write(data)
		return 0
	}
	if err := sh.Run(context.Background(), args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func withParam(op ochazuke.Operation, name string, p ochazuke.Parameter) ochazuke.Operation {
	if op.Parameters == nil {
		op.Parameters = map[string]ochazuke.Parameter{}
	}
	op.Parameters[name] = p
	return op
}

// deltaTUI is the app over a registry with the delta fake registered as
// fake, its rows loaded.
func deltaTUI(t *testing.T) (*App, string) {
	t.Helper()
	_, state, data := fake(t)
	t.Setenv(deltaEnv, "1")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := nabe(t, "-data", data, "extension", "register", bin, "fake"); err != nil {
		t.Fatalf("register: %v\n%s", err, errOut)
	}
	a, err := New(Options{Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if m := a.exts[0]; !m.deltas() {
		t.Fatalf("the delta fake's catalog does not declare stateDelta: %+v", m.doc.StateDelta)
	}
	a.LoadRows()
	if err := cli.Settle(a.r, nil); err != nil {
		t.Fatal(err)
	}
	return a, state
}

// rowWatch marks every row instance of the page's lists (a label SetItems
// rewrites) and remembers the instances, so a poll that touched a list or
// made an instance shows.
type rowWatch struct {
	a     *App
	insts map[string]*ochazuke.Instance
}

const untouched = "\x00untouched"

// hashed matches a --since that sends a list hash.
var hashed = regexp.MustCompile(`(^|[=,])item=[0-9a-f]`)

func watchRows(a *App) rowWatch {
	w := rowWatch{a, map[string]*ochazuke.Instance{}}
	for _, l := range a.exts[0].specs() {
		for _, item := range a.r.List(l.ID).Items() {
			inst := a.r.Instances[item.ID]
			inst.Label = untouched
			w.insts[item.ID] = inst
		}
	}
	return w
}

// touched are the lists whose rows SetItems rewrote; created the row
// instances that are not the ones watched.
func (w rowWatch) touched() (lists []string, created int) {
	for _, l := range w.a.exts[0].specs() {
		for _, item := range w.a.r.List(l.ID).Items() {
			inst := w.a.r.Instances[item.ID]
			if inst != w.insts[item.ID] {
				created++
			}
			if inst.Label != untouched && !slices.Contains(lists, l.ID) {
				lists = append(lists, l.ID)
			}
		}
	}
	return lists, created
}

func settle(t *testing.T, a *App) {
	t.Helper()
	if err := cli.Settle(a.r, nil); err != nil {
		t.Fatal(err)
	}
}

// A delta-speaking state source: the first load asks for every list
// (--since=), and an unchanged poll, from the timer (PageView.Reload
// through FetchFlags) or after a verb (nabe's own stream), sends the
// page's hashes and touches no row: no SetItems, no instance. A changed
// row is patched in place (the other rows and every instance kept); an
// explicit refresh asks for every list in full, uncached.
func TestStateDeltasTouchNothingWhenUnchanged(t *testing.T) {
	a, state := deltaTUI(t)
	m := a.exts[0]
	if got := calls(t, state); !slices.ContainsFunc(got, func(c string) bool {
		return strings.HasPrefix(c, "item state --json {} --since=") && !hashed.MatchString(c)
	}) {
		t.Fatalf("first load execs %q, want item state --since=", got)
	}
	if a.r.List("ext/fake/item").Len() != 2 || a.r.List("ext/fake/projects").Len() != 1 {
		t.Fatalf("rows: %d items, %d projects", a.r.List("ext/fake/item").Len(), a.r.List("ext/fake/projects").Len())
	}
	since := strings.Join(m.view.SinceFlags(), " ")
	if !hashed.MatchString(since) || !strings.Contains(since, "--since-cache") {
		t.Fatalf("after the first load the page holds no hashes: %q", since)
	}

	// The timer's poll, nothing changed.
	w := watchRows(a)
	mark := len(calls(t, state))
	m.every = 0
	a.tick(m)
	settle(t, a)
	got := execsAfter(t, state, mark)
	if len(got) != 1 || !strings.HasPrefix(got[0], "item state --json {} --since-cache ") || !hashed.MatchString(got[0]) {
		t.Fatalf("timer poll execs %q", got)
	}
	if lists, created := w.touched(); len(lists) != 0 || created != 0 {
		t.Fatalf("an unchanged timer poll touched %q and made %d instances", lists, created)
	}

	// After a verb (nabe's stream), nothing changed.
	mark = len(calls(t, state))
	a.reloadRows(m, false)
	settle(t, a)
	if got := execsAfter(t, state, mark); len(got) != 1 || !strings.Contains(got[0], "--since-cache") {
		t.Fatalf("stream poll execs %q", got)
	}
	if lists, created := w.touched(); len(lists) != 0 || created != 0 {
		t.Fatalf("an unchanged streamed poll touched %q and made %d instances", lists, created)
	}

	// One row changed: patched, its instance and the others kept, the
	// projects list untouched.
	for _, c := range []struct {
		name string
		poll func()
	}{{"BETA", func() { a.tick(m) }}, {"beta", func() { a.reloadRows(m, false) }}} {
		if err := os.WriteFile(filepath.Join(state, "items"), []byte("a1 alpha\nb2 "+c.name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		w = watchRows(a)
		c.poll()
		settle(t, a)
		lists, created := w.touched()
		if created != 0 || !slices.Equal(lists, []string{"ext/fake/item"}) || a.r.Instances["ext/fake/item/b2"].Label != c.name {
			t.Fatalf("a one-row change to %s: touched %q, %d instances made, b2 %q", c.name, lists, created, a.r.Instances["ext/fake/item/b2"].Label)
		}
	}

	// An explicit refresh: every list in full, uncached, through
	// PageView.Refresh.
	mark = len(calls(t, state))
	a.reload(m, true)
	settle(t, a)
	got = execsAfter(t, state, mark)
	if !slices.ContainsFunc(got, func(c string) bool {
		return strings.HasPrefix(c, `item state --json {"fresh":true}`) && strings.Contains(c, "--since=") && !hashed.MatchString(c)
	}) {
		t.Fatalf("explicit refresh execs %q, want item state fresh, no hashes", got)
	}
	if m.freshNext.Load() {
		t.Fatal("freshNext still set after the refresh")
	}
	if a.r.List("ext/fake/item").Len() != 2 || a.r.Instances["ext/fake/item/b2"].Label != "beta" {
		t.Fatalf("after refresh: %d rows", a.r.List("ext/fake/item").Len())
	}
	frame(a)
	if m.refreshAsked {
		t.Fatal("the status line still says refreshing after the refresh landed")
	}
}
