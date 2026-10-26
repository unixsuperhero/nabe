package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/cli"
	"github.com/unixsuperhero/oden"
)

// fake prepares the fake extension's state (two items) and returns the
// script's absolute path, the state directory and a fresh data directory.
func fake(t *testing.T) (bin, state, data string) {
	t.Helper()
	state = t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "items"), []byte("a1 alpha\nb2 beta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_STATE", state)
	t.Setenv("FAKE_MODE", "")
	bin, err := filepath.Abs(filepath.Join("testdata", "fake-ext"))
	if err != nil {
		t.Fatal(err)
	}
	return bin, state, t.TempDir()
}

// nabe runs one command line.
func nabe(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := run(args, strings.NewReader(""), &out, &errOut)
	return out.String(), errOut.String(), err
}

// registered is a data directory with the fake extension registered.
func registered(t *testing.T) (state, data string) {
	t.Helper()
	bin, state, data := fake(t)
	if _, errOut, err := nabe(t, "-data", data, "extension", "register", bin); err != nil {
		t.Fatalf("register: %v\n%s", err, errOut)
	}
	return state, data
}

func calls(t *testing.T, state string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(state, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestRegisterWritesTheFileAndPrintsTheSummary(t *testing.T) {
	bin, _, data := fake(t)
	out, errOut, err := nabe(t, "-data", data, "extension", "register", bin)
	if err != nil {
		t.Fatalf("register: %v\n%s", err, errOut)
	}
	var s Summary
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("stdout %q: %v", out, err)
	}
	if want := "fake: 4 nouns, 7 verbs, 0 pages (framework nouns skipped: 2)"; s.Summary != want {
		t.Fatalf("summary %q, want %q", s.Summary, want)
	}
	raw, err := os.ReadFile(filepath.Join(data, "catalogs", "fake.json"))
	if err != nil {
		t.Fatal(err)
	}
	var e Entry
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	if e.Bin != bin || e.RegisteredAt.IsZero() || e.Catalog.Name != "fake" || len(e.Catalog.Nouns) != 6 {
		t.Fatalf("entry = %+v", e)
	}
}

func TestRegisterByNameOnPathWithPrefixesAndName(t *testing.T) {
	bin, _, data := fake(t)
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, errOut, err := nabe(t, "-data", data, "ext", "reg", "fake-ext", "--name", "other")
	if err != nil || !strings.Contains(out, `"summary": "other: 4 nouns`) {
		t.Fatalf("ext reg: %v %q\n%s", err, out, errOut)
	}
	if e, err := (RegistryIn(data)).Read("other"); err != nil || e.Bin != bin {
		t.Fatalf("entry %+v: %v", e, err)
	}
	// Bare words fill bin, then name (the shell's positional order).
	if out, errOut, err := nabe(t, "-data", data, "ext", "reg", "fake-ext", "third"); err != nil || !strings.Contains(out, `"summary": "third: 4 nouns`) {
		t.Fatalf("ext reg BIN NAME: %v %q\n%s", err, out, errOut)
	}
	if _, _, err := nabe(t, "-data", data, "ext", "reg", "fake-ext", "x", "1", "y"); err == nil || !strings.Contains(err.Error(), `unexpected argument "y"`) {
		t.Fatalf("too many words: %v", err)
	}
}

func TestListRefreshAndUnregister(t *testing.T) {
	state, data := registered(t)
	out, _, err := nabe(t, "-data", data, "extension", "list")
	var rows []Listing
	if err != nil || json.Unmarshal([]byte(out), &rows) != nil || len(rows) != 1 || rows[0].Name != "fake" || rows[0].Nouns != 4 || rows[0].Verbs != 7 {
		t.Fatalf("list: %v %q", err, out)
	}
	if out, _, err := nabe(t, "-data", data, "extension", "refresh"); err != nil || !strings.Contains(out, `"summary":"fake: 4 nouns`) {
		t.Fatalf("refresh: %v %q", err, out)
	}
	if n := strings.Count(strings.Join(calls(t, state), "\n"), "catalog"); n != 2 {
		t.Fatalf("catalog ran %d times", n)
	}
	if _, _, err := nabe(t, "-data", data, "extension", "unregister", "fake", "--yes"); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if _, err := os.Stat(filepath.Join(data, "catalogs", "fake.json")); !os.IsNotExist(err) {
		t.Fatalf("file still there: %v", err)
	}
	if _, _, err := nabe(t, "-data", data, "extension", "unregister", "fake", "--yes"); err == nil || !strings.Contains(err.Error(), "fake is not registered") {
		t.Fatalf("second unregister: %v", err)
	}
}

func TestRegisterErrors(t *testing.T) {
	bin, _, data := fake(t)
	for _, c := range []struct{ mode, bin, want string }{
		{"schema2", bin, "fake-ext catalog: schema 2, nabe reads schema 1"},
		{"fail", bin, "fake-ext catalog failed: exit status 3: catalog broke"},
		{"", filepath.Join(t.TempDir(), "missing"), "bin " + filepath.Join(os.TempDir(), "")},
	} {
		t.Setenv("FAKE_MODE", c.mode)
		_, _, err := nabe(t, "-data", data, "extension", "register", c.bin)
		if err == nil || (c.mode != "" && err.Error() != c.want) || (c.mode == "" && !strings.HasPrefix(err.Error(), "bin ")) {
			t.Errorf("mode %q: err = %v, want %q", c.mode, err, c.want)
		}
	}
	if names, _ := RegistryIn(data).Names(); len(names) != 0 {
		t.Fatalf("a failed register wrote %v", names)
	}
	if _, err := parseCatalog("x", []byte(`{"schema":1,"nouns":[]}`)); err == nil || err.Error() != `x catalog: missing required field "name"` {
		t.Fatalf("missing name: %v", err)
	}
	if _, err := parseCatalog("x", []byte(`[1]`)); err == nil || !strings.Contains(err.Error(), "not a JSON object") {
		t.Fatalf("array: %v", err)
	}
}

// tui is the app over a registry with the fake mounted and its rows loaded.
func tui(t *testing.T) (*App, string) {
	t.Helper()
	state, data := registered(t)
	a, err := New(Options{Data: data})
	if err != nil {
		t.Fatal(err)
	}
	a.LoadRows()
	if err := cli.Settle(a.r, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.r.List("ext/fake/item").Focus(); err != nil {
		t.Fatal(err)
	}
	return a, state
}

// press sends one key through the model and returns the work it scheduled.
func press(a *App, key tea.KeyPressMsg) tea.Cmd {
	_, cmd := a.m.Update(key)
	return cmd
}

// drain runs cmd and feeds every message back through the model.
func drain(a *App, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			drain(a, c)
		}
	case nil:
	default:
		_, next := a.m.Update(msg)
		drain(a, next)
	}
}

