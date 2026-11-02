package workflowupgrade

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// realBranch is a branch over this repository's own files: base and head
// read the working tree, overridden per side by the maps.
func realBranch(t *testing.T, changed []string, base, head map[string]string) Branch {
	t.Helper()
	root := repoRoot(t)
	read := func(over map[string]string) func(string) ([]byte, bool) {
		return func(p string) ([]byte, bool) {
			if s, ok := over[p]; ok {
				return []byte(s), true
			}
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
			return data, err == nil
		}
	}
	return Branch{
		Changed: changed,
		Base:    read(base),
		Head:    read(head),
		HeadDir: func(dir string) []string {
			entries, _ := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
			var names []string
			for _, e := range entries {
				if !e.IsDir() {
					names = append(names, e.Name())
				}
			}
			return names
		},
	}
}

func readRepo(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func flaggedIDs(rep *CheckReport) string {
	var ids []string
	for _, e := range rep.Entries {
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// entriesListing are the catalog entries with a source on file f.
func entriesListing(t *testing.T, f string) []string {
	t.Helper()
	entries, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range entries {
		for _, s := range e.Sources {
			if glob, _, _ := strings.Cut(s, "#"); MatchSource(glob, f) {
				ids = append(ids, e.ID)
				break
			}
		}
	}
	return ids
}

const validatePath = "internal/tracker/workflow_validate.go"

// inCheckRule replaces the first old after checkRule's declaration (the
// file's describeRule switch names the same rules above it).
func inCheckRule(t *testing.T, src, old, new string) string {
	t.Helper()
	at := strings.Index(src, "func (w *WorkflowConfig) checkRule(")
	if at < 0 || !strings.Contains(src[at:], old) {
		t.Fatalf("checkRule has no %q", old)
	}
	return src[:at] + strings.Replace(src[at:], old, new, 1)
}

// The retros' case (20261031-044715, -033319, -134848, -005441): one new
// case in checkRule's shared switch for a rule test-plan-gate names, and
// only test-plan-gate is asked about.
func TestCatalogCheckAttributesANewRuleCaseToItsEntry(t *testing.T) {
	sharing := entriesListing(t, validatePath)
	if len(sharing) < 4 {
		t.Fatalf("expected several entries on %s, got %v", validatePath, sharing)
	}
	src := readRepo(t, validatePath)
	anchor := "\tcase \"has_comment_prefix\":"
	newCase := "\tcase \"has_test_plan_v2\":\n\t\treturn fmt.Errorf(\"test plan v2 missing\")\n"
	head := inCheckRule(t, src, anchor, newCase+anchor)
	entryPath := CatalogPath + "/test-plan-gate.yaml"
	catalog := readRepo(t, entryPath)
	ruleIn := "rule_in: [has_test_plan]"
	if strings.Count(catalog, ruleIn) != 1 {
		t.Fatalf("%s has %d %q", entryPath, strings.Count(catalog, ruleIn), ruleIn)
	}
	headCatalog := strings.Replace(catalog, ruleIn, "rule_in: [has_test_plan, has_test_plan_v2]", 1)

	b := realBranch(t, []string{validatePath}, nil, map[string]string{validatePath: head, entryPath: headCatalog})
	rep, err := CheckBranch(b, "v0.36.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := flaggedIDs(rep); got != "test-plan-gate" {
		t.Errorf("a new has_test_plan_v2 case flagged %q, want only test-plan-gate (entries on the file: %v)", got, sharing)
	}

	// A change inside an existing case is that case's entry's alone.
	edited := inCheckRule(t, src, `return fmt.Errorf("no assignee`, `return fmt.Errorf("still no assignee`)
	rep, err = CheckBranch(realBranch(t, []string{validatePath}, nil, map[string]string{validatePath: edited}), "v0.36.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := flaggedIDs(rep); got != "assignee-gate" {
		t.Errorf("an edit in the has_assignee case flagged %q", got)
	}

	// A change outside the switch is every entry's on checkRule: strict.
	outside := inCheckRule(t, src, `ruleArg = rule[idx+2:]`, `ruleArg = strings.TrimSpace(rule[idx+2:])`)
	rep, err = CheckBranch(realBranch(t, []string{validatePath}, nil, map[string]string{validatePath: outside}), "v0.36.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"assignee-gate", "checklist-gates", "comment-evidence-gates", "test-plan-gate"} {
		if !strings.Contains(","+flaggedIDs(rep)+",", ","+id+",") {
			t.Errorf("a change to checkRule's rule parsing didn't flag %s (flagged %s)", id, flaggedIDs(rep))
		}
	}
}

const dispatchPath = "internal/tracker/dispatch_prompt.go"

// The retros' case (20261031-055913, -123516): one string of the built-in
// briefing changes and catalog-check reports nothing; a code change in the
// same file still counts.
func TestCatalogCheckIgnoresBriefingTextOnly(t *testing.T) {
	if len(entriesListing(t, dispatchPath)) == 0 {
		t.Fatalf("no entry lists %s: the test proves nothing", dispatchPath)
	}
	src := readRepo(t, dispatchPath)
	line := "- `issue-cli lead whoami` shows your lead"
	if !strings.Contains(src, line) {
		t.Fatalf("%s has no %q", dispatchPath, line)
	}
	text := strings.Replace(src, line, "- `issue-cli lead whoami` names your lead", 1)
	rep, err := CheckBranch(realBranch(t, []string{dispatchPath}, nil, map[string]string{dispatchPath: text}), "v0.36.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 0 || !rep.OK() {
		t.Errorf("a briefing string change flagged %s", flaggedIDs(rep))
	}

	code := strings.Replace(src, "func AgentSessionName(slug string) string {", "func AgentSessionName(slug string) string {\n\tslug = strings.TrimSpace(slug)", 1)
	if code == src {
		t.Fatal("AgentSessionName not found")
	}
	rep, err = CheckBranch(realBranch(t, []string{dispatchPath}, nil, map[string]string{dispatchPath: code}), "v0.36.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) == 0 {
		t.Error("a code change in dispatch_prompt.go flagged nothing")
	}
}

func TestTextOnlyChange(t *testing.T) {
	a := []byte("package x\n\n// one\nconst s = \"a\"\nfunc f() string { return `b` + s }\n")
	cases := []struct {
		name string
		b    string
		want bool
	}{
		{"string", "package x\n\n// one\nconst s = \"zz\"\nfunc f() string { return `b` + s }\n", true},
		{"comment and raw string", "package x\n\n// two\nconst s = \"a\"\nfunc f() string { return `c c` + s }\n", true},
		{"code", "package x\n\n// one\nconst s = \"a\"\nfunc f() string { return `b` }\n", false},
		{"new string arg", "package x\n\n// one\nconst s = \"a\"\nfunc f() string { return `b` + s + \"x\" }\n", false},
	}
	for _, c := range cases {
		if got := textOnlyChange(a, []byte(c.b)); got != c.want {
			t.Errorf("%s: textOnlyChange = %v, want %v", c.name, got, c.want)
		}
	}
	if textOnlyChange(nil, a) {
		t.Error("a new file is not a text-only change")
	}
}

// The retros' case (20261031-104618, -101601): a change to one part of a
// bundled template, or to its stamp line, is not every entry's whose
// sources list the whole template; the part an entry's detect: sees is.
func TestCatalogCheckAttributesTemplateChangesByDetect(t *testing.T) {
	tpl := TemplatesPath + "/content.yaml"
	sharing := entriesListing(t, tpl)
	if len(sharing) < 2 {
		t.Fatalf("expected several entries on %s, got %v", tpl, sharing)
	}
	src := readRepo(t, tpl)
	stamp := ReadStamp([]byte(src))
	if stamp == "" {
		t.Fatalf("%s has no stamp", tpl)
	}
	restamped := strings.Replace(src, stamp, "content@v9.9.0", 1)
	rep, err := CheckBranch(realBranch(t, []string{tpl}, nil, map[string]string{tpl: restamped}), "v0.36.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 0 {
		t.Errorf("a stamp move flagged %s", flaggedIDs(rep))
	}
	if len(rep.Templates) != 1 {
		t.Errorf("the template itself must still be checked: %+v", rep.Templates)
	}

	// owner_only on the publish transition is owner-only-gates' and
	// publish-stamp's (its detect sees the publish transition), not others'.
	if !strings.Contains(src, "owner_only: true") {
		t.Fatalf("%s has no owner_only: true", tpl)
	}
	dropped := strings.Replace(src, "owner_only: true", "owner_only: false", 1)
	rep, err = CheckBranch(realBranch(t, []string{tpl}, nil, map[string]string{tpl: dropped}), "v0.36.0")
	if err != nil {
		t.Fatal(err)
	}
	got := flaggedIDs(rep)
	if !strings.Contains(got, "owner-only-gates") {
		t.Errorf("owner_only change didn't flag owner-only-gates: %q", got)
	}
	for _, id := range sharing {
		if id == "owner-only-gates" || id == "publish-stamp" {
			continue
		}
		if strings.Contains(","+got+",", ","+id+",") {
			t.Errorf("owner_only change flagged unrelated %s (%q)", id, got)
		}
	}
}

// An entry without detect: and a template that doesn't parse fall back to
// the whole file.
func TestDetectRegionFallsBack(t *testing.T) {
	if _, ok := detectRegion([]byte("a: 1\n"), nil); ok {
		t.Error("no probes: want the whole file")
	}
	if _, ok := detectRegion([]byte(":\n\t- ["), []Probe{{Path: "a"}}); ok {
		t.Error("broken yaml: want the whole file")
	}
	got, ok := detectRegion([]byte("statuses:\n  - name: a\n  - name: measure\n    prompt: p\n"), []Probe{{Path: "statuses[].name", Equals: "measure"}})
	if !ok || !strings.Contains(got, "prompt: p") || strings.Contains(got, "name: a") {
		t.Errorf("region = %q, %v: want the whole measure status only", got, ok)
	}
}
