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
	return "usage: " + prog + " [-data DIR] [--smoke] NOUN VERB [ARG...] [--target ID] [--json '{...}'] [--ARG VALUE] [--yes]\n" + ocli.UsageHint(prog)
}

// run is the TUI with no words, one frame with --smoke, else one command
// resolved by oden: a builtin (help, commands, which), catalog, a noun
// (extension, or a mounted <ext>-<noun>) or a nabe-<word> on PATH.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet(prog, flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data", DefaultDataDir(), "nabe data directory; the registry is DIR/catalogs")
	smoke := flags.Bool("smoke", false, "Mount the registry, load the rows, render one 80x24 frame to stdout and exit")
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