func frame(a *App) string {
	a.m.Width, a.m.Height = 80, 24
	return ansi.Strip(a.m.View().Content)
}

func TestTUIMountsThePageWithTheListRows(t *testing.T) {
	a, _ := tui(t)
	if a.r.Page != "ext/fake" {
		t.Fatalf("page %q", a.r.Page)
	}
	l := a.r.List("ext/fake/item")
	if sel, ok := l.Selected(); !ok || sel.ID != "ext/fake/item/a1" || sel.Noun != "fake-item" || sel.Data != "a1" || a.r.Instances[sel.ID].Data != "a1" {
		t.Fatalf("selected %+v", sel)
	}
	if a.r.Focus != "ext/fake/item/a1" {
		t.Fatalf("focus %q", a.r.Focus)
	}
	if rows := a.r.Instances["fake-tool/echo"].Bindings; len(rows) != 0 {
		t.Fatalf("a verb with required parameters got rows %+v", rows)
	}
	if a.r.Noun("fake-app") != nil {
		t.Fatal("the interaction-only noun app was mounted")
	}
	if a.r.Noun("fake-field") != nil {
		t.Fatal("the noun pinned to insert was mounted")
	}
	if p := a.r.Operations["fake-item.touch"].Parameters["note"]; p.Type != "string" || p.Source != "" {
		t.Fatalf("touch params %+v", p)
	}
	view := frame(a)
	for _, want := range []string{"fake", "Groups", "item (2)", "alpha", "beta", "tool", "projects", "Extensions"} {
		if !strings.Contains(view, want) {
			t.Errorf("frame lacks %q:\n%s", want, view)
		}
	}
}

