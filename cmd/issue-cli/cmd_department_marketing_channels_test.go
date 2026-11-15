package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/depttemplate"
	"github.com/kacperwerema/dark-factory/internal/lint"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// channelSlots are the channel types the bundled marketing workflow ships
// commented out, each between its own BEGIN/END channel markers. social
// shipped live once it had a research step (TestBundledMarketingShipsSocial).
var channelSlots = []string{"ppc", "cold-outreach"}

// marketingWorkflow is the bundled marketing workflow file, with the named
// channel slots uncommented the way the slots' header says: "# " removed
// from each line between the slot's BEGIN and END markers, in every block
// so marked (its eval and its type).
func marketingWorkflow(t *testing.T, uncomment ...string) string {
	t.Helper()
	tmpl, err := depttemplate.Load("marketing")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := workflowupgrade.ReadTemplate(tmpl.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, name := range uncomment {
		marker := "  # BEGIN channel " + name + "\n"
		endMarker := "  # END channel " + name + "\n"
		blocks := 0
		for from := 0; ; {
			begin := strings.Index(text[from:], marker)
			if begin < 0 {
				break
			}
			begin += from + len(marker)
			end := strings.Index(text[begin:], endMarker)
			if end < 0 {
				t.Fatalf("BEGIN channel %s with no END", name)
			}
			end += begin
			block := regexp.MustCompile(`(?m)^  # ?`).ReplaceAllString(text[begin:end], "  ")
			text = text[:begin] + block + text[end:]
			from = begin + len(block) + len(endMarker)
			blocks++
		}
		if blocks == 0 {
			t.Fatalf("no BEGIN/END channel %s block", name)
		}
	}
	return text
}

func loadMarketingWorkflow(t *testing.T, uncomment ...string) *tracker.WorkflowConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "marketing.yaml")
	mustWrite(t, path, marketingWorkflow(t, uncomment...))
	wf, err := tracker.LoadWorkflow(path)
	if err != nil {
		t.Fatalf("uncommented %v: %v", uncomment, err)
	}
	return wf
}

// TestBundledMarketingShipsSocial: as shipped, social is a live type with
// its eval and no channel markers left, and every other channel slot is
// still commented: a worker gets their prompts only once the owner
// uncomments them. Each slot has its own BEGIN/END blocks, its eval and its
// type, to uncomment alone.
func TestBundledMarketingShipsSocial(t *testing.T) {
	wf := loadMarketingWorkflow(t)
	if _, ok := wf.Types["social"]; !ok {
		t.Fatal("the bundled marketing workflow has no social type")
	}
	text := marketingWorkflow(t)
	if strings.Contains(text, "BEGIN channel social") || strings.Contains(text, "END channel social") {
		t.Error("social is live: its channel markers should be gone")
	}
	for _, name := range channelSlots {
		if _, ok := wf.Types[name]; ok {
			t.Errorf("the bundled marketing workflow ships type %s uncommented", name)
		}
		for _, m := range []string{"  # BEGIN channel " + name + "\n", "  # END channel " + name + "\n"} {
			if strings.Count(text, m) != 2 {
				t.Errorf("want two %q lines (the eval and the type), got %d", strings.TrimSpace(m), strings.Count(text, m))
			}
		}
	}
	evals := map[string]bool{}
	for _, e := range wf.Evals {
		evals[e.ID] = true
	}
	if !evals["social"] {
		t.Error("social has no eval")
	}
	for _, name := range channelSlots {
		if evals[name] {
			t.Errorf("the bundled marketing workflow ships eval %s uncommented", name)
		}
	}
	if _, ok := wf.Types["seo-pass"]; !ok {
		t.Error("seo-pass is missing: the file read is not the marketing template")
	}
}

// socialPath is the social channel's path: research before the brief
// (factory/open-source-partners-integrate-outside-tools-openmontage-openseo-postiz-instead-of-building-them-in-house#D-6).
var socialPath = []string{"research", "brief", "draft", "owner review", "publish", "measure", "done"}

