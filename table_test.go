package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/cli"
)

// withColumns registers the fake extension with columns declared on item.
func withColumns(t *testing.T) string {
	t.Helper()
	_, data := registered(t)
	reg := RegistryIn(data)
	e, err := reg.Read("fake")
	if err != nil {
		t.Fatal(err)
	}
	for i := range e.Catalog.Nouns {
		if e.Catalog.Nouns[i].Name == "item" {
			e.Catalog.Nouns[i].Columns = []ochazuke.Column{{Title: "item"}, {Title: "state", Priority: 1}, {Title: "where", Min: 8, Priority: 2}}
		}
	}
	if err := reg.write("fake", e); err != nil {
		t.Fatal(err)
	}
	return data
}

// A mounted list whose row noun declares columns is a table: the header,
// the state rows' cells; a noun without columns keeps its labels.
func TestMountedListDrawsCatalogColumns(t *testing.T) {
	a, err := New(Options{Data: withColumns(t)})
	if err != nil {
		t.Fatal(err)
	}
	m := a.exts[0]
	item, projects := a.r.List("ext/fake/item"), a.r.List("ext/fake/projects")
	if len(item.Columns()) != 3 || projects.Columns() != nil {
		t.Fatalf("columns: item %v projects %v", item.Columns(), projects.Columns())
	}
	st := &stateLoad{items: map[string][]ochazuke.ListItem{}, seen: map[string]bool{}}
	a.stateBatch(m, st, stateBatch{rows: []cli.Row{
		{Noun: "item", ID: "a1", Label: "alpha", Cells: []string{"alpha", "idle", "~/proj/alpha"}},
		{Noun: "item", ID: "b2", Label: "beta"},
	}, done: true})
	if err := item.Focus(); err != nil {
		t.Fatal(err)
	}
	for _, w := range []int{120, 80} {
		a.m.Width, a.m.Height = w, 20
		frame := ansi.Strip(a.m.View().Content)
		for _, want := range []string{"item", "state", "where", "alpha", "idle", "~/proj/alpha", "beta"} {
			if !strings.Contains(frame, want) {
				t.Fatalf("width %d: frame lacks %q:\n%s", w, want, frame)
			}
		}
	}
	// Rows from a list source bring cells too.
	l := m.lists[0]
	for _, ml := range m.lists {
		if ml.noun == "item" {
			l = ml
		}
	}
	if err := a.setRows(l, []any{map[string]any{"id": "c3", "name": "gamma", "cells": []any{"gamma", "busy", nil}}}); err != nil {
		t.Fatal(err)
	}
	if got := item.Items(); len(got) != 1 || strings.Join(got[0].Cells, "|") != "gamma|busy|" {
		t.Fatalf("list rows %+v", got)
	}
}

// The extensions page is a table of the registry: name, counts, refresh
// time, version and bin; narrow, the bin and version drop first.
func TestExtensionsPageIsATable(t *testing.T) {
	_, data := registered(t)
	a, err := New(Options{Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.r.OpenPage(extensionsPage); err != nil {
		t.Fatal(err)
	}
	if err := a.extList.Focus(); err != nil {
		t.Fatal(err)
	}
	row := a.extList.Items()[0]
	if row.Label == "" || len(row.Cells) != len(extensionColumns) || row.Cells[0] != "fake" || row.Cells[1] != "4" || row.Cells[2] != "7" || row.Cells[3] != "0" || row.Cells[4] == "" {
		t.Fatalf("row %+v", row)
	}
	a.m.Width, a.m.Height = 120, 12
	frame := ansi.Strip(a.m.View().Content)
	for _, want := range []string{"name", "nouns", "verbs", "pages", "refreshed", "version", "bin", "fake", "0.1.0", "fake-ext"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("120: frame lacks %q:\n%s", want, frame)
		}
	}
	a.m.Width = 40
	frame = ansi.Strip(a.m.View().Content)
	if !strings.Contains(frame, "nouns") || strings.Contains(frame, "fake-ext") {
		t.Fatalf("40: want counts, not the bin:\n%s", frame)
	}
}
