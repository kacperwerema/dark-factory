package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/postmortem"
)

const pmQueueBody = "## Steps\nx\n## Worked\nx\n## Did not work\nnone\n## Decisions\nnone\n## Known\nnone\n## Proposed actions\n\n" +
	"- [ ] A1: Remember the flaky lock test\n  Differently: a fresh agent would retry the lock test before blaming the code\n  Place: memory pitfall\n  Text: The lock test is flaky under load; retry it once.\n" +
	"- [ ] A2: Write the coupon rounding rule down\n  Differently: a fresh agent would round coupons per line, not per cart\n  Place: memory convention\n  Text: Coupons round per line.\n"

// AC5: list --grouped prints the page's rows: a group of copies naming
// every post-mortem, the alone ones, the fixed ones, and the answers left.
func TestPostmortemListGrouped(t *testing.T) {
	proj := postmortemFixture(t, "ask")
	root := filepath.Dir(proj.IssueDir)
	dir := filepath.Join(root, "retros")
	os.WriteFile(filepath.Join(proj.IssueDir, "CLI", "fixer.md"), []byte("---\ntitle: \"Fixer\"\nstatus: \"done\"\n---\n\nBody.\n"), 0o644)
	day := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	a, err := postmortem.Save(dir, postmortem.Meta{Issue: "cli/sample", Title: "Sample"}, pmQueueBody, day)
	if err != nil {
		t.Fatal(err)
	}
	b, err := postmortem.Save(dir, postmortem.Meta{Issue: "cli/other", Title: "Other"}, strings.Replace(pmQueueBody, "Coupons round per line.", "Receipts print twice.", 1), day.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runPostmortem(ctx, []string{"fixed", filepath.Base(b) + "#A2", "--by", "cli/fixer"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "Fixed by cli/fixer: 1 action(s) need no answer now.")
	stdout.Reset()
	if err := runPostmortem(ctx, []string{"list", "--grouped"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		"[memory-0] 2 say the same: Remember the flaky lock test",
		"cli/other  " + filepath.Base(b) + "#A1",
		"cli/sample  " + filepath.Base(a) + "#A1",
		"alone  cli/sample  " + filepath.Base(a) + "#A2",
		"== fixed already: no answer needed ==",
		filepath.Base(b) + "#A2  fixed by cli/fixer",
		"3 open action(s) of 2 post-mortem(s) need 2 answer(s): 1 group(s) of 2 copies, 1 alone; 1 fixed already need none.",
		"/p/" + proj.Slug + "/postmortems",
	} {
		assertContains(t, out, want)
	}
	stdout.Reset()
	ctx.JSONOutput = true
	if err := runPostmortem(ctx, []string{"list", "--grouped"}); err != nil {
		t.Fatal(err)
	}
	var q pmQueue
	if err := json.Unmarshal(stdout.Bytes(), &q); err != nil {
		t.Fatalf("%v\n%s", err, stdout.String())
	}
	if q.Open != 3 || q.Rows != 2 || len(q.Groups) != 2 || q.Groups[0].Count != 2 || len(q.Fixed) != 1 || q.Fixed[0].Fix != "fixed by cli/fixer" {
		t.Fatalf("json: %+v", q)
	}
}

// AC3: fixed --copies links every copy of the group; a --by that is neither
// an issue nor a commit on main links nothing.
func TestPostmortemFixedLinksTheCopies(t *testing.T) {
	proj := postmortemFixture(t, "ask")
	dir := filepath.Join(filepath.Dir(proj.IssueDir), "retros")
	day := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	a, _ := postmortem.Save(dir, postmortem.Meta{Issue: "cli/sample", Title: "Sample"}, pmQueueBody, day)
	b, _ := postmortem.Save(dir, postmortem.Meta{Issue: "cli/other", Title: "Other"}, pmQueueBody, day.Add(time.Hour))
	ctx, stdout, _ := newTestContext(proj, false)
	before, _ := os.ReadFile(a)
	if err := runPostmortem(ctx, []string{"fixed", filepath.Base(a) + "#A1", "--by", "no/such-issue"}); err == nil || !strings.Contains(err.Error(), "nothing was linked") {
		t.Fatalf("an unknown --by: %v", err)
	}
	if after, _ := os.ReadFile(a); string(after) != string(before) {
		t.Fatal("a refused link wrote the retro")
	}
	if err := runPostmortem(ctx, []string{"fixed", filepath.Base(a) + "#A1", "--by", "cli/sample", "--copies"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "Fix underway: cli/sample (shipping): the action(s) wait until it is done.")
	for _, p := range []string{a, b} {
		raw, _ := os.ReadFile(p)
		acts := postmortem.Actions(string(raw))
		if acts[0].Fixed != "cli/sample" || acts[1].Fixed != "" {
			t.Errorf("%s: %+v", filepath.Base(p), acts)
		}
	}
	stdout.Reset()
	if err := runPostmortem(ctx, []string{"show", "cli/sample"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "fix underway: cli/sample (shipping)")
	if err := runPostmortem(ctx, []string{"fixed", "nope.md#A1", "--by", "cli/sample"}); err == nil {
		t.Fatal("a file outside the queue was linked")
	}
}
