package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The done originals of queue items 444 and 445 (retro sweep o-20261120-3387
// item N4): the sweep report f9bc's follow-ups 1 and 2, shipped on 11-17.
const (
	twinScored = "---\ntitle: \"Memory usefulness is scored only at statuses where a lift is possible, not 20 no-help stages at idea\"\nstatus: \"done\"\nsystem: \"Factory\"\n---\n## Why\n\nFrom the sweep report f9bc (issues/.outputs/files/product/o-20261117-f9bc-report.md), item 1: its Why and Evidence lines are the brief.\n"
	twinMix    = "---\ntitle: \"memory show and the Unhelpful list show the status mix, so an empty score is visible before anyone forgets a memory\"\nstatus: \"done\"\nsystem: \"Factory\"\n---\n## Why\n\nFrom the sweep report f9bc (issues/.outputs/files/product/o-20261117-f9bc-report.md), item 2: its Why and Evidence lines are the brief.\n"
	twinReport = "factory/memory-usefulness-a-first-look"
	// The slugs come from the titles.
	twinScoredSlug = "factory/memory-usefulness-is-scored-only-at-statuses-where-a-lift-is-possible-not-20-no-help-stages-at-idea"
	twinMixSlug    = "factory/memory-show-and-the-unhelpful-list-show-the-status-mix-so-an-empty-score-is-visible-before-anyone-forgets-a-memory"
)

func twinProject(t *testing.T) (*Context, *bytes.Buffer, string) {
	t.Helper()
	proj := newTestProject(t)
	for _, d := range []string{"Factory", ".outputs"} {
		if err := os.MkdirAll(filepath.Join(proj.IssueDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(proj.IssueDir, "Factory", "memory-scored-at-lift-statuses.md"), twinScored)
	mustWrite(t, filepath.Join(proj.IssueDir, "Factory", "memory-show-status-mix.md"), twinMix)
	mustWrite(t, filepath.Join(proj.IssueDir, ".outputs", "outputs.jsonl"),
		fmt.Sprintf(`{"op":"add","id":"o-20261117-f9bc","date":"2026-11-17T17:09:32Z","department":"product","title":"Memory usefulness","kind":"report","file":"product/o-20261117-f9bc-report.md","issue":%q}`+"\n", twinReport))
	ctx, out, _ := testContext(proj)
	return ctx, out, proj.IssueDir
}

// TestCreateWarnsOnDoneTwinFromTheSameSweepItem: 445 named the report
// issue, not the output, and its follow-up number, not an item; create
// maps the slug to the output and warns with the done issue and the close
// line. It still writes the issue.
func TestCreateWarnsOnDoneTwinFromTheSameSweepItem(t *testing.T) {
	ctx, out, dir := twinProject(t)
	body := "Follow-up from the report " + twinReport + " (its code follow-up 2; see the report on the shelf)."
	if err := runCreate(ctx, []string{"--title", "Status mix for scored uses", "--system", "CLI", "--body", body}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"⚠ A done issue may already hold this",
		"- " + twinMixSlug + ": same sweep source (report f9bc, item 2)",
		"If it does: issue-cli close cli/status-mix-for-scored-uses --fixed-by " + twinMixSlug,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("create output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, twinScoredSlug) {
		t.Errorf("item 1 named for an item-2 follow-up:\n%s", got)
	}
	readIssueFile(t, filepath.Join(dir, "CLI", "status-mix-for-scored-uses.md"))
}

// TestCreateWarnsOnDoneTwinByTitle: 444's title shares 7 of its 12 words
// with the done issue, with no source line at all.
func TestCreateWarnsOnDoneTwinByTitle(t *testing.T) {
	ctx, out, _ := twinProject(t)
	title := "Memory usefulness is scored only where a memory could have helped (a lift is possible), not on every briefing that carried it"
	if err := runCreate(ctx, []string{"--title", title, "--system", "Factory"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "- "+twinScoredSlug+": 7 of 12 title words") {
		t.Errorf("no title twin:\n%s", got)
	}
}

// TestCreateQuietWithoutDoneTwin: another topic, the same source's other
// item and an open twin warn nothing.
func TestCreateQuietWithoutDoneTwin(t *testing.T) {
	ctx, out, dir := twinProject(t)
	mustWrite(t, filepath.Join(dir, "open-twin.md"), "---\ntitle: \"Board cards get a colour for each department, in the board view\"\nstatus: \"idea\"\n---\nFrom the sweep report f9bc, item 3.\n")
	body := "From the sweep report f9bc (o-20261117-f9bc), item 3: the brief."
	if err := runCreate(ctx, []string{"--title", "Board cards get a colour per department", "--body", body}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Contains(got, "done issue") {
		t.Errorf("warned without a done twin:\n%s", got)
	}
}

func TestTwinSource(t *testing.T) {
	hexes := map[string]string{twinReport: "f9bc"}
	for _, c := range []struct{ body, want string }{
		{"From retro sweep o-20261120-3387 (report.md), item N4 (cause C5)", "3387#N4"},
		{"From the sweep report F9BC, item 1:", "f9bc#1"},
		{"Follow-up from the report " + twinReport + " (its code follow-up 1)", "f9bc#1"},
		{"Follow-up from the report factory/unknown (its code follow-up 1)", ""},
		{"From sweep o-20261105-665d with no item", ""},
		{"item 4 of nothing", ""},
		{strings.Repeat("x", twinHead) + " o-20261105-665d item 1", ""},
	} {
		if got := twinSource(c.body, hexes); got != c.want {
			t.Errorf("twinSource(%.60q) = %q, want %q", c.body, got, c.want)
		}
	}
}

func TestWarnDoneTwinsCapsTheList(t *testing.T) {
	var twins []doneTwin
	for i := range 5 {
		twins = append(twins, doneTwin{Slug: fmt.Sprintf("x/done-%d", i), Shared: 6, Of: 8})
	}
	var b bytes.Buffer
	warnDoneTwins(&b, "x/new", twins)
	got := b.String()
	if !strings.Contains(got, "x/done-2: 6 of 8 title words") || strings.Contains(got, "x/done-3") || !strings.Contains(got, "… and 2 more") {
		t.Errorf("list not capped at %d:\n%s", twinMaxShown, got)
	}
	b.Reset()
	warnDoneTwins(&b, "x/new", nil)
	if b.Len() != 0 {
		t.Errorf("no twins printed %q", b.String())
	}
}
