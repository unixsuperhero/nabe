package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/cli"
)

// In the TUI a parameter's pick-list source is a cache in its Enum: the
// rows of the list the source fills (item.list), else one quiet load of
// the source with the rows (tool.voices). Until it lands the parameter is
// Free; afterwards the palette's choices come from it without an exec and
// a value outside them is refused before the bin runs.
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
	if p := note(); p.Source != "" || p.Free || !slices.Equal(p.Enum, []string{"a1", "b2"}) {
		t.Fatalf("note after the load: %+v", p)
	}
	if p := voice(); p.Source != "" || p.Free || !slices.Equal(p.Enum, []string{"loud", "soft"}) {
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
	if _, err := a.r.Execute(ochazuke.Request{Noun: "fake-item", Verb: "touch", Args: ochazuke.Arguments{"note": "zz"}}); err == nil {
		t.Fatal("a note outside the source's choices ran")
	}
	if got := execsAfter(t, state, mark); len(got) != 0 {
		t.Fatalf("choices and a refused value exec %q", got)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "fake-item", Verb: "touch", Args: ochazuke.Arguments{"note": "b2"}}); err != nil {
		t.Fatal(err)
	}
	if err := cli.Settle(a.r, nil); err != nil {
		t.Fatal(err)
	}
	// The verb reloads the rows (and so note's choices), not voices.
	want := []string{`item touch --target a1 --json {"note":"b2"} --yes`, "item list --json {}"}
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

// From the CLI a source names the mounted proxy: it runs inline to check
// the value, and help says where the choices come from.
func TestSourcesNameTheProxyFromTheCLI(t *testing.T) {
	state, data := registered(t)
	out, errOut, err := nabe(t, "-data", data, "fake-tool", "echo", "hi", "--voice", "loud")
	if err != nil {
		t.Fatalf("echo --voice loud: %v\n%s\n%s", err, out, errOut)
	}
	if got := calls(t, state); !slices.Contains(got, "tool voices --json {} --yes") {
		t.Fatalf("the source did not run: %q", got)
	}
	if _, _, err := nabe(t, "-data", data, "fake-tool", "echo", "hi", "--voice", "shout"); err == nil || !strings.Contains(err.Error(), "loud") {
		t.Fatalf("echo --voice shout: %v", err)
	}
	out, _, err = nabe(t, "-data", data, "fake-tool", "echo", "--help")
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
