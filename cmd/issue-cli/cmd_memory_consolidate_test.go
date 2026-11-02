package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// trailProject is goalsProject with a trail on engine.md: comments of
// every kind, a decision and a retro.
func trailProject(t *testing.T) *tracker.Project {
	t.Helper()
	proj := goalsProject(t)
	path := filepath.Join(proj.IssueDir, "engine.md")
	mustWrite(t, path, "---\ntitle: \"Engine\"\nstatus: \"in progress\"\nsystem: \"Factory\"\n---\nBody.\n\n## Decisions\n- **D-1** · 2026-10-30 · YOLO, provisional: (provisional) The runner keeps TMUX_TMPDIR under /tmp because the temp dir is too deep for a socket.\n")
	if err := tracker.SaveComments(path, []tracker.Comment{
		{ID: 1, Date: "2026-10-30", Text: `approval: "go" → in progress — recorded by lead-acme`},
		{ID: 2, Date: "2026-10-30", Text: "progress: make shot URL= takes one screenshot without a playtest session"},
		{ID: 3, Date: "2026-10-30", Text: "question: does BSD script take -c?"},
		{ID: 4, Date: "2026-10-30", Text: "answer: no, BSD script has no -c; ptyCommand handles it"},
		{ID: 5, Date: "2026-10-30", Text: "tests: go test ./... ok"},
		{ID: 6, Date: "2026-10-30", Text: "progress: the deploy uses token " + "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789"},
		{ID: 7, Date: "2026-10-30", Text: "progress: already remembered fact about the board"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(proj.WorkDir, "retros"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(proj.WorkDir, "retros", "20261030-120000-engine.md"),
		"# Workflow Retrospective\n\nIssue: engine\nTitle: Engine\nStatus: done\nSystem: Factory\nDate: 2026-10-30T12:00:00Z\n\nBase workflow: none\nTooling/workflow friction: rtk hides go test -v test names; use rtk proxy for a parser\n")
	mustWrite(t, filepath.Join(proj.WorkDir, "retros", "20261030-130000-seo-pass.md"),
		"# Workflow Retrospective\n\nIssue: seo-pass\nDate: 2026-10-30T13:00:00Z\n\nTooling/workflow friction: another issue's retro\n")
	return proj
}

// AC1: memory candidates lists the facts an issue's trail and retro
// could teach, with their sources; machine comments, secrets and what is
// already remembered stay out, and it writes nothing.
func TestMemoryCandidatesListsTrailAndRetro(t *testing.T) {
	memoryAs(t, "")
	proj := trailProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"add", "make shot URL takes one screenshot of a page", "--kind", "howto"}); err != nil {
		t.Fatal(err)
	}
	if err := runMemory(ctx, []string{"add", "the board fact", "--issue", "engine", "--comment", "7"}); err != nil {
		t.Fatal(err)
	}
	before := len(memSet(t, proj).All())
	ctx, out := goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"candidates", "engine"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{
		"== Memory candidates: engine ==",
		"comment 2 (progress)\n  make shot URL= takes one screenshot without a playtest session",
		"comment 3 (question)\n  does BSD script take -c?",
		"comment 4 (answer)\n  no, BSD script has no -c; ptyCommand handles it",
		"comment 6 (progress)\n  (looks like a GitHub token; not shown",
		"· D-1\n  The runner keeps TMUX_TMPDIR under /tmp",
		"retro 20261030-120000-engine.md · tooling/workflow friction\n  rtk hides go test -v test names",
		"(1 already remembered: --all lists them)",
		"issue-cli memory add \"<the fact, in your words>\" --issue engine [--comment N]",
	} {
		assertContains(t, s, want)
	}
	if !strings.Contains(s, "  ~ m-") {
		t.Errorf("the make shot candidate should show its similar memory:\n%s", s)
	}
	for _, not := range []string{"approval:", "tests:", "go test ./... ok", "ghp_", "another issue's retro", "board fact", "Base workflow"} {
		if strings.Contains(s, not) {
			t.Errorf("candidates show %q:\n%s", not, s)
		}
	}
	if after := len(memSet(t, proj).All()); after != before {
		t.Errorf("candidates wrote memories: %d → %d", before, after)
	}

	ctx, out = goalsCtx(t, proj, true)
	if err := runMemory(ctx, []string{"candidates", "engine", "--all"}); err != nil {
		t.Fatal(err)
	}
	var got []candidate
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	var remembered string
	for _, c := range got {
		if c.Comment == 7 {
			remembered = c.Remembered
		}
	}
	if len(got) != 7 || !strings.HasPrefix(remembered, "m-") {
		t.Errorf("--all --json: %d candidates, comment 7 remembered as %q: %+v", len(got), remembered, got)
	}
}

func TestMemoryCandidatesEmptyTrail(t *testing.T) {
	memoryAs(t, "")
	proj := goalsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"candidates", "seo-pass"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "No candidates on seo-pass")
	if err := runMemory(ctx, []string{"candidates"}); err == nil {
		t.Error("candidates without a slug should be a usage error")
	}
}