func TestBoundKeyRunsTheVerbAsAPendingJob(t *testing.T) {
	a, state := tui(t)
	cmd := press(a, tea.KeyPressMsg{Code: 't', Text: "t"})
	out, err := a.r.Execute(ochazuke.Request{Noun: "jobs", Verb: "list"})
	if err != nil {
		t.Fatal(err)
	}
	var jobs []ochazuke.JobInfo
	for _, j := range out.([]ochazuke.JobInfo) {
		if j.State == ochazuke.JobRunning {
			jobs = append(jobs, j)
		}
	}
	if len(jobs) != 1 || jobs[0].Verb != "fake-item.touch" || jobs[0].State != ochazuke.JobRunning || jobs[0].Owner != "ext/fake/item/a1" {
		t.Fatalf("jobs = %+v", jobs)
	}
	before := len(calls(t, state))
	drain(a, cmd)
	got := calls(t, state)[before:]
	// Only the verb's own list reloads (the extension has no state source).
	if len(got) != 2 || got[0] != "item touch --target a1 --json {} --yes" || got[1] != "item list --json {}" {
		t.Fatalf("calls after t: %q", got)
	}
	if view := frame(a); !strings.Contains(view, `item touch: {"touched":"a1"}`) {
		t.Fatalf("status missing:\n%s", view)
	}
	// A verb row: enter runs its own verb.
	if err := a.r.SetFocus("fake-tool/ping"); err != nil {
		t.Fatal(err)
	}
	drain(a, press(a, tea.KeyPressMsg{Code: tea.KeyEnter}))
	if last := calls(t, state); !strings.Contains(strings.Join(last, "\n"), "tool ping --json {} --yes") {
		t.Fatalf("ping not run: %q", last)
	}
}

func TestConfirmShowsTheTemplateThenTheListRefreshes(t *testing.T) {
	a, _ := tui(t)
	drain(a, press(a, tea.KeyPressMsg{Code: 'x', Text: "x"}))
	top := a.r.Instances[a.r.TopModal()]
	if top == nil || top.Noun != "confirm" || top.Payload.Prompt != "Remove alpha?" {
		t.Fatalf("modal %+v", top)
	}
	if view := frame(a); !strings.Contains(view, "Remove alpha?") {
		t.Fatalf("frame lacks the prompt:\n%s", view)
	}
	drain(a, press(a, tea.KeyPressMsg{Code: 'y', Text: "y"}))
	sel, _ := a.r.List("ext/fake/item").Selected()
	if a.r.TopModal() != "" || sel.ID != "ext/fake/item/b2" || a.r.Instances["ext/fake/item/a1"] != nil {
		t.Fatalf("after y: modal %q selected %+v", a.r.TopModal(), sel)
	}
}

func TestBindingsValidateAndCollisionsAreWarned(t *testing.T) {
	a, _ := tui(t)
	if _, err := a.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
	want := "fake: key j on item dropped: taken by list (list.move)"
	if len(a.m.Notices) != 1 || a.m.Notices[0] != want || a.m.Status != "" {
		t.Fatalf("notices %q, status %q", a.m.Notices, a.m.Status)
	}
	if view := frame(a); !strings.Contains(view, "Notice: fake: key j on item dropped") {
		t.Fatalf("frame lacks the notice:\n%s", view)
	}
	// j still moves the list, and the notice outlives the key.
	press(a, tea.KeyPressMsg{Code: 'j', Text: "j"})
	if a.r.Focus != "ext/fake/item/b2" {
		t.Fatalf("j: focus %q", a.r.Focus)
	}
	if view := frame(a); !strings.Contains(view, "Notice: fake: key j on item dropped") {
		t.Fatalf("the notice went with the key:\n%s", view)
	}
	for _, row := range a.r.Noun("fake-item").Bindings["normal"] {
		if row.Keys == "j" {
			t.Fatalf("j row loaded: %+v", row)
		}
	}
}

func TestSmokeRendersTheMountedPage(t *testing.T) {
	_, data := registered(t)
	out, errOut, err := nabe(t, "-data", data, "--smoke")
	if err != nil || !strings.Contains(out, "alpha") || !strings.Contains(out, "item (2)") {
		t.Fatalf("smoke: %v\n%s\n%s", err, out, errOut)
	}
	empty := t.TempDir()
	if out, _, err := nabe(t, "-data", empty, "--smoke"); err != nil || !strings.Contains(out, "no extensions registered") {
		t.Fatalf("empty smoke: %v\n%s", err, out)
	}
}

