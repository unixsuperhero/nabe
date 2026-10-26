package main

import (
	"testing"

	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/cli"
)

// Every ochazuke tool's catalog lists the framework's outline verbs as
// application verbs (they have application handlers); they are the
// framework's, so nabe mounts no <ext>-outline. An extension's own
// application verb on a framework noun still mounts.
func TestFrameworkVerbsDoNotMount(t *testing.T) {
	a, _ := tui(t)
	if n := a.r.Noun("fake-outline"); n != nil {
		t.Fatalf("fake-outline mounted: %+v", n)
	}
	if _, ok := a.r.Operations["fake-outline.expand"]; ok {
		t.Fatal("fake-outline.expand mounted")
	}
	for _, c := range []struct {
		noun string
		verb cli.CatalogVerb
		want bool
	}{
		{"outline", cli.CatalogVerb{Name: "expand", Kind: ochazuke.KindApplication}, false},
		{"chooser", cli.CatalogVerb{Name: "pick", Kind: ochazuke.KindApplication}, false},
		{"history", cli.CatalogVerb{Name: "list", Kind: ochazuke.KindInteraction}, false},
		{"history", cli.CatalogVerb{Name: "capture-hook", Kind: ochazuke.KindApplication}, true},
		{"item", cli.CatalogVerb{Name: "list", Kind: ochazuke.KindApplication}, true},
	} {
		if got := mountable(c.noun, c.verb); got != c.want {
			t.Errorf("mountable(%s.%s) = %v, want %v", c.noun, c.verb.Name, got, c.want)
		}
	}
}
