package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/cli"
)

// The registry is data: one JSON file per registered extension,
// <dir>/<name>.json, holding the bin and the catalog it answered. Mounting
// reads these files and never execs; register and refresh are the only
// writers.

// Entry is one registry file.
type Entry struct {
	Bin          string    `json:"bin"`
	RegisteredAt time.Time `json:"registeredAt"`
	// RefreshEvery overrides how often nabe reloads the extension's rows
	// (a Go duration; "0s" never; default 60s). Register and refresh keep
	// it.
	RefreshEvery string         `json:"refreshEvery,omitempty"`
	Catalog      cli.CatalogDoc `json:"catalog"`
}

// Registry is the directory of registry files.
type Registry struct{ Dir string }

// DefaultDataDir is $XDG_DATA_HOME/nabe, else ~/.local/share/nabe.
func DefaultDataDir() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "nabe"
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "nabe")
}

// RegistryIn is the registry under a nabe data directory.
func RegistryIn(data string) Registry { return Registry{Dir: filepath.Join(data, "catalogs")} }

// execTimeout bounds every exec of an extension bin.
const execTimeout = 60 * time.Second

// validName is an extension name: it becomes a file name and the prefix of
// every mounted noun.
var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_]*$`)

func (g Registry) path(name string) string { return filepath.Join(g.Dir, name+".json") }

// Names lists the registered extensions, sorted.
func (g Registry) Names() ([]string, error) {
	files, err := os.ReadDir(g.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, f := range files {
		if name, ok := strings.CutSuffix(f.Name(), ".json"); ok && !f.IsDir() {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// Read loads one registry file.
func (g Registry) Read(name string) (Entry, error) {
	var e Entry
	data, err := os.ReadFile(g.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return e, fmt.Errorf("%s is not registered (%s)", name, g.path(name))
	}
	if err != nil {
		return e, err
	}
	if err := json.Unmarshal(data, &e); err != nil {
		return e, fmt.Errorf("%s: %w", g.path(name), err)
	}
	return e, nil
}

// write stores an entry atomically.
func (g Registry) write(name string, e Entry) error {
	if err := os.MkdirAll(g.Dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(g.Dir, "."+name+"-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), g.path(name))
}

// Summary is what register and refresh report.
type Summary struct {
	Summary string `json:"summary"`
	Name    string `json:"name"`
	Bin     string `json:"bin"`
	Version string `json:"version"`
	File    string `json:"file"`
	Nouns   int    `json:"nouns"`
	Verbs   int    `json:"verbs"`
	Pages   int    `json:"pages"`
	Skipped int    `json:"skipped"`
}

// Register resolves bin (a path, or a name on PATH), asks it for its
// catalog, validates it and writes the registry file under name (else the
// catalog's name).
func (g Registry) Register(ctx context.Context, bin, name string) (Summary, error) {
	path, err := resolveBin(bin)
	if err != nil {
		return Summary{}, err
	}
	doc, err := fetchCatalog(ctx, path)
	if err != nil {
		return Summary{}, err
	}
	if name == "" {
		// Family tools are installed as o-<name>; the page is called <name>.
		name = strings.TrimPrefix(doc.Name, "o-")
	}
	if !validName.MatchString(name) {
		return Summary{}, fmt.Errorf("extension name %q: use lower-case letters, digits and _ (pass --name)", name)
	}
	e := Entry{Bin: path, RegisteredAt: time.Now().UTC().Truncate(time.Second), Catalog: doc}
	if old, err := g.Read(name); err == nil {
		e.RefreshEvery = old.RefreshEvery
	}
	if err := g.write(name, e); err != nil {
		return Summary{}, err
	}
	p := planOf(doc)
	s := Summary{Name: name, Bin: path, Version: doc.Version, File: g.path(name), Nouns: len(p.Nouns), Verbs: p.verbs(), Pages: len(doc.Pages), Skipped: len(p.Skipped)}
	s.Summary = fmt.Sprintf("%s: %d nouns, %d verbs, %d pages (framework nouns skipped: %d)", name, s.Nouns, s.Verbs, s.Pages, s.Skipped)
	return s, nil
}

// Refresh re-registers name from the bin its file names.
func (g Registry) Refresh(ctx context.Context, name string) (Summary, error) {
	e, err := g.Read(name)
	if err != nil {
		return Summary{}, err
	}
	return g.Register(ctx, e.Bin, name)
}

// Unregister removes name's file.
func (g Registry) Unregister(name string) error {
	err := os.Remove(g.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s is not registered (%s)", name, g.path(name))
	}
	return err
}

// Listing is one row of extension list.
type Listing struct {
	Name         string    `json:"name"`
	Bin          string    `json:"bin,omitempty"`
	Version      string    `json:"version,omitempty"`
	RegisteredAt time.Time `json:"registeredAt,omitzero"`
	Nouns        int       `json:"nouns"`
	Verbs        int       `json:"verbs"`
	Error        string    `json:"error,omitempty"`
}

// List reads every registry file; an unreadable one is listed with its error.
func (g Registry) List() ([]Listing, error) {
	names, err := g.Names()
	if err != nil {
		return nil, err
	}
	out := []Listing{}
	for _, name := range names {
		e, err := g.Read(name)
		if err != nil {
			out = append(out, Listing{Name: name, Error: err.Error()})
			continue
		}
		p := planOf(e.Catalog)
		out = append(out, Listing{Name: name, Bin: e.Bin, Version: e.Catalog.Version, RegisteredAt: e.RegisteredAt, Nouns: len(p.Nouns), Verbs: p.verbs()})
	}
	return out, nil
}

// resolveBin is the absolute path of bin: a path when it has a separator,
// else the first executable of that name on PATH.
func resolveBin(bin string) (string, error) {
	if bin == "" {
		return "", errors.New("register needs a bin (a path or a name on PATH)")
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return "", fmt.Errorf("bin %s: %w", bin, err)
	}
	return filepath.Abs(path)
}

// fetchCatalog runs `bin catalog` and validates the answer.
func fetchCatalog(ctx context.Context, bin string) (cli.CatalogDoc, error) {
	var doc cli.CatalogDoc
	out, err := execBin(ctx, bin, "catalog")
	if err != nil {
		return doc, err
	}
	return parseCatalog(filepath.Base(bin), out)
}

// parseCatalog decodes and validates a catalog answer: schema, name and
// nouns are required, and the schema must be the one nabe reads.
func parseCatalog(bin string, data []byte) (cli.CatalogDoc, error) {
	var doc cli.CatalogDoc
	var head map[string]json.RawMessage
	if err := json.Unmarshal(data, &head); err != nil {
		return doc, fmt.Errorf("%s catalog: not a JSON object: %w", bin, err)
	}
	for _, field := range []string{"schema", "name", "nouns"} {
		if _, ok := head[field]; !ok {
			return doc, fmt.Errorf("%s catalog: missing required field %q", bin, field)
		}
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return doc, fmt.Errorf("%s catalog: %w", bin, err)
	}
	if doc.Schema != cli.Schema {
		return doc, fmt.Errorf("%s catalog: schema %d, nabe reads schema %d", bin, doc.Schema, cli.Schema)
	}
	if doc.Name == "" {
		return doc, fmt.Errorf("%s catalog: empty name", bin)
	}
	for i, n := range doc.Nouns {
		if n.Name == "" {
			return doc, fmt.Errorf("%s catalog: nouns[%d] has no name", bin, i)
		}
		for k, v := range n.Verbs {
			if v.Name == "" {
				return doc, fmt.Errorf("%s catalog: noun %s verbs[%d] has no name", bin, n.Name, k)
			}
		}
	}
	return doc, nil
}

// execBin runs bin with args and returns its stdout; a failure carries the
// command and its stderr.
func execBin(ctx context.Context, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		words := filepath.Base(bin) + " " + strings.Join(args[:min(2, len(args))], " ")
		if msg := strings.TrimSpace(errOut.String()); msg != "" {
			return nil, fmt.Errorf("%s failed: %w: %s", words, err, msg)
		}
		return nil, fmt.Errorf("%s failed: %w", words, err)
	}
	return out.Bytes(), nil
}

// plan is what nabe mounts from a catalog: the application nouns with their
// application verbs, and the names of the nouns it skips.
type plan struct {
	Nouns   []cli.CatalogNoun
	Skipped []string
}

// planOf keeps the nouns with at least one application verb whose pin is
// not a modal mode. The framework's own nouns (app, list, page, palette,
// prompt, input, mode, focus, history, bindings, jobs, terminal, ...) have
// interaction verbs only: they run inside the extension's process on its
// own screen, so a host has nothing to mount for them. A noun pinned to a
// mode other than normal (the catalog's pin: an insert-mode field, a
// terminal) lives only inside a modal of the extension's screen, so it is
// skipped too. Kind and pin decide it; no noun is named.
func planOf(doc cli.CatalogDoc) plan {
	var p plan
	for _, n := range doc.Nouns {
		var verbs []cli.CatalogVerb
		for _, v := range n.Verbs {
			if v.Kind == ochazuke.KindApplication {
				verbs = append(verbs, v)
			}
		}
		if len(verbs) == 0 || modalPin(n.Pin) {
			p.Skipped = append(p.Skipped, n.Name)
			continue
		}
		p.Nouns = append(p.Nouns, cli.CatalogNoun{Name: n.Name, Pin: n.Pin, Verbs: verbs})
	}
	return p
}

// normalMode is the base mode: a noun pinned to it is not modal.
const normalMode = "normal"

// modalPin reports a pin to a mode other than normal.
func modalPin(pin string) bool { return pin != "" && pin != normalMode }

func (p plan) verbs() int {
	n := 0
	for _, noun := range p.Nouns {
		n += len(noun.Verbs)
	}
	return n
}
