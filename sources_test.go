package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/cli"
)

// In the TUI a parameter's pick-list source is a cache in its Enum: the
// rows of the list the source fills (item.list), else one quiet load of
// the source with the rows (tool.voices). The palette's choices come from
// it without an exec. The choices are suggestions: the parameter is Free,
// so a value outside them reaches the bin, which validates its own.
func TestSourcesFillPickListsInTheTUI(t *testing.T) {
	state, data := registered(t)
	a, err := New(Options{Data: data})
	if err != nil {
		t.Fatal(err)
	}
	note := func() ochazuke.Parameter { return a.r.Operations["fake-item.touch"].Parameters["note"] }
	voice := func() ochazuke.Parameter { return a.r.Operations["fake-tool.echo"].Parameters["voice"] }
	if p := note(); p.Source != "" || !p.Free || len(p.Enum) != 0 {
		t.Fatalf("note before the load: %+v", p)
	}
	a.LoadRows()
	if err := cli.Settle(a.r, nil); err != nil {
		t.Fatal(err)
	}
	if p := note(); p.Source != "" || !p.Free || !slices.Equal(p.Enum, []string{"a1", "b2"}) {
		t.Fatalf("note after the load: %+v", p)
	}
	if p := voice(); p.Source != "" || !p.Free || !slices.Equal(p.Enum, []string{"loud", "soft"}) {
		t.Fatalf("voice after the load: %+v", p)
	}
	mark := len(calls(t, state))
	choices, err := a.r.Choices(a.r.Context(), voice())
	if err != nil || len(choices) != 2 || choices[0].ID != "loud" {
		t.Fatalf("choices %+v %v", choices, err)
	}
	if err := a.r.List("ext/fake/item").Focus(); err != nil {
		t.Fatal(err)
	}
	if got := execsAfter(t, state, mark); len(got) != 0 {
		t.Fatalf("choices exec %q", got)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "fake-item", Verb: "touch", Args: ochazuke.Arguments{"note": "zz"}}); err != nil {
		t.Fatalf("a note outside the source's choices was refused: %v", err)
	}
	if err := cli.Settle(a.r, nil); err != nil {
		t.Fatal(err)
	}
	// The value reaches the bin; the verb reloads the rows (and so note's
	// choices), not voices.
	want := []string{`item touch --target a1 --json {"note":"zz"} --yes`, "item list --json {}"}
	if got := execsAfter(t, state, mark); !slices.Equal(got, want) {
		t.Fatalf("verb execs %q, want %q", got, want)
	}

	// The list's next rows are the next choices.
	if _, err := a.r.Execute(ochazuke.Request{Noun: "fake-item", Verb: "remove", Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	if err := cli.Settle(a.r, nil); err != nil {
		t.Fatal(err)
	}
	if p := note(); !slices.Equal(p.Enum, []string{"b2"}) {
		t.Fatalf("note after remove: %+v", p)
	}
}

// From the CLI a source names the mounted proxy, so help says where the
// choices come from; they are suggestions: a value outside them (a custom
// id the bin accepts, as mtm's --model does) goes to the bin unchecked,
// and the source never runs to check it.
func TestSourcesNameTheProxyFromTheCLI(t *testing.T) {
	state, data := registered(t)
	for _, voice := range []string{"loud", "shout"} {
		mark := len(calls(t, state))
		out, errOut, err := nabe(t, "-data", data, "fake-tool", "echo", "hi", "--voice", voice)
		want := []string{`tool echo --json {"text":"hi","voice":"` + voice + `"} --yes`}
		if got := execsAfter(t, state, mark); err != nil || !slices.Equal(got, want) {
			t.Fatalf("echo --voice %s: %v execs %q, want %q\n%s\n%s", voice, err, got, want, out, errOut)
		}
	}
	out, _, err := nabe(t, "-data", data, "fake-tool", "echo", "--help")
	if err != nil || !strings.Contains(out, "choices from: fake-tool.voices") {
		t.Fatalf("help: %v\n%s", err, out)
	}
}

// A source whose noun is not mounted is dropped with a notice.
func TestUnmountedSourceIsANotice(t *testing.T) {
	_, data := registered(t)
	reg := RegistryIn(data)
	e, err := reg.Read("fake")
	if err != nil {
		t.Fatal(err)
	}
	for i, n := range e.Catalog.Nouns {
		for j, v := range n.Verbs {
			if n.Name == "item" && v.Name == "touch" {
				e.Catalog.Nouns[i].Verbs[j].Params["tag"] = ochazuke.Parameter{Type: "string", Source: "field.submit"}
			}
		}
	}
	if err := reg.write("fake", e); err != nil {
		t.Fatal(err)
	}
	for _, sync := range []bool{false, true} {
		a, err := New(Options{Data: data, Sync: sync})
		if err != nil {
			t.Fatal(err)
		}
		if p := a.r.Operations["fake-item.touch"].Parameters["tag"]; p.Source != "" {
			t.Fatalf("sync %v: tag %+v", sync, p)
		}
		if !slices.Contains(a.m.Notices, "fake: item touch --tag: choices source field.submit is not mounted; no pick list") {
			t.Fatalf("sync %v: notices %q", sync, a.m.Notices)
		}
	}
}

// A timed refresh (PageView.Reload) fills the lists itself; the pick lists
// a list answers follow its rows once the reload lands.
func TestTimedReloadKeepsListChoices(t *testing.T) {
	a, state := tui(t)
	note := func() []string { return a.r.Operations["fake-item.touch"].Parameters["note"].Enum }
	if !slices.Equal(note(), []string{"a1", "b2"}) {
		t.Fatalf("note before: %q", note())
	}
	if err := os.WriteFile(filepath.Join(state, "items"), []byte("c3 gamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := a.exts[0]
	m.every = 0
	a.tick(m)
	drain(a, a.r.PendingCommands())
	frame(a)
	if a.r.List("ext/fake/item").Len() != 1 || !slices.Equal(note(), []string{"c3"}) {
		t.Fatalf("after the tick: %d rows, note %q", a.r.List("ext/fake/item").Len(), note())
	}
}