// assertChannelPath checks a channel type's path is want, every step on it
// has its transition, and the only owner_only gate is the step into
// publish, the one way out of the machine.
func assertChannelPath(t *testing.T, wf *tracker.WorkflowConfig, name string, want []string) {
	t.Helper()
	typ, ok := wf.Types[name]
	if !ok {
		t.Fatalf("type %s is missing", name)
	}
	if strings.Join(typ.Path, ",") != strings.Join(want, ",") {
		t.Errorf("%s path %v, want %v", name, typ.Path, want)
	}
	eff := wf.ForIssue(&tracker.Issue{Type: name, Status: typ.Path[0]})
	publishes := false
	for i := 1; i < len(typ.Path); i++ {
		from, to := typ.Path[i-1], typ.Path[i]
		var tr *tracker.WorkflowTransition
		for j := range eff.Transitions {
			if eff.Transitions[j].From == from && eff.Transitions[j].To == to {
				tr = &eff.Transitions[j]
			}
		}
		if tr == nil {
			t.Errorf("%s: no %s → %s", name, from, to)
			continue
		}
		owner := false
		for _, a := range tr.Actions {
			owner = owner || (a.Type == "require_human_approval" && a.OwnerOnly)
		}
		if to == "publish" {
			publishes = true
			if !owner {
				t.Errorf("%s: %s → publish is not owner_only", name, from)
			}
		} else if owner {
			t.Errorf("%s: %s → %s is owner_only: only → publish goes out", name, from, to)
		}
	}
	if !publishes {
		t.Errorf("%s: no step into publish on the path", name)
	}
}

// installMarketingWith installs Marketing from its template in a sandbox
// project, with brand.md and icp.md settled, and its workflow file
// replaced by text; it returns the project for lint.
func installMarketingWith(t *testing.T, text string) *tracker.Project {
	t.Helper()
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing"}); err != nil {
		t.Fatal(err)
	}
	proj = reloadDeptProject(t, cfg)
	for _, f := range []string{"brand.md", "icp.md"} {
		mustWrite(t, filepath.Join(proj.Root(), "departments", "marketing", f), "# settled\n")
	}
	wfPath := filepath.Join(proj.Root(), "workflows", "marketing.yaml")
	if _, err := os.Stat(wfPath); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, wfPath, text)
	return proj
}

// TestSocialTypeOnItsPath is AC1 and AC4 on the template: social walks
// research → brief → draft → owner review → publish → measure → done, the
// step into publish is its only owner_only gate, Marketing's template
// pairs it with the Social system, and an installed Marketing lints clean.
func TestSocialTypeOnItsPath(t *testing.T) {
	wf := loadMarketingWorkflow(t)
	assertChannelPath(t, wf, "social", socialPath)
	tmpl, err := depttemplate.Load("marketing")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(tmpl.Systems, "Social") {
		t.Errorf("the marketing department template's systems %v lack Social", tmpl.Systems)
	}
	proj := installMarketingWith(t, marketingWorkflow(t))
	assertMarketingLintClean(t, proj, "as shipped")
}

