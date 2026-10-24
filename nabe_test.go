package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/cli"
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
	if want := "fake: 2 nouns, 5 verbs, 0 pages (framework nouns skipped: 1)"; s.Summary != want {
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
	if e.Bin != bin || e.RegisteredAt.IsZero() || e.Catalog.Name != "fake" || len(e.Catalog.Nouns) != 3 {
		t.Fatalf("entry = %+v", e)
	}
}

func TestRegisterByNameOnPathWithPrefixesAndName(t *testing.T) {
	bin, _, data := fake(t)
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, errOut, err := nabe(t, "-data", data, "ext", "reg", "fake-ext", "--name", "other")
	if err != nil || !strings.Contains(out, `"summary": "other: 2 nouns`) {
		t.Fatalf("ext reg: %v %q\n%s", err, out, errOut)
	}
	if e, err := (RegistryIn(data)).Read("other"); err != nil || e.Bin != bin {
		t.Fatalf("entry %+v: %v", e, err)
	}
}

func TestListRefreshAndUnregister(t *testing.T) {
	state, data := registered(t)
	out, _, err := nabe(t, "-data", data, "extension", "list")
	var rows []Listing
	if err != nil || json.Unmarshal([]byte(out), &rows) != nil || len(rows) != 1 || rows[0].Name != "fake" || rows[0].Nouns != 2 || rows[0].Verbs != 5 {
		t.Fatalf("list: %v %q", err, out)
	}
	if out, _, err := nabe(t, "-data", data, "extension", "refresh"); err != nil || !strings.Contains(out, `"summary":"fake: 2 nouns`) {
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
	if p := a.r.Operations["fake-item.touch"].Parameters["note"]; p.Type != "string" || p.Source != "" {
		t.Fatalf("touch params %+v", p)
	}
	view := frame(a)
	for _, want := range []string{"fake", "item (2)", "alpha", "beta", "tool (2)", "echo (palette: text)  Echo the text", "ping  Answer pong", "Extensions"} {
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
