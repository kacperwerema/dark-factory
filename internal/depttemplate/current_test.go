package depttemplate

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// currentPins is the file holding the sha256 of each bundled file
// department update compares by hash (role.md and every job brief, in
// every template), one "<key>/<path> <sha256>" line each.
var currentPins = filepath.Join("testdata", "current.sha256")

// comparedFiles is every template file department update tells apart as
// current, old or edited by its sha256, keyed "<key>/<path>". The
// workflow file is left out: an earlier workflow text is listed only when
// it differs in wording (TestEarlierWorkflowsDifferInWordingOnly), and the
// workflow templates belong to workflow upgrade's catalog.
func comparedFiles(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, tm := range List() {
		rels := []string{"role.md"}
		for _, j := range tm.Jobs {
			if j.Brief != "" && !strings.HasPrefix(j.Brief, "builtin:") {
				rels = append(rels, j.Brief)
			}
		}
		for _, rel := range rels {
			raw, err := tm.file(rel)
			if err != nil {
				t.Fatalf("%s: %v", tm.Key, err)
			}
			out[tm.Key+"/"+rel] = raw
		}
	}
	return out
}

func readPins(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open(currentPins)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pins := map[string]string{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, sum, ok := strings.Cut(line, " ")
		if !ok || len(sum) != 64 {
			t.Fatalf("%s:%d: want \"<key>/<path> <sha256>\", got %q", currentPins, n, line)
		}
		pins[name] = sum
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return pins
}

// TestBundledFilesChangeOnlyWithTheirEarlierHash: a bundled role.md or
// job brief that changes must leave its old hash in earlierTexts, or
// department update calls every install of the old text "edited by you"
// and never offers the new one. The pins hold today's hashes, so a change
// fails here until the old hash moves into earlierTexts (with the old text
// under testdata/earlier/<key>/, which TestEarlierHashesAreBacked checks)
// and the pin is set to the new hash.
func TestBundledFilesChangeOnlyWithTheirEarlierHash(t *testing.T) {
	pins := readPins(t)
	files := comparedFiles(t)
	keys := map[string]bool{}
	for name := range files {
		key, _, _ := strings.Cut(name, "/")
		keys[key] = true
	}
	for _, tm := range List() {
		if !keys[tm.Key] {
			t.Errorf("template %s: no compared file", tm.Key)
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		cur := sha(files[name])
		pin, ok := pins[name]
		switch {
		case !ok:
			t.Errorf("%s is not pinned: add the line %q to %s", name, name+" "+cur, currentPins)
		case pin != cur:
			key, rel, _ := strings.Cut(name, "/")
			n := len(earlierTexts[key].Files[rel]) + 1
			t.Errorf("%s changed (pinned %s, now %s).\n"+
				"Move the old hash into earlierTexts[%q].Files[%q] (internal/depttemplate/update.go), "+
				"save the old text as testdata/earlier/%s/%s.%d (git show HEAD:cmd/issue-cli/templates/departments/%s > that file), "+
				"then set its line in %s to %q.\n"+
				"Without it department update calls every install of the old text edited and never offers the new one.",
				name, pin, cur, key, rel, key, rel, n, name, currentPins, name+" "+cur)
		}
	}
	for name := range pins {
		if _, ok := files[name]; !ok {
			t.Errorf("%s: pinned in %s but no template has it: drop the line", name, currentPins)
		}
	}
}