// AC2 through the CLI: the consolidation pack lists a cluster, the run
// merges it with add --supersedes id,id (the update step), the old ones
// stay in history as superseded and the log still has every memory.
func TestMemoryConsolidateMergesThroughTheUpdateStep(t *testing.T) {
	memoryAs(t, "")
	proj := goalsProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	for _, args := range [][]string{
		{"add", "make shot takes one screenshot of one page"},
		{"add", "make shot takes a screenshot of one page quickly", "--new"},
		{"add", "the viewer listens on loopback by default"},
	} {
		if err := runMemory(ctx, args); err != nil {
			t.Fatal(err)
		}
	}
	ctx, out := goalsCtx(t, proj, true)
	if err := runMemory(ctx, []string{"consolidate"}); err != nil {
		t.Fatal(err)
	}
	var pack memory.Pack
	if err := json.Unmarshal(out.Bytes(), &pack); err != nil {
		t.Fatal(err)
	}
	if len(pack.Clusters) != 1 || len(pack.Clusters[0].Members) != 2 {
		t.Fatalf("pack %+v, want one cluster of two", pack)
	}
	ids := pack.Clusters[0].IDs()

	ctx, out = goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"consolidate"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "Nothing here is written or deleted")
	assertContains(t, s, "--supersedes "+strings.Join(ids, ","))
	assertContains(t, s, "Promote to CLAUDE.md")

	ctx, out = goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"add", "make shot URL=<page> takes one screenshot of one page", "--supersedes", strings.Join(ids, ",")}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "supersedes "+strings.Join(ids, ", "))
	set := memSet(t, proj)
	if n := len(set.All()); n != 4 {
		t.Fatalf("%d memories, want 4: a merge deletes nothing", n)
	}
	for _, id := range ids {
		if m := set.Get(id); m == nil || m.State(ctx.now()) != "superseded" {
			t.Errorf("%s: %+v, want superseded", id, m)
		}
	}
	raw, err := os.ReadFile(memory.ProjectPath(proj.IssueDir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"op":"owner-delete"`) || strings.Contains(string(raw), `"op":"forget"`) {
		t.Errorf("the merge wrote a delete or forget:\n%s", raw)
	}
	ctx, out = goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"consolidate"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Near-duplicates (0)")
	if err := runMemory(ctx, []string{"add", "x y z words", "--supersedes", ","}); err == nil {
		t.Error("--supersedes with no id should be a usage error")
	}
}

// AC3 through the CLI: search ranks an old, unused memory below a recent,
// used one, and show prints the strength.
func TestMemorySearchDecaysOldUnusedMemories(t *testing.T) {
	memoryAs(t, "")
	proj := goalsProject(t)
	old := time.Date(2025, 9, 1, 12, 0, 0, 0, time.UTC)
	ctx, _ := goalsCtx(t, proj, false)
	ctx.Now = func() time.Time { return old }
	if err := runMemory(ctx, []string{"add", "the playtest runner finds chrome for testing itself"}); err != nil {
		t.Fatal(err)
	}
	ctx, _ = goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"add", "the playtest runner finds chrome for testing on its own", "--new"}); err != nil {
		t.Fatal(err)
	}
	all := memSet(t, proj).All()
	var oldID, newID string
	for _, m := range all {
		if strings.HasPrefix(m.Created, "2025") {
			oldID = m.ID
		} else {
			newID = m.ID
		}
	}
	if err := memory.ProjectStore(proj.IssueDir).RecordUse([]string{newID}, "agent-x", ctx.now()); err != nil {
		t.Fatal(err)
	}
	ctx, out := goalsCtx(t, proj, true)
	if err := runMemory(ctx, []string{"search", "playtest", "chrome"}); err != nil {
		t.Fatal(err)
	}
	var hits []memory.Hit
	if err := json.Unmarshal(out.Bytes(), &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ID != newID || hits[1].ID != oldID {
		t.Fatalf("hits %+v, want %s (recent, used) before %s (old, unused)", hits, newID, oldID)
	}
	ctx, out = goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"show", oldID}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "· strength 0.")
}
