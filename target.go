package main

import (
	"fmt"
	"strings"

	"github.com/unixsuperhero/ochazuke"
)

// From the CLI no rows are loaded, so a mounted verb's --target names no
// instance; cli.Shell asks reveal to make one. The target is the
// extension's own id (`nabe mtm-project open --target p1`), or a row's
// nabe id (`--target ext/mtm/projects/p1`, the id the TUI and the palette
// show), which carries its list and so its noun. The instance is a stand-in
// row: its Data is the extension's id, so the proxy passes
// `--target ID` to the bin, and its label is the id ({{label}} in a
// confirm). A target that names one of nabe's own instances (app, a page,
// a list) is that instance and is not revealed: use the row's nabe id.

// reveal makes an instance for target (see above). words are the command
// line; its first word is the noun, a unique prefix allowed as oden allows.
func (a *App) reveal(words []string, target string) error {
	for _, m := range a.exts {
		for _, l := range m.specs() {
			if id, ok := strings.CutPrefix(target, l.ID+"/"); ok && id != "" && m.source(l) != "" {
				a.standIn(target, m.page, m.rowNoun(l), id)
				return nil
			}
		}
	}
	noun := a.nounOf(words)
	list, ok := a.homes[noun]
	if !ok {
		return fmt.Errorf("no instance %s, and %s is not a mounted noun", target, noun)
	}
	a.standIn(target, a.r.Instances[list].Parent, noun, target)
	return nil
}

// standIn registers a row of noun under page whose extension id is id.
func (a *App) standIn(target, page, noun, id string) {
	inst := a.r.Instance(target, ochazuke.Parent(page), ochazuke.Kind(noun))
	inst.Data, inst.Label = id, id
}

// nounOf is the noun the command line names: its first word, or the one
// noun it is a prefix of; "" when neither.
func (a *App) nounOf(words []string) string {
	if len(words) == 0 {
		return ""
	}
	word := words[0]
	if a.r.Noun(word) != nil {
		return word
	}
	match := ""
	for name := range a.r.Nouns {
		if strings.HasPrefix(name, word) {
			if match != "" {
				return ""
			}
			match = name
		}
	}
	return match
}
