package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/postmortem"
)

const pmAtSections = "## Steps\nBuilt it.\n\n## Worked\nnone\n\n## Did not work\nnone\n\n## Decisions\nnone\n\n## Known\nnone\n\n"

// AC1: an agreed memory action with At: shipping makes a memory that
// memory list --at shipping returns; a refused one makes nothing, and an
// agreed one that updates a near-duplicate ties the old memory instead.
func TestDecideMemoryActionAt(t *testing.T) {
	memoryAs(t, "")
	proj := newTestProject(t)
	old := memAt(t, proj, "Before the ff at shipping merge main once more and rerun make validate there.", "")
	body := pmAtSections + `## Proposed actions

- [ ] A1: Tie the release-notes lesson to shipping
  Differently: a fresh agent at shipping would write the Unreleased entry before the fast-forward
  Place: memory convention
  Text: At shipping write the CHANGELOG Unreleased entry before fast-forwarding main.
  At: Shipping
- [ ] A2: Tie the merge lesson to shipping
  Differently: a fresh agent at shipping would merge main once more before the ff
  Place: memory pitfall
  Text: Before the ff at shipping, merge main once more and rerun make validate there.
  At: shipping
- [ ] A3: A lesson the owner refuses
  Differently: a fresh agent at testing would run the slow tier by hand first
  Place: memory howto
  Text: Run make ux-test ALL=1 by hand at testing before asking for review.
  At: testing
`
	acts, err := postmortem.Check(body, proj.WorkDir, proj.AllStatuses())
	if err != nil {
		t.Fatal(err)
	}
	if acts[0].At != "shipping" {
		t.Fatalf("Check kept %q, want the workflow's spelling", acts[0].At)
	}
	retroPath := filepath.Join(proj.WorkDir, "retros", "pm.md")
	if err := os.MkdirAll(filepath.Dir(retroPath), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, retroPath, "# Post-mortem\n\nIssue: plain\nKind: postmortem\n\n"+body)
	env := postmortem.Env{
		Repo: proj.WorkDir, IssueDir: proj.IssueDir, Project: proj.Slug, Issue: "plain",
		RetroPath: retroPath, RetroRef: "retros/pm.md", Owner: "owner",
		Now: time.Now(), Statuses: proj.AllStatuses(),
	}
	outs, err := postmortem.Decide(env, []postmortem.Answer{{ID: "A1", Agree: true}, {ID: "A2", Agree: true}, {ID: "A3"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range outs[:2] {
		if o.Err != "" || !strings.Contains(o.Line, " at shipping") {
			t.Fatalf("%s: %+v", o.ID, o)
		}
	}
	ctx, out, _ := testContext(proj)
	if err := runMemory(ctx, []string{"list", "--at", "shipping"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"CHANGELOG Unreleased entry", old.ID} {
		if !strings.Contains(got, want) {
			t.Errorf("memory list --at shipping lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ux-test") {
		t.Errorf("a refused action made a memory:\n%s", got)
	}
	set, err := memory.ProjectStore(proj.IssueDir).Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range set.All() {
		if strings.Contains(m.Text, "ux-test") {
			t.Fatalf("the refused action was written: %+v", m)
		}
	}
}

// Decide checks At: again: a status the workflow dropped after the write
// fails that action and leaves it open.
func TestDecideRefusesAnAtTheWorkflowDropped(t *testing.T) {
	memoryAs(t, "")
	proj := newTestProject(t)
	retroPath := filepath.Join(proj.WorkDir, "pm.md")
	mustWrite(t, retroPath, pmAtSections+"## Proposed actions\n\n- [ ] A1: Tie it to review\n  Differently: a fresh agent at review would read the diff first\n  Place: memory howto\n  Text: At review read the whole diff before the description.\n  At: review\n")
	env := postmortem.Env{Repo: proj.WorkDir, IssueDir: proj.IssueDir, Issue: "plain", RetroPath: retroPath, Owner: "owner", Statuses: proj.AllStatuses()}
	outs, err := postmortem.Decide(env, []postmortem.Answer{{ID: "A1", Agree: true}})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].Err == "" || !strings.Contains(outs[0].Err, "not a status") || !strings.Contains(outs[0].Err, "shipping") {
		t.Fatalf("A1: %+v", outs[0])
	}
	if all := memSet(t, proj).All(); len(all) != 0 {
		t.Fatalf("written anyway: %+v", all)
	}
}
