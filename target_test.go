package main

import (
	"strings"
	"testing"
)

// From the CLI a mounted verb takes --target: the extension's own id, or
// the row's nabe id (which names its list); the proxy passes the
// extension's id to the bin.
func TestCLITargetReachesTheBin(t *testing.T) {
	state, data := registered(t)
	last := func() string {
		got := calls(t, state)
		return got[len(got)-1]
	}
	for _, c := range []struct {
		args []string
		call string
		out  string
	}{
		{[]string{"fake-item", "touch", "--target", "a1"}, "item touch --target a1 --json {} --yes", `"touched": "a1"`},
		{[]string{"fake-it", "tou", "--target", "b2"}, "item touch --target b2 --json {} --yes", `"touched": "b2"`},
		{[]string{"fake-item", "touch", "--target", "ext/fake/item/a1"}, "item touch --target a1 --json {} --yes", `"touched": "a1"`},
		{[]string{"fake-project", "open", "--target", "ext/fake/projects/p1"}, "project open --target p1 --json {} --yes", `"opened": "p1"`},
		{[]string{"fake-project", "open", "--target", "p9"}, "project open --target p9 --json {} --yes", `"opened": "p9"`},
	} {
		out, errOut, err := nabe(t, append([]string{"-data", data}, c.args...)...)
		if err != nil || last() != c.call || !strings.Contains(out, c.out) {
			t.Fatalf("%q: %v call %q\n%s\n%s", c.args, err, last(), out, errOut)
		}
	}
	// The confirm names the target; declined, nothing runs.
	_, _, err := nabe(t, "-data", data, "fake-item", "remove", "--target", "a1")
	if err == nil || !strings.Contains(err.Error(), "declined: Remove a1?") || strings.HasPrefix(last(), "item remove") {
		t.Fatalf("remove without --yes: %v, last %q", err, last())
	}
	if out, _, err := nabe(t, "-data", data, "fake-item", "remove", "--target", "a1", "--yes"); err != nil || last() != "item remove --target a1 --json {} --yes" || !strings.Contains(out, `"removed": "a1"`) {
		t.Fatalf("remove --yes: %v %q %s", err, last(), out)
	}
	// Without --target the verb runs untargeted, as before.
	if _, _, err := nabe(t, "-data", data, "fake-item", "touch"); err != nil || last() != "item touch --json {} --yes" {
		t.Fatalf("touch: %v %q", err, last())
	}
	if _, _, err := nabe(t, "-data", data, "extension", "refresh", "--target", "nope"); err == nil || !strings.Contains(err.Error(), "extension is not a mounted noun") {
		t.Fatalf("extension --target nope: %v", err)
	}
}
