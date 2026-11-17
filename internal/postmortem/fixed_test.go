package postmortem

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/memory"
)

// AC1: two file actions with the same words join only when they edit the
// same file.
func TestGroupSameTargetOnly(t *testing.T) {
	a := item("a", PlaceFile, "Say where tmux sockets go in the validation notes")
	b := item("b", PlaceFile, "Say where tmux sockets go in the validation notes")
	c := item("c", PlaceFile, "Say where tmux sockets go in the validation notes")
	a.Arg, b.Arg, c.Arg = "CLAUDE.md", "./CLAUDE.md", "docs/workflow.md"
	a.File, b.File, c.File = "a.md", "b.md", "c.md"
	gs := GroupItems([]Item{a, b, c})[0].Groups
	if len(gs) != 2 || len(gs[0].Items) != 2 || gs[0].Items[1].Issue != "b" || !gs[1].Alone || gs[1].Items[0].Issue != "c" {
		t.Fatalf("groups: %+v", gs)
	}
	keys := GroupKeys([]Item{a, b, c})
	if keys[a.Key()] != keys[b.Key()] || keys[c.Key()] == keys[a.Key()] {
		t.Fatalf("keys: %v", keys)
	}
}

// AC3: a Fixed: line is written under the action, read back, replaced by a
// second link, and refused on a decided action.
func TestLinkFixWritesOneLine(t *testing.T) {
	dir := t.TempDir()
	path := saved(t, dir, "cart", queueDay)
	if err := LinkFix(path, "a2", "ui/the-fix"); err != nil {
		t.Fatal(err)
	}
	if err := LinkFix(path, "A2", "ui/another-fix"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if n := strings.Count(string(raw), "Fixed: "); n != 1 {
		t.Fatalf("%d Fixed: lines:\n%s", n, raw)
	}
	acts := Actions(string(raw))
	if acts[1].Fixed != "ui/another-fix" || acts[1].Decided() || acts[1].Under != "Validation" || acts[1].Text == "" {
		t.Fatalf("A2: %+v", acts[1])
	}
	if _, err := Refuse(path, "A3", "owner", "", queueDay); err != nil {
		t.Fatal(err)
	}
	if err := LinkFix(path, "A3", "x"); err == nil || !strings.Contains(err.Error(), "already decided") {
		t.Fatalf("a link on a decided action: %v", err)
	}
	for _, bad := range []string{"", "two words"} {
		if err := LinkFix(path, "A1", bad); err == nil {
			t.Errorf("LinkFix(%q) passed", bad)
		}
	}
}

// AC3: a linked issue fixes the action once it is done, a commit once it is
// on main, a memory action once a memory has its very words (a near one is
// an update, which still waits); an open issue is a fix
// underway and the action still waits.
func TestFixesSplitTheQueue(t *testing.T) {
	dir := t.TempDir()
	repoDir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repoDir, "-c", "user.email=t@example.com", "-c", "user.name=T", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "the fix")
	sha := git("rev-parse", "HEAD")

	cart := saved(t, dir, "cart", queueDay)
	till := saved(t, dir, "till", queueDay.Add(1))
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(LinkFix(cart, "A2", "ui/done-fix"))
	must(LinkFix(cart, "A3", sha[:9]))
	must(LinkFix(till, "A2", "ui/open-fix"))
	must(LinkFix(till, "A3", "ui/nowhere"))
	f := &Fixes{
		Repo: repoDir, MainOf: func() string { return "main" },
		IssueState: func(slug string) (string, bool, bool) {
			switch slug {
			case "ui/done-fix":
				return "done", true, true
			case "ui/open-fix":
				return "in progress", false, true
			}
			return "", false, false
		},
		Memories: []memory.Memory{{ID: "m-abc123", Text: "renaming a  data-testid breaks playtest sessions that name it: run make playtest-check"}, {ID: "m-near01", Text: "Renaming a data-testid breaks the playtest sessions that name it; run make playtest-check twice."}},
	}
	open, fixed := f.Split(Queue(dir))
	words := map[string]string{}
	for _, it := range append(append([]Item{}, open...), fixed...) {
		words[it.Issue+"/"+it.ID] = it.Fix.Words()
	}
	want := map[string]string{
		"cart/A1": "known as m-abc123", "till/A1": "known as m-abc123",
		"cart/A2": "fixed by ui/done-fix", "cart/A3": "fixed by " + sha[:7],
		"till/A2": "fix underway: ui/open-fix (in progress)", "till/A3": "fix underway: ui/nowhere",
		"cart/A4": "", "till/A4": "",
	}
	for k, w := range want {
		if words[k] != w {
			t.Errorf("%s: %q, want %q", k, words[k], w)
		}
	}
	if len(fixed) != 4 || len(open) != 4 {
		t.Fatalf("open %d, fixed %d", len(open), len(fixed))
	}
	if w := WaitingOf(open); len(w) != 2 || w[0].Issue != "till" || w[0].Pending != 3 || w[1].Pending != 1 {
		t.Fatalf("WaitingOf: %+v", w)
	}
	// Nothing was written: the retro still has every action open.
	raw, _ := os.ReadFile(cart)
	if len(Pending(Actions(string(raw)))) != 4 {
		t.Fatal("a fix wrote an outcome")
	}
}

// AC2: AgreeOne applies one action and ticks it with the owner's words;
// AgreeAs ticks a copy as that one, applying nothing.
func TestAgreeOneThenItsCopies(t *testing.T) {
	dir := t.TempDir()
	issueDir := t.TempDir()
	cart := saved(t, dir, "cart", queueDay)
	till := saved(t, dir, "till", queueDay)
	env := Env{IssueDir: issueDir, Project: "acme", Issue: "cart", RetroPath: cart, RetroRef: "retros/x.md", Owner: "owner", Now: queueDay}
	made, line, err := AgreeOne(env, "a1", "one for all")
	if err != nil || !strings.HasPrefix(made, "memory m-") || line != made+" · agreed 2026-11-05 (owner): one for all" {
		t.Fatalf("AgreeOne: %q %q %v", made, line, err)
	}
	copyLine, err := AgreeAs(till, "A1", "x.md#A1", made, "owner", "one for all", queueDay)
	if err != nil || copyLine != "as x.md#A1: "+made+" · agreed 2026-11-05 (owner): one for all" {
		t.Fatalf("AgreeAs: %q %v", copyLine, err)
	}
	set, _ := memory.ProjectStore(issueDir).Load()
	if n := len(set.Active(queueDay)); n != 1 {
		t.Fatalf("%d memories, want the one", n)
	}
	for _, p := range []string{cart, till} {
		raw, _ := os.ReadFile(p)
		a := Actions(string(raw))[0]
		if !a.Done || !strings.Contains(a.Applied, "one for all") {
			t.Errorf("%s A1: %+v", p, a)
		}
	}
	if _, _, err := AgreeOne(env, "A1", ""); err == nil || !strings.Contains(err.Error(), "not an open action") {
		t.Fatalf("a second agree: %v", err)
	}
}