func TestMountedNounRunsFromTheCLI(t *testing.T) {
	state, data := registered(t)
	out, _, err := nabe(t, "-data", data, "fake-tool", "ping")
	if err != nil || !strings.Contains(out, `"pong": true`) {
		t.Fatalf("fake-tool ping: %v %q", err, out)
	}
	if last := calls(t, state); last[len(last)-1] != "tool ping --json {} --yes" {
		t.Fatalf("calls %q", last)
	}
}

func TestABrokenRegistryFileIsAWarning(t *testing.T) {
	_, data := registered(t)
	if err := os.WriteFile(filepath.Join(data, "catalogs", "bad.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.exts) != 1 || !strings.Contains(strings.Join(a.m.Notices, "\n"), "bad.json") {
		t.Fatalf("exts %d, notices %q", len(a.exts), a.m.Notices)
	}
}

func TestListVerbRowsAnswerToTheDeclaredNoun(t *testing.T) {
	a, state := tui(t)
	l := a.r.List("ext/fake/projects")
	if l == nil || l.Len() != 1 {
		t.Fatalf("projects list %+v", l)
	}
	row := l.Items()[0]
	if row.ID != "ext/fake/projects/p1" || row.Noun != "fake-project" || row.Data != "p1" {
		t.Fatalf("row %+v", row)
	}
	if a.r.List("ext/fake/project") != nil {
		t.Fatal("project, whose rows projects lists, got a list of its own")
	}
	if err := a.r.SetFocus(row.ID); err != nil {
		t.Fatal(err)
	}
	before := len(calls(t, state))
	drain(a, press(a, tea.KeyPressMsg{Code: 'o', Text: "o"}))
	if got := calls(t, state)[before:]; len(got) < 1 || got[0] != "project open --target p1 --json {} --yes" {
		t.Fatalf("calls after o: %q", got)
	}
}

func TestGroupsSelectTheRightPanelWithoutExecuting(t *testing.T) {
	a, state := tui(t)
	if err := a.exts[0].groups.Focus(); err != nil {
		t.Fatal(err)
	}
	before := len(calls(t, state))
	check := func(want, absent string) {
		t.Helper()
		view := frame(a)
		if !strings.Contains(view, want) || strings.Contains(view, absent) {
			t.Fatalf("want %q without %q:\n%s", want, absent, view)
		}
	}
	key := func(k rune) {
		t.Helper()
		drain(a, press(a, tea.KeyPressMsg{Code: k, Text: string(k)}))
		frame(a)
	}
	check("alpha", "Answer pong")
	key('j')
	check("ping  Answer pong", "alpha")
	drain(a, press(a, tea.KeyPressMsg{Code: tea.KeyEnter}))
	frame(a)
	if !a.r.List("ext/fake/tool").Focused() {
		t.Fatalf("enter did not enter the tool panel: %s", a.r.Focus)
	}
	key('h')
	key('j')
	check("pone", "Answer pong")
	key('l')
	if a.r.Focus != "ext/fake/projects/p1" {
		t.Fatalf("l did not focus the project row: %s", a.r.Focus)
	}
	key('}')
	if !a.exts[0].groups.Focused() {
		t.Fatalf("tab order reached a hidden list: %s", a.r.Focus)
	}
	key('k')
	check("Answer pong", "pone")
	key('l')
	key('j')
	key('h')
	key('l')
	if a.r.Focus != "fake-tool/ping" {
		t.Fatalf("the content selection was lost: %s", a.r.Focus)
	}
	if got := execsAfter(t, state, before); len(got) != 0 {
		t.Fatalf("group navigation executed the extension: %q", got)
	}
}

// execsAfter is the fake's invocations since before.
func execsAfter(t *testing.T, state string, before int) []string {
	t.Helper()
	return calls(t, state)[before:]
}

// The exec budget: startup loads each extension once (one state exec, or
// one per list without a state source), and keys, focus moves and page
// switches never exec. A verb execs itself, then the state (or only its
// own list); an explicit refresh asks for the state uncached; the timer
// reloads once per tick. Stale rows show while a load runs, with
// "refreshing" on the page.
func TestExecBudget(t *testing.T) {
	for _, c := range []struct {
		mode    string
		startup []string
		verb    []string
	}{
		{"state", []string{"item state --json {}"}, []string{"item touch --target a1 --json {} --yes", "item state --json {}"}},
		{"", []string{"item list --json {}", "projects list --json {}"}, []string{"item touch --target a1 --json {} --yes", "item list --json {}"}},
	} {
		t.Run("mode="+c.mode, func(t *testing.T) {
			bin, state, data := fake(t)
			t.Setenv("FAKE_MODE", c.mode)
			if _, errOut, err := nabe(t, "-data", data, "extension", "register", bin); err != nil {
				t.Fatalf("register: %v\n%s", err, errOut)
			}
			mark := len(calls(t, state))
			a, err := New(Options{Data: data})
			if err != nil {
				t.Fatal(err)
			}
			if got := execsAfter(t, state, mark); len(got) != 0 {
				t.Fatalf("New execs %q", got)
			}
			a.LoadRows()
			if view := frame(a); !strings.Contains(view, "loading…") || strings.Contains(view, "alpha") {
				t.Fatalf("the first page before any load lands: want empty lists and loading:\n%s", view)
			}
			if err := cli.Settle(a.r, nil); err != nil {
				t.Fatal(err)
			}
			got := execsAfter(t, state, mark)
			slices.Sort(got)
			if !slices.Equal(got, c.startup) {
				t.Fatalf("startup execs %q, want %q", got, c.startup)
			}
			if view := frame(a); strings.Contains(view, "loading…") || strings.Contains(view, "refreshing…") || !strings.Contains(view, "alpha") || !strings.Contains(view, "beta") || a.r.List("ext/fake/item").Len() != 2 || a.r.List("ext/fake/projects").Len() != 1 {
				t.Fatalf("after the load:\n%s", view)
			}

			mark = len(calls(t, state))
			ctrlJ := tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}
			for _, k := range []tea.KeyPressMsg{ctrlJ, ctrlJ, {Code: 'j', Text: "j"}, {Code: 'k', Text: "k"}, {Code: '}', Text: "}"}, {Code: '}', Text: "}"}, ctrlJ, ctrlJ} {
				drain(a, press(a, k))
				frame(a)
			}
			if got := execsAfter(t, state, mark); len(got) != 0 {
				t.Fatalf("keys and page switches exec %q", got)
			}
			if a.r.Page != "ext/fake" {
				t.Fatalf("page %s", a.r.Page)
			}

			if err := a.r.List("ext/fake/item").Focus(); err != nil {
				t.Fatal(err)
			}
			mark = len(calls(t, state))
			drain(a, press(a, tea.KeyPressMsg{Code: 't', Text: "t"}))
			if got := execsAfter(t, state, mark); !slices.Equal(got, c.verb) {
				t.Fatalf("verb execs %q, want %q", got, c.verb)
			}

			mark = len(calls(t, state))
			if _, err := a.r.Execute(ochazuke.Request{Noun: "extension", Verb: "refresh", Args: ochazuke.Arguments{"name": "fake"}}); err != nil {
				t.Fatal(err)
			}
			if err := cli.Settle(a.r, nil); err != nil {
				t.Fatal(err)
			}
			want := []string{"catalog", "item list --json {}", "projects list --json {}"}
			if c.mode == "state" {
				want = []string{"catalog", `item state --json {"fresh":true}`}
			}
			if got := execsAfter(t, state, mark); !slices.Equal(got, want) {
				t.Fatalf("extension refresh execs %q, want %q", got, want)
			}

			m := a.exts[0]
			if m.every != defaultRefreshEvery {
				t.Fatalf("every %s", m.every)
			}
			mark = len(calls(t, state))
			a.StartRefresh() // armed, waiting: nothing runs yet
			armed := slices.ContainsFunc(a.r.Jobs(), func(j ochazuke.JobInfo) bool {
				return j.State == ochazuke.JobRunning && j.Verb == timerVerb && j.Owner == m.page
			})
			if got := execsAfter(t, state, mark); len(got) != 0 || !armed {
				t.Fatalf("arming execs %q (armed %v)", got, armed)
			}
			a.r.PendingCommands() // the wait itself never runs here
			m.every = 0           // the tick below does not arm again
			a.tick(m)             // the timer fired
			drain(a, a.r.PendingCommands())
			if got := execsAfter(t, state, mark); len(got) != len(c.startup) {
				t.Fatalf("a tick execs %q, want the startup load %q", got, c.startup)
			}
		})
	}
}

