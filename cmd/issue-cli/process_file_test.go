package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// AC1: --file reads a candidate workflow file as written, prints its
// lifecycle, transitions and file lint, and writes nothing.
func TestProcessFileShowsACandidate(t *testing.T) {
	proj := makeDepartmentProject(t)
	ctx, out, _ := newTestContext(proj, false)
	dir := proj.WorkDir

	marketing, err := workflowupgrade.ReadTemplate("marketing")
	if err != nil {
		t.Fatal(err)
	}
	// A candidate with a typo the parser would drop.
	text := string(marketing) + "\nstatuss: []\n"
	cand := filepath.Join(dir, "workflows", "marketing.next.yaml")
	writeDeptFile(t, cand, text)
	live := filepath.Join(dir, "workflows", "marketing.yaml")
	before := snapshotFiles(t, cand, live, proj.WorkflowFile)

	if err := runProcessWorkflow(ctx, []string{"--file", cand}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	assertContains(t, got, "Workflow file: "+cand+" (candidate, read only")
	assertContains(t, got, "research → brand → icp") // the foundation type's path: the candidate's, not the live content file's
	assertContains(t, got, `unknown key "statuss"`)
	assertContains(t, got, "Nothing was written")
	assertContains(t, got, "--department Marketing (workflows/marketing.yaml)")
	if strings.Contains(got, "Departments on their own workflow file") {
		t.Errorf("a candidate is not Product's file:\n%s", got)
	}

	out.Reset()
	if err := runProcessTransitions(ctx, []string{"--file", cand, "--type", "social"}); err != nil {
		t.Fatal(err)
	}
	got = out.String()
	assertContains(t, got, `type "social"`)
	assertContains(t, got, "(candidate, read only")
	assertContains(t, got, "== Lint (file checks)")
	if strings.Contains(got, "/hall/station/") {
		t.Errorf("the hall draws the live file, not a candidate:\n%s", got)
	}

	// Unknown type: refused from the candidate's own types.
	if err := runProcessTransitions(ctx, []string{"--file", cand, "--type", "nope"}); err == nil || !strings.Contains(err.Error(), "social") {
		t.Errorf("--type nope: err = %v, want the candidate's types listed", err)
	}

	// Load errors name the file and exit non-zero.
	broken := filepath.Join(dir, "broken.yaml")
	writeDeptFile(t, broken, "statuses: [\n")
	empty := filepath.Join(dir, "empty.yaml")
	writeDeptFile(t, empty, "transitions: []\n")
	for _, c := range []struct{ path, want string }{
		{broken, "parsing workflow " + broken},
		{empty, "no statuses"},
		{filepath.Join(dir, "missing.yaml"), "missing.yaml"},
	} {
		for _, run := range []func(*Context, []string) error{runProcessWorkflow, runProcessTransitions} {
			err := run(ctx, []string{"--file", c.path})
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "live workflow file is unchanged") {
				t.Errorf("--file %s: err = %v, want %q", c.path, err, c.want)
			}
		}
	}

	// --file names its own file: not with --department or a slug.
	if err := runProcessWorkflow(ctx, []string{"--file", cand, "--department", "Marketing"}); err == nil || !strings.Contains(err.Error(), "pass one") {
		t.Errorf("--file with --department: err = %v", err)
	}
	if err := runProcessTransitions(ctx, []string{"--file", cand, "seo/faq-page"}); err == nil || !strings.Contains(err.Error(), "--file and an issue slug") {
		t.Errorf("--file with a slug: err = %v", err)
	}

	// No project needed: the file is the input.
	noProj, out2, _ := newTestContext(nil, false)
	if err := runProcessWorkflow(noProj, []string{"--file", cand}); err != nil {
		t.Fatalf("--file without a project: %v", err)
	}
	if strings.Contains(out2.String(), "Live files:") {
		t.Errorf("no project, no live files to name:\n%s", out2.String())
	}

	if after := snapshotFiles(t, cand, live, proj.WorkflowFile); after != before {
		t.Errorf("--file changed a file:\nbefore %s\nafter  %s", before, after)
	}
}

// snapshotFiles is each file's size, mtime and content, to show a read
// wrote nothing.
func snapshotFiles(t *testing.T, paths ...string) string {
	t.Helper()
	var b strings.Builder
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(p + " " + st.ModTime().String() + " " + string(raw) + "\n")
	}
	return b.String()
}

// AC2: --workflow names a system overlay; a path there is refused and
// points to --file and --department, while a system name still works.
func TestProcessWorkflowFlagPathRefused(t *testing.T) {
	proj := makeDepartmentProject(t)
	ctx, out, _ := newTestContext(proj, false)
	for _, v := range []string{"workflows/marketing.template.yaml", "marketing.yaml", "next.yml"} {
		for _, topic := range []string{"workflow", "transitions"} {
			err := runProcess(ctx, []string{topic, "--workflow", v})
			if err == nil || !strings.Contains(err.Error(), "--file "+v) || !strings.Contains(err.Error(), "--department") {
				t.Errorf("process %s --workflow %s: err = %v, want the --file / --department pointer", topic, v, err)
			}
		}
		if err := runProcessTransitions(ctx, []string{"--workflow", v}); err == nil || !strings.Contains(err.Error(), "--file") {
			t.Errorf("process transitions --workflow %s (topic parse): err = %v", v, err)
		}
	}
	out.Reset()
	if err := runProcess(ctx, []string{"transitions", "--workflow", "CLI"}); err != nil {
		t.Fatalf("--workflow CLI (a system name): %v", err)
	}
	assertContains(t, out.String(), `system "CLI"`)
}

// AC3: the splice steps in docs/departments.md check the candidate with
// --file before it replaces the live file.
func TestDepartmentsDocSpliceUsesFile(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "departments.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	start := strings.Index(doc, "### Bringing a template change into a live department")
	end := strings.Index(doc, "### Video studio")
	if start < 0 || end < start {
		t.Fatal("docs/departments.md: the splice section moved; update this test")
	}
	sec := doc[start:end]
	check := strings.Index(sec, "issue-cli process workflow --file workflows/marketing.next.yaml")
	move := strings.Index(sec, "mv workflows/marketing.next.yaml workflows/marketing.yaml")
	if check < 0 || move < 0 || check > move {
		t.Errorf("the splice steps should check the candidate with --file before moving it over the live file:\n%s", sec)
	}
	assertContains(t, sec, "process transitions --file")
	if strings.Contains(sec, "There is no dry run of a candidate file") {
		t.Error("the splice section still says there is no dry run")
	}
}
