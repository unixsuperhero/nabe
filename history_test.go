package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/cli"
)

// A mounted verb records the history kind its catalog declares (tool ping
// is a fetch); one that declares none stays the framework default, a
// change.
func TestMountedVerbsKeepTheCatalogHistory(t *testing.T) {
	a, _ := tui(t)
	for key, want := range map[string]string{
		"fake-tool.ping":    ochazuke.HistoryFetch,
		"fake-tool.voices":  ochazuke.HistoryChange,
		"fake-item.touch":   ochazuke.HistoryChange,
		"extension.refresh": ochazuke.HistoryChange,
	} {
		if got := a.r.Operations[key].HistoryKindOf(); got != want {
			t.Errorf("%s records %q, want %q", key, got, want)
		}
	}
	for _, verb := range []string{"ping", "voices"} {
		if _, err := a.r.Execute(ochazuke.Request{Noun: "fake-tool", Verb: verb}); err != nil {
			t.Fatal(err)
		}
	}
	if err := cli.Settle(a.r, nil); err != nil {
		t.Fatal(err)
	}
	keys := func(kinds ...string) []string {
		var out []string
		for _, e := range a.r.HistoryOf(0, kinds...) {
			out = append(out, e.Request.Key())
		}
		return out
	}
	if got := keys(ochazuke.HistoryFetch); !slices.Contains(got, "fake-tool.ping") {
		t.Fatalf("fetches %v lack fake-tool.ping", got)
	}
	if got := keys(ochazuke.HistoryAction, ochazuke.HistoryChange); slices.Contains(got, "fake-tool.ping") || !slices.Contains(got, "fake-tool.voices") {
		t.Fatalf("the user's entries %v: want fake-tool.voices, not the fetch fake-tool.ping", got)
	}
}

// A catalog history kind the framework does not know is a notice and the
// default, never a kind the ring cannot file.
func TestUnknownCatalogHistoryIsANotice(t *testing.T) {
	a, _ := tui(t)
	if got := a.historyKind("fake", "tool", cli.CatalogVerb{Name: "ping", History: "poll"}); got != "" {
		t.Fatalf("kind %q, want the default", got)
	}
	if !slices.ContainsFunc(a.m.Notices, func(n string) bool { return strings.Contains(n, `history "poll"`) }) {
		t.Fatalf("notices %v", a.m.Notices)
	}
}
