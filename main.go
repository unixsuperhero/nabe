// Command nabe is the host app: an ochazuke app with no nouns of its own
// beyond extension, which mounts extension catalogs as pages. Any
// executable answering `BIN catalog` with ochazuke/cli's catalog JSON
// (schema 1) becomes a page with lists and verbs. See AGENTS.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	ocli "github.com/unixsuperhero/ochazuke/cli"
	"github.com/unixsuperhero/oden"
)

// prog is the name nabe was invoked by: nabe, or o-nabe / o-tui when it is
// installed under the o root, so help and errors say what the user typed.
var prog = filepath.Base(os.Args[0])

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		os.Exit(oden.Report(os.Stderr, prog, err))
	}
}

func usage() string {
	return "usage: " + prog + " [-data DIR] [--smoke] [--page NAME | NAME]\n       " + prog + " [-data DIR] NOUN VERB [ARG...] [--target ID] [--json '{...}'] [--ARG VALUE] [--yes]\n" + ocli.UsageHint(prog)
}

// run is the TUI with no words, one frame with --smoke, else one command
// resolved by oden: a builtin (help, commands, which), catalog, a noun
// (extension, or a mounted <ext>-<noun>) or a nabe-<word> on PATH. The TUI
// and the frame open on --page NAME, or on NAME given as the only word
// when it names a page and no command claims it exactly (pageWord).
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet(prog, flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data", DefaultDataDir(), "nabe data directory; the registry is DIR/catalogs")
	smoke := flags.Bool("smoke", false, "Mount the registry, load the rows, render one 80x24 frame to stdout and exit")
	page := flags.String("page", "", "Open on this page: an extension's name, or extensions")
	words := []string{"help"}
	if err := flags.Parse(args); err == nil {
		words = flags.Args()
	} else if !errors.Is(err, flag.ErrHelp) {
		return err
	}
	a, err := New(Options{Data: *data, Sync: len(words) > 0 && !*smoke})
	if err != nil {
		return err
	}
	if *page == "" && len(words) == 1 && a.pageWord(words[0], a.commands(words, stdin, stdout, stderr, nil)) {
		*page, words, a.sync = words[0], nil, false
	}
	if *page != "" {
		if len(words) > 0 {
			return fmt.Errorf("--page opens the TUI; it takes no command (%s)", strings.Join(words, " "))
		}
		if err := a.OpenOn(*page); err != nil {
			return err
		}
	}
	if *smoke {
		a.LoadRows()
		if err := ocli.Settle(a.r, nil); err != nil {
			a.m.Status = err.Error()
		}
		a.m.Width, a.m.Height = 80, 24
		fmt.Fprintln(stdout, ansi.Strip(a.m.View().Content))
		return nil
	}
	app := a.commands(words, stdin, stdout, stderr, func() error {
		a.LoadRows()
		a.StartRefresh()
		_, err := tea.NewProgram(a.m).Run()
		return err
	})
	return app.Run(context.Background())
}

// commands is the oden App for one invocation: builtins, every noun of the
// runtime and catalog (ochazuke/cli.Mount), Default the TUI with no words,
// else oden's not found report for the unknown word.
// The shell fills a verb's parameters from bare words after NOUN VERB.
func (a *App) commands(words []string, stdin io.Reader, stdout, stderr io.Writer, tui func() error, opts ...oden.Option) *oden.App {
	app := oden.New(prog, words, append([]oden.Option{oden.WithBuiltins(), oden.WithStdio(stdin, stdout, stderr)}, opts...)...)
	sh := ocli.New(a.r, prog, stdin, stdout, stderr)
	sh.Usage = usage()
	sh.Owners = a.owners
	app.Default = func(_ context.Context, words []string) error {
		if len(words) == 0 {
			return tui()
		}
		return oden.ErrNotFound
	}
	ocli.Mount(app, sh)
	return app
}

// pageWord reports whether a lone word opens the TUI on a page: it names
// one (OpenOn) and app resolves no command by exactly that word, so a
// command always wins and a prefix of a mounted noun never reaches oden as
// an ambiguous word.
func (a *App) pageWord(word string, app *oden.App) bool {
	if _, ok := a.pageNamed(word); !ok {
		return false
	}
	return app.Catalog().Lookup(word) == nil
}
