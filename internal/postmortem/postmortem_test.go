package postmortem

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/retro"
)

const sections = `## Steps
Designed, built, tested.

## Worked
The stand-in agent.

## Did not work
none

## Decisions
D-1 kept.

## Known
none

`

func good() string {
	return sections + `## Proposed actions

- [ ] A1: Run playtest-check after renaming a data-testid
  Differently: a fresh agent renaming a testid would run make playtest-check before testing
  Place: memory pitfall
  Text: Renaming a data-testid breaks playtest sessions that name it; run make playtest-check.
- [ ] A2: Say where tmux sockets go in CLAUDE.md
  Differently: a fresh agent would keep TMUX_TMPDIR under /tmp in tests instead of t.TempDir
  Place: file CLAUDE.md
  Under: Validation
  Text: Tests keep TMUX_TMPDIR under /tmp (isolateTmux).
- [ ] A3: Try one clarify round in design
  Differently: a fresh agent would ask every design question at once instead of one per comment
  Place: workflow
  Text: Add a clarify round to in design.
- [ ] A4: The worktree rule
  Differently: a fresh agent would still stay in its worktree, the docs say it already
  Place: none docs/rules.md#worktree
`
}

func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs", "rules.md"), []byte("# Rules\n"), 0o644)
	return dir
}

