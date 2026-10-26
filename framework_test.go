package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/cli"
)

// Every ochazuke tool's catalog lists the framework's outline verbs as
// application verbs (they have application handlers); they are the
// framework's, so nabe mounts no <ext>-outline. An extension's own
// application verb on a framework noun still mounts. The fake's catalog
// has no builtin (a tool built before ochazuke v0.8.0), so this is the
// fallback: the keys a fresh runtime registers.
func TestFrameworkVerbsDoNotMount(t *testing.T) {
	a, _ := tui(t)
	if n := a.r.Noun("fake-outline"); n != nil {
		t.Fatalf("fake-outline mounted: %+v", n)
	}
	if _, ok := a.r.Operations["fake-outline.expand"]; ok {
		t.Fatal("fake-outline.expand mounted")
	}
	doc := cli.CatalogDoc{Nouns: []cli.CatalogNoun{
		{Name: "outline", Verbs: []cli.CatalogVerb{{Name: "expand", Kind: ochazuke.KindApplication}}},
		{Name: "chooser", Verbs: []cli.CatalogVerb{{Name: "pick", Kind: ochazuke.KindApplication}}},
		{Name: "history", Verbs: []cli.CatalogVerb{{Name: "list", Kind: ochazuke.KindInteraction}, {Name: "capture-hook", Kind: ochazuke.KindApplication}}},
		{Name: "item", Verbs: []cli.CatalogVerb{{Name: "list", Kind: ochazuke.KindApplication}}},
	}}
	if got, want := planKeys(doc), []string{"history.capture-hook", "item.list"}; !slices.Equal(got, want) {
		t.Fatalf("plan without builtin = %q, want %q", got, want)
	}
}

// A catalog that marks the framework's verbs builtin (ochazuke v0.8.0)
// mounts CatalogDoc.AppNouns: builtin decides, not this build's registry,
// so a framework verb nabe's ochazuke does not know stays out and a tool's
// verb under a name the framework also uses mounts.
func TestBuiltinVerbsDoNotMount(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "fake-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc cli.CatalogDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for i, n := range doc.Nouns {
		for j := range n.Verbs {
			doc.Nouns[i].Verbs[j].Builtin = n.Name == "app" || n.Name == "outline"
		}
	}
	doc.Nouns = append(doc.Nouns,
		cli.CatalogNoun{Name: "history", Verbs: []cli.CatalogVerb{{Name: "list", Kind: ochazuke.KindInteraction, Builtin: true}, {Name: "capture-hook", Kind: ochazuke.KindApplication}}},
		cli.CatalogNoun{Name: "chooser", Verbs: []cli.CatalogVerb{{Name: "future", Kind: ochazuke.KindApplication, Builtin: true}}},
	)
	want := []string{"history.capture-hook", "item.list", "item.remove", "item.touch", "project.open", "projects.list", "tool.echo", "tool.ping", "tool.voices"}
	if got := planKeys(doc); !slices.Equal(got, want) {
		t.Fatalf("plan = %q, want %q", got, want)
	}
	// The outline's verbs are builtin here; marked as the tool's, they mount.
	for j := range doc.Nouns[6].Verbs {
		doc.Nouns[6].Verbs[j].Builtin = false
	}
	if got := planKeys(doc); !slices.Contains(got, "outline.expand") {
		t.Fatalf("an application verb the catalog does not mark builtin did not mount: %q", got)
	}
	if p := planOf(doc); !slices.Contains(p.Skipped, "app") || !slices.Contains(p.Skipped, "field") || !slices.Contains(p.Skipped, "chooser") {
		t.Fatalf("skipped %q", p.Skipped)
	}
}

func planKeys(doc cli.CatalogDoc) []string {
	var keys []string
	for key := range mountableVerbs(doc) {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