// TestSocialPublishOwnerOnlyLint is AC4: health lint's owner-only check
// finds nothing on the bundled file, and names type social when its step
// into publish loses owner_only.
func TestSocialPublishOwnerOnlyLint(t *testing.T) {
	text := marketingWorkflow(t)
	ownerOnly := func(raw string) []string {
		t.Helper()
		got, err := lint.File("marketing.yaml", []byte(raw), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, f := range got {
			if f.Check == "owner-only" {
				out = append(out, f.Message)
			}
		}
		return out
	}
	if got := ownerOnly(text); len(got) != 0 {
		t.Fatalf("owner-only findings on the bundled file: %v", got)
	}
	// The social type's own owner review → publish, replacing the base one
	// without owner_only.
	loose := strings.Replace(text, `    transitions:
      - from: "research"
        to: "brief"`, `    transitions:
      - from: "owner review"
        to: "publish"
        replace: true
        actions:
          - type: "require_human_approval"
            status: "publish"
      - from: "research"
        to: "brief"`, 1)
	if loose == text {
		t.Fatal("the social type's transitions block moved: update the test")
	}
	got := ownerOnly(loose)
	if len(got) != 1 || !strings.Contains(got[0], "type social enters publish from owner review") {
		t.Fatalf("owner-only findings = %v, want one on type social", got)
	}
}

// TestCommentedChannelSlotsLoadWhenUncommented: each slot, uncommented
// alone, loads as a type whose every step has its transition; the only
// owner_only gate on its path is owner review → publish, the one way out of
// the machine; an installed Marketing with that file lints clean. PPC and
// cold outreach keep no prompts of their own (they spend, or send to
// strangers).
func TestCommentedChannelSlotsLoadWhenUncommented(t *testing.T) {
	for _, name := range channelSlots {
		t.Run(name, func(t *testing.T) {
			wf := loadMarketingWorkflow(t, name)
			for _, other := range channelSlots {
				if _, ok := wf.Types[other]; ok != (other == name) {
					t.Errorf("uncommenting %s: type %s present = %v", name, other, ok)
				}
			}
			assertChannelPath(t, wf, name, []string{"brief", "draft", "owner review", "publish", "measure", "done"})
			if len(wf.Types[name].Statuses) != 0 {
				t.Errorf("%s has prompts of its own: the slot stays a description and a path", name)
			}
			proj := installMarketingWith(t, marketingWorkflow(t, name))
			assertMarketingLintClean(t, proj, name+" uncommented")
		})
	}
}

// socialProducts are tools a social channel or its research could run on.
// The prompts name the department's connectors (`social`, `trends`,
// `competitors`), never one of them, so swapping the tool is a change to the
// connectors: entry only.
var socialProducts = []string{"postiz", "buffer", "hootsuite", "later.com", "mixpost", "typefully", "publer", "sprout", "metricool", "zapier",
	"trendspyg", "pytrends", "4cat", "zeeschuimer", "mcp-youtube", "bluesky-mcp-server", "apify", "brandwatch", "socialblade", "reddit-mcp-buddy", "praw"}

// TestSocialTypeNamesNoTool is AC2 on the template text: research asks for
// similar profiles, competitors and trends with links, read-only, through
// the `trends` and `competitors` connectors, and puts its report on the
// shelf; the brief cites that output; the draft makes no connector call,
// publish schedules only at the approved dates and records each post,
// measure reads numbers or writes "no value yet". No prompt names a product.
func TestSocialTypeNamesNoTool(t *testing.T) {
	wf := loadMarketingWorkflow(t)
	prompts := map[string]string{}
	for _, s := range wf.Types["social"].Statuses {
		prompts[s.Name] = s.Prompt
	}
	want := map[string][]string{
		"research": {"similar profiles", "competitors", "competitors.md", "icp.md", "trends", "hashtags", "formats", "cadence", "hooks", "link",
			"read-only", "`trends` and `competitors` connectors", "public pages only", "never post, schedule", "never log in",
			"no `social` connector call", "mem-guard check", "## Research", "at least 5 distinct links", "--kind report", "cites it"},
		"brief":        {"`social` connector", "brand.md", "icp.md", "rules", "research output", "cite", "o-…", "finding"},
		"draft":        {"no connector call", "--kind post"},
		"owner review": {"research report", "end your turn", "revise:"},
		"publish":      {"`social` connector", "schedule", "approved", "Never post now", "## Published", "issue-cli output publish"},
		"measure":      {"`social` connector", "analytics read calls only", "no value yet", "goal set"},
	}
	for status, words := range want {
		p, ok := prompts[status]
		if !ok {
			t.Errorf("social has no %s prompt", status)
			continue
		}
		for _, w := range words {
			if !strings.Contains(strings.ToLower(p), strings.ToLower(w)) {
				t.Errorf("%s prompt doesn't say %q:\n%s", status, w, p)
			}
		}
	}
	for status, p := range prompts {
		low := strings.ToLower(p)
		for _, product := range socialProducts {
			if regexp.MustCompile(`\b` + regexp.QuoteMeta(product) + `\b`).MatchString(low) {
				t.Errorf("%s prompt names %s: name the department's connector instead", status, product)
			}
		}
	}
	if len(prompts) != len(want) {
		t.Errorf("social prompts for %d statuses, want %d (%v)", len(prompts), len(want), prompts)
	}
}