func TestAGoodPostmortemPasses(t *testing.T) {
	acts, err := Check(good(), repo(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 4 {
		t.Fatalf("got %d actions", len(acts))
	}
	if acts[1].Place != PlaceFile || acts[1].Arg != "CLAUDE.md" || acts[1].Under != "Validation" {
		t.Fatalf("A2 parsed as %+v", acts[1])
	}
	if acts[0].Arg != "pitfall" || !strings.Contains(acts[0].Text, "playtest-check") {
		t.Fatalf("A1 parsed as %+v", acts[0])
	}
}

// AC2: an action without the "act differently" test or a place is refused.
func TestActionsWithoutTheTestOrAPlaceAreRefused(t *testing.T) {
	root := repo(t)
	cases := map[string]struct{ item, want string }{
		"no differently": {"- [ ] A1: Do a thing\n  Place: memory fact\n  Text: a fact here\n", "no Differently: line"},
		"non-answer":     {"- [ ] A1: Do a thing\n  Differently: n/a\n  Place: memory fact\n  Text: a fact here\n", "doesn't say what a fresh agent would do differently"},
		"too short":      {"- [ ] A1: Do a thing\n  Differently: be better\n  Place: memory fact\n  Text: a fact here\n", "doesn't say"},
		"echo":           {"- [ ] A1: Run the tests before pushing\n  Differently: run the tests before pushing.\n  Place: memory fact\n  Text: x y\n", "doesn't say"},
		"no place":       {"- [ ] A1: Do a thing\n  Differently: a fresh agent would run the check first\n  Text: a fact here\n", "no Place: line"},
		"unknown place":  {"- [ ] A1: Do a thing\n  Differently: a fresh agent would run the check first\n  Place: slack\n  Text: x\n", "is not a place"},
		"two places":     {"- [ ] A1: Do a thing\n  Differently: a fresh agent would run the check first\n  Place: file CLAUDE.md | memory fact\n  Text: x\n", "more than one place"},
		"no kind":        {"- [ ] A1: Do a thing\n  Differently: a fresh agent would run the check first\n  Place: memory\n  Text: x\n", "needs a kind"},
		"bad kind":       {"- [ ] A1: Do a thing\n  Differently: a fresh agent would run the check first\n  Place: memory lesson\n  Text: x\n", "no kind"},
		"no text":        {"- [ ] A1: Do a thing\n  Differently: a fresh agent would run the check first\n  Place: file CLAUDE.md\n", "no Text: line"},
		"escape":         {"- [ ] A1: Do a thing\n  Differently: a fresh agent would run the check first\n  Place: file ../other/CLAUDE.md\n  Text: x\n", "leaves the repo"},
		"absolute":       {"- [ ] A1: Do a thing\n  Differently: a fresh agent would run the check first\n  Place: file /etc/motd.md\n  Text: x\n", "absolute"},
		"code file":      {"- [ ] A1: Do a thing\n  Differently: a fresh agent would run the check first\n  Place: file main.go\n  Text: x\n", "not a markdown file"},
		"issues dir":     {"- [ ] A1: Do a thing\n  Differently: a fresh agent would run the check first\n  Place: file issues/x.md\n  Text: x\n", "under issues/"},
		"none no ref":    {"- [ ] A1: Do a thing\n  Differently: a fresh agent would run the check first\n  Place: none\n", "needs the doc"},
		"none missing":   {"- [ ] A1: Do a thing\n  Differently: a fresh agent would run the check first\n  Place: none docs/gone.md#x\n", "isn't in the repo"},
		"workflow arg":   {"- [ ] A1: Do a thing\n  Differently: a fresh agent would run the check first\n  Place: workflow workflow.yaml\n  Text: x\n", "takes no argument"},
		"no id":          {"- Do a thing\n  Differently: a fresh agent would run the check first\n  Place: memory fact\n  Text: x\n", "not an action item"},
		"secret":         {"- [ ] A1: Do a thing\n  Differently: a fresh agent would run the check first\n  Place: memory fact\n  Text: the key is sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n", "secret"},
		"dup id":         {"- [ ] A1: Do a thing\n  Differently: a fresh agent would run the check first\n  Place: workflow\n  Text: x\n- [ ] A1: Again\n  Differently: a fresh agent would run the other check\n  Place: workflow\n  Text: y\n", "used twice"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Check(sections+"## Proposed actions\n\n"+c.item, root, nil)
			var re *RefusedError
			if !errors.As(err, &re) {
				t.Fatalf("not refused: %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("refusal doesn't say %q:\n%s", c.want, err)
			}
			if !strings.Contains(err.Error(), "Differently:") || !strings.Contains(err.Error(), TheTest) {
				t.Fatalf("refusal doesn't show the shape:\n%s", err)
			}
		})
	}
}

func TestAMissingSectionIsRefused(t *testing.T) {
	body := strings.Replace(good(), "## Known\nnone\n", "", 1)
	_, err := Check(body, repo(t), nil)
	if err == nil || !strings.Contains(err.Error(), "## Known") {
		t.Fatalf("want the Known section named, got %v", err)
	}
}

func TestMarkTicksAndRecords(t *testing.T) {
	out, err := Mark(good(), "A2", true, "commit abc123 (CLAUDE.md)")
	if err != nil {
		t.Fatal(err)
	}
	out, err = Mark(out, "a1", false, "2026-10-31 (owner): not true")
	if err != nil {
		t.Fatal(err)
	}
	acts := Actions(out)
	if !acts[1].Done || acts[1].Applied != "commit abc123 (CLAUDE.md)" {
		t.Fatalf("A2: %+v", acts[1])
	}
	if acts[0].Done || acts[0].Refused != "2026-10-31 (owner): not true" {
		t.Fatalf("A1: %+v", acts[0])
	}
	if len(Pending(acts)) != 2 {
		t.Fatalf("pending: %+v", Pending(acts))
	}
	if _, err := Mark(out, "A1", true, "x"); err == nil {
		t.Fatal("marking a decided action twice passed")
	}
	// The outcome lines are fields, not refusals: a marked file still passes.
	if _, err := Check(out, repo(t), nil); err != nil {
		t.Fatalf("a marked post-mortem no longer passes: %v", err)
	}
}

func TestSplice(t *testing.T) {
	in := "# T\n\n## Validation\n\nRun make validate.\n\n## Shell\n\nzsh.\n"
	got, err := Splice(in, "Validation", "Keep TMUX_TMPDIR under /tmp.")
	if err != nil {
		t.Fatal(err)
	}
	want := "# T\n\n## Validation\n\nRun make validate.\n\nKeep TMUX_TMPDIR under /tmp.\n\n## Shell\n\nzsh.\n"
	if got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
	got, _ = Splice(in, "", "End.")
	if !strings.HasSuffix(got, "zsh.\n\nEnd.\n") {
		t.Fatalf("append: %q", got)
	}
	if _, err := Splice(in, "Gone", "x"); err == nil {
		t.Fatal("a missing heading passed")
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := repo(t)
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# Project\n\n## Validation\n\nRun make validate.\n"), 0o644)
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "T")
	run("config", "commit.gpgsign", "false")
	run("config", "core.hooksPath", "/dev/null")
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return dir
}

// AC3/AC4 (the apply half): agreed actions are applied — a memory, a
// one-file commit, an experiment proposal — refused ones are recorded, and
// every outcome is written back into the retro.
func TestDecideAppliesAgreedAndRecordsRefused(t *testing.T) {
	dir := gitRepo(t)
	issues := filepath.Join(dir, "tracker")
	os.MkdirAll(filepath.Join(dir, "retros"), 0o755)
	retroPath := filepath.Join(dir, "retros", "pm.md")
	os.WriteFile(retroPath, []byte("# Post-mortem\n\nIssue: ui/x\nKind: postmortem\n\n"+good()), 0o644)
	var filed []Action
	env := Env{
		Repo: dir, IssueDir: issues, Project: "demo", Issue: "ui/x",
		RetroPath: retroPath, RetroRef: "retros/pm.md", Owner: "owner",
		Now: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC),
		NewExperiment: func(a Action) (string, error) {
			filed = append(filed, a)
			return "workflow/try-one-clarify-round", nil
		},
	}
	if _, err := Decide(env, []Answer{{ID: "A1", Agree: true}}); err == nil || !strings.Contains(err.Error(), "A2") {
		t.Fatalf("a partial round passed: %v", err)
	}
	outs, err := Decide(env, []Answer{
		{ID: "A1", Agree: true}, {ID: "A2", Agree: true}, {ID: "A3", Agree: true}, {ID: "A4", Agree: false, Note: "keep it"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range outs {
		if o.Err != "" {
			t.Fatalf("%s failed: %s", o.ID, o.Err)
		}
	}
	set, err := memory.ProjectStore(issues).Load()
	if err != nil {
		t.Fatal(err)
	}
	mems := set.Active(env.Now)
	if len(mems) != 1 || mems[0].Kind != "pitfall" || mems[0].Source.Retro != "retros/pm.md" || mems[0].Source.Issue != "ui/x" {
		t.Fatalf("memory: %+v", mems)
	}
	claude, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if !strings.Contains(string(claude), "Run make validate.\n\nTests keep TMUX_TMPDIR under /tmp (isolateTmux).\n") {
		t.Fatalf("CLAUDE.md: %q", claude)
	}
	log, _ := exec.Command("git", "-C", dir, "log", "-1", "--format=%s%n%b").Output()
	if !strings.Contains(string(log), "docs(postmortem): Say where tmux sockets go") || !strings.Contains(string(log), "Refs: ui/x") {
		t.Fatalf("commit: %s", log)
	}
	sha, _ := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	if len(filed) != 1 || filed[0].ID != "A3" {
		t.Fatalf("experiment: %+v", filed)
	}
	raw, _ := os.ReadFile(retroPath)
	acts := Actions(string(raw))
	wantApplied := map[string]string{
		"A1": "memory " + mems[0].ID,
		"A2": "commit " + strings.TrimSpace(string(sha)) + " (CLAUDE.md)",
		"A3": "experiment workflow/try-one-clarify-round",
	}
	for _, a := range acts {
		if w, ok := wantApplied[a.ID]; ok && (!a.Done || !strings.HasPrefix(a.Applied, w)) {
			t.Fatalf("%s: applied %q, want %q (done %v)", a.ID, a.Applied, w, a.Done)
		}
	}
	if acts[3].Done || acts[3].Refused != "2026-10-31 (owner): keep it" {
		t.Fatalf("A4: %+v", acts[3])
	}
	if _, err := Decide(env, nil); !errors.Is(err, ErrDecided) {
		t.Fatalf("a second round: %v", err)
	}
}

func TestAFailedApplyStaysOpen(t *testing.T) {
	dir := gitRepo(t)
	retroPath := filepath.Join(dir, "pm.md")
	body := sections + "## Proposed actions\n\n- [ ] A1: Add under a gone heading\n  Differently: a fresh agent would read the gone section first\n  Place: file CLAUDE.md\n  Under: Gone\n  Text: x\n- [ ] A2: Refuse\n  Differently: a fresh agent would do the other thing instead\n  Place: workflow\n  Text: y\n"
	os.WriteFile(retroPath, []byte(body), 0o644)
	env := Env{Repo: dir, IssueDir: filepath.Join(dir, "tracker"), Issue: "ui/x", RetroPath: retroPath, Owner: "owner"}
	outs, err := Decide(env, []Answer{{ID: "A1", Agree: true}, {ID: "A2"}})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].Err == "" || !strings.Contains(outs[0].Err, "Gone") {
		t.Fatalf("A1: %+v", outs[0])
	}
	raw, _ := os.ReadFile(retroPath)
	pend := Pending(Actions(string(raw)))
	if len(pend) != 1 || pend[0].ID != "A1" {
		t.Fatalf("pending: %+v", pend)
	}
}

// Waits is WaitingIn's rule over loaded retros: only an issue's newest
// post-mortem, only with actions open, wherever it sits.
func TestWaitsNewestOfAnIssueWithOpenActions(t *testing.T) {
	open := "## Proposed actions\n\n- [ ] A1: Do it\n  Differently: a fresh agent does it this other way\n  Place: none docs/x.md\n"
	entry := func(name, slug, body string) *retro.Entry {
		return &retro.Entry{FileName: name, FilePath: "/r/" + name, IssueSlug: slug, Kind: Kind, Body: body}
	}
	got := Waits([]*retro.Entry{
		entry("20261101-000000-postmortem-a.md", "a", open), // a's newest answered it
		entry("20261102-000000-postmortem-a.md", "a", "## Proposed actions\n\nNone.\n"),
		entry("20261101-000000-postmortem-b.md", "b", open),
		entry("20261101-000000-postmortem-none.md", "", open),
		{FileName: "20261101-000000-c.md", FilePath: "/r/c", IssueSlug: "c", Body: open},
	})
	if len(got) != 1 || got["/r/20261101-000000-postmortem-b.md"] != 1 {
		t.Errorf("waits %v", got)
	}
}

// AC4: an epic's brief lists each child's issue file, branch, retro and
// post-mortem as paths, and a plain issue's brief has no Children part.
func TestBriefListsEpicChildren(t *testing.T) {
	r := Run{Slug: "x/epic", Title: "Epic", IssueFile: "/b/issues/X/epic.md", Children: []Child{
		{Slug: "x/one", IssueFile: "/b/issues/X/one.md", Branch: "work/x/one", Retros: []string{"/b/retros/1-x-one.md"}, Postmortems: []string{"/b/retros/2-x-one.md"}},
		{Slug: "x/two", IssueFile: "/b/issues/X/two.md", Branch: "work/x/two"},
	}}
	got := Brief(r)
	for _, want := range []string{
		"### Children",
		"- x/one: issue /b/issues/X/one.md · branch work/x/one · retro /b/retros/1-x-one.md · post-mortem /b/retros/2-x-one.md\n",
		"- x/two: issue /b/issues/X/two.md · branch work/x/two\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("brief lacks %q:\n%s", want, got)
		}
	}
	r.Children = nil
	if strings.Contains(Brief(r), "### Children") {
		t.Error("a plain issue's brief has a Children part")
	}
}

var atStatuses = []string{"idea", "in progress", "testing", "shipping", "done"}

// AC2: At: naming a status the workflow lacks, or At: on a place other
// than memory, is a refusal that names the statuses.
func TestCheckRefusesUnknownAt(t *testing.T) {
	item := func(place, at string) string {
		return sections + "## Proposed actions\n\n- [ ] A1: Tie it to a step\n  Differently: a fresh agent would merge main again before the ff\n  Place: " + place + "\n  Text: Merge main again before the ff.\n  At: " + at + "\n"
	}
	root := repo(t)
	_, err := Check(item("memory pitfall", "shiping"), root, atStatuses)
	if err == nil || !strings.Contains(err.Error(), `At: "shiping" is not a status`) || !strings.Contains(err.Error(), "in progress, testing, shipping") {
		t.Fatalf("an unknown status: %v", err)
	}
	if !strings.Contains(err.Error(), "  At: <optional, memory only") {
		t.Errorf("the refusal's shape lacks At:\n%s", err)
	}
	if _, err := Check(item("file CLAUDE.md", "shipping"), root, atStatuses); err == nil || !strings.Contains(err.Error(), "for Place: memory only") {
		t.Fatalf("At: on a file action: %v", err)
	}
	if _, err := Check(item("memory pitfall", "shipping"), root, nil); err == nil || !strings.Contains(err.Error(), "statuses aren't known") {
		t.Fatalf("no statuses to check against: %v", err)
	}
	acts, err := Check(item("memory pitfall", "Shipping"), root, atStatuses)
	if err != nil {
		t.Fatal(err)
	}
	if acts[0].At != "shipping" || strings.Contains(acts[0].Text, "At:") {
		t.Fatalf("parsed as %+v", acts[0])
	}
}

// AC3: the brief's places list names At: on the memory place, with the
// workflow's statuses.
func TestBriefListsAt(t *testing.T) {
	got := Brief(Run{Slug: "x/one", Title: "One", Statuses: atStatuses})
	want := "- memory <kind>: a shared memory (fact, convention, pitfall, howto, who); At: <status> when it matters at one step, so transition shows it to whoever enters that status (idea, in progress, testing, shipping, done)\n"
	if !strings.Contains(got, want) {
		t.Errorf("brief lacks %q:\n%s", want, got)
	}
	if !strings.Contains(got, "  At: <optional, memory only") {
		t.Errorf("the brief's shape lacks At:\n%s", got)
	}
}