// A registry file's refreshEvery sets the interval and survives refresh;
// one that is not a duration is a warning and the default.
func TestRefreshEveryOverride(t *testing.T) {
	_, data := registered(t)
	reg := RegistryIn(data)
	for _, c := range []struct {
		every string
		want  time.Duration
	}{{"5m", 5 * time.Minute}, {"0s", 0}, {"soon", defaultRefreshEvery}} {
		e, err := reg.Read("fake")
		if err != nil {
			t.Fatal(err)
		}
		e.RefreshEvery = c.every
		if err := reg.write("fake", e); err != nil {
			t.Fatal(err)
		}
		a, err := New(Options{Data: data})
		if err != nil {
			t.Fatal(err)
		}
		if got := a.exts[0].every; got != c.want {
			t.Fatalf("refreshEvery %q: every %s, want %s", c.every, got, c.want)
		}
		// The warning is a notice; the footer shows only the newest one
		// (ochazuke v0.1.1), so read the notices, not the frame.
		if c.every == "soon" && !strings.Contains(strings.Join(a.m.Notices, "\n"), "refreshEvery") {
			t.Fatalf("no warning: %q", a.m.Notices)
		}
	}
	if _, errOut, err := nabe(t, "-data", data, "extension", "refresh", "fake"); err != nil {
		t.Fatalf("refresh: %v\n%s", err, errOut)
	}
	if e, _ := reg.Read("fake"); e.RefreshEvery != "soon" {
		t.Fatalf("refresh dropped refreshEvery: %q", e.RefreshEvery)
	}
}

// Every extension loads in the background from startup: the first page
// draws before any load lands, every page is filled once the jobs settle,
// and pages follow registration (registeredAt), then order.
func TestPagesLoadInBackgroundInRegistrationOrder(t *testing.T) {
	bin, _, data := fake(t)
	t.Setenv("FAKE_MODE", "state")
	reg := RegistryIn(data)
	at := time.Date(2026, 10, 24, 18, 0, 0, 0, time.UTC)
	for i, name := range []string{"zeta", "alpha"} {
		if _, errOut, err := nabe(t, "-data", data, "extension", "register", bin, name); err != nil {
			t.Fatalf("register %s: %v\n%s", name, err, errOut)
		}
		e, _ := reg.Read(name)
		e.RegisteredAt = at.Add(time.Duration(i) * time.Minute) // zeta first
		if err := reg.write(name, e); err != nil {
			t.Fatal(err)
		}
	}
	pages := func(a *App) string {
		var ids []string
		for _, p := range a.r.PageList() {
			ids = append(ids, p.ID)
		}
		return strings.Join(ids, " ")
	}
	a, err := New(Options{Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if got := pages(a); got != "ext/zeta ext/alpha extensions" || a.r.Page != "ext/zeta" {
		t.Fatalf("pages %q open %s", got, a.r.Page)
	}
	a.LoadRows()
	if view := frame(a); !strings.Contains(view, "loading…") || a.r.List("ext/zeta/item").Len() != 0 {
		t.Fatalf("first page before the loads land:\n%s", view)
	}
	if err := cli.Settle(a.r, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ext/zeta/item", "ext/alpha/item", "ext/zeta/projects", "ext/alpha/projects"} {
		if n := a.r.List(id).Len(); n == 0 {
			t.Fatalf("%s empty after the loads settled", id)
		}
	}
	if _, errOut, err := nabe(t, "-data", data, "extension", "order", "alpha", "-1"); err != nil {
		t.Fatalf("order: %v\n%s", err, errOut)
	}
	if _, errOut, err := nabe(t, "-data", data, "extension", "refresh"); err != nil {
		t.Fatalf("refresh: %v\n%s", err, errOut)
	}
	if a, err = New(Options{Data: data}); err != nil {
		t.Fatal(err)
	}
	if got := pages(a); got != "ext/alpha ext/zeta extensions" {
		t.Fatalf("after order alpha -1 and a refresh: pages %q", got)
	}
	if e, _ := reg.Read("zeta"); !e.RegisteredAt.Equal(at) {
		t.Fatalf("refresh moved registeredAt to %s", e.RegisteredAt)
	}
}

// The first load shows each batch as it lands; a refresh keeps the old
// rows whole until its stream ends, then swaps every list (a noun with no
// lines is emptied).
func TestStateBatchesFillFirstThenSwapOnRefresh(t *testing.T) {
	_, data := registered(t)
	a, err := New(Options{Data: data})
	if err != nil {
		t.Fatal(err)
	}
	m := a.exts[0]
	item, projects := a.r.List("ext/fake/item"), a.r.List("ext/fake/projects")
	st := &stateLoad{items: map[string][]ochazuke.ListItem{}, seen: map[string]bool{}}
	a.stateBatch(m, st, stateBatch{rows: []cli.Row{{Noun: "item", ID: "a1", Label: "alpha"}}})
	if item.Len() != 1 || m.loaded {
		t.Fatalf("first batch: %d rows, loaded %v", item.Len(), m.loaded)
	}
	a.stateBatch(m, st, stateBatch{rows: []cli.Row{{Noun: "item", ID: "b2"}, {Noun: "projects", ID: "p1", Label: "pone"}}, done: true})
	if item.Len() != 2 || projects.Len() != 1 || !m.loaded {
		t.Fatalf("end: item %d projects %d loaded %v", item.Len(), projects.Len(), m.loaded)
	}
	st = &stateLoad{items: map[string][]ochazuke.ListItem{}, seen: map[string]bool{}}
	a.stateBatch(m, st, stateBatch{rows: []cli.Row{{Noun: "item", ID: "c3"}}})
	if item.Len() != 2 {
		t.Fatalf("a refresh's partial batch replaced the rows: %d", item.Len())
	}
	a.stateBatch(m, st, stateBatch{done: true})
	if item.Len() != 1 || projects.Len() != 0 {
		t.Fatalf("refresh end: item %d projects %d", item.Len(), projects.Len())
	}
}

func TestUnknownWordIsNotFound(t *testing.T) {
	_, data := registered(t)
	out, errOut, err := nabe(t, "-data", data, "fake-tools", "ping")
	if oden.ExitCode(err) != 1 || out != "" || !strings.HasPrefix(errOut, prog+": command \"fake-tools\" not found\ndid you mean: fake-tool\n") {
		t.Fatalf("fake-tools: %v %q\n%s", err, out, errOut)
	}
}

// The TUI opens on a named page: --page NAME, or NAME alone when no
// command claims the word exactly (a mounted noun's prefix such as fake is
// a page word, not an ambiguous command).
func TestOpensOnANamedPage(t *testing.T) {
	_, data := registered(t)
	for _, args := range [][]string{{"--page", "extensions"}, {"extensions"}} {
		out, errOut, err := nabe(t, append([]string{"-data", data, "--smoke"}, args...)...)
		if err != nil || !strings.Contains(out, "registry ") || strings.Contains(out, "Groups") {
			t.Fatalf("%q: %v\n%s\n%s", args, err, out, errOut)
		}
	}
	for _, args := range [][]string{{"--page", "fake"}, {"--page", "ext/fake"}, {"fake"}} {
		out, errOut, err := nabe(t, append([]string{"-data", data, "--smoke"}, args...)...)
		if err != nil || !strings.Contains(out, "Groups") || !strings.Contains(out, "alpha") {
			t.Fatalf("%q: %v\n%s\n%s", args, err, out, errOut)
		}
	}
	if _, _, err := nabe(t, "-data", data, "--smoke", "--page", "nope"); err == nil || !strings.Contains(err.Error(), `no page "nope"; pages: fake, extensions`) {
		t.Fatalf("--page nope: %v", err)
	}
	if _, _, err := nabe(t, "-data", data, "--page", "fake", "fake-tool", "ping"); err == nil || !strings.Contains(err.Error(), "takes no command") {
		t.Fatalf("--page with a command: %v", err)
	}

	a, err := New(Options{Data: data, Sync: true})
	if err != nil {
		t.Fatal(err)
	}
	app := a.commands([]string{"fake"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, nil)
	for word, want := range map[string]bool{"fake": true, "extensions": true, "fake-tool": false, "extension": false, "help": false, "fak": false} {
		if got := a.pageWord(word, app); got != want {
			t.Errorf("pageWord(%q) = %v, want %v", word, got, want)
		}
	}
	if err := a.OpenOn("extensions"); err != nil || a.r.Page != extensionsPage || !a.extList.Focused() {
		t.Fatalf("OpenOn extensions: %v page %s focus %s", err, a.r.Page, a.r.Focus)
	}
}
