package postmortem

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/retro"
)

var queueDay = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

// saved writes a post-mortem of issue with good()'s actions under dir.
func saved(t *testing.T, dir, issue string, at time.Time) string {
	t.Helper()
	p, err := Save(dir, Meta{Issue: issue, Title: strings.ToUpper(issue), Status: "done"}, good(), at)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func archived(t *testing.T, path string) string {
	t.Helper()
	to := filepath.Join(filepath.Dir(path), retro.ArchiveDir, filepath.Base(path))
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, to); err != nil {
		t.Fatal(err)
	}
	return to
}

// AC1: a post-mortem moved to archive/ still waits, by the rule a top-level
// one waits by: the newest of its issue by name, wherever the file sits.
func TestWaitingInReadsTheArchive(t *testing.T) {
	dir := t.TempDir()
	saved(t, dir, "top", queueDay)
	moved := archived(t, saved(t, dir, "moved", queueDay.Add(time.Minute)))

	// The older one of "again" sits at the top; the newest is archived and
	// fully answered, so nothing of that issue waits.
	saved(t, dir, "again", queueDay.Add(2*time.Minute))
	newest := saved(t, dir, "again", queueDay.Add(3*time.Minute))
	for _, id := range []string{"A1", "A2", "A3", "A4"} {
		if _, err := Refuse(newest, id, "owner", "", queueDay); err != nil {
			t.Fatal(err)
		}
	}
	archived(t, newest)

	got := map[string]Waiting{}
	for _, w := range WaitingIn(dir) {
		got[w.Issue] = w
	}
	if len(got) != 2 || got["top"].Pending != 4 || got["moved"].Pending != 4 || got["moved"].File != moved {
		t.Fatalf("WaitingIn: %+v", got)
	}
	entries, err := retro.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if waits := Waits(entries); len(waits) != len(got) {
		t.Fatalf("Waits and WaitingIn disagree: %v against %+v", waits, got)
	}
	if q := Queue(dir); len(q) != 8 {
		t.Fatalf("Queue: %d items, want the 8 open ones", len(q))
	}
}

func item(issue, place, title string) Item {
	return Item{Action: Action{ID: "A1", Place: place, Title: title}, Issue: issue}
}

// Real titles from the queue of 2026-11-05: the five branch_on_main
// copies read as one, and a neighbour on the same words doesn't join.
func TestGroupJoinsCopies(t *testing.T) {
	copies := [][3]string{
		{"Add a done gate that the branch head is on main.", "a worker whose fast-forward failed would be refused at done with the reason, and could not close an issue whose work never reached main, whatever box it ticked.", "Add a validate rule (say `branch_on_main`) to shipping → done: the work branch's head is an ancestor of the local main, refused with both shas. Issues with no branch or no commits pass (report-only types). Then have the transition's closing \"Local CI tests main at <sha>\" line print only after that holds. Measure on the benchmark that no shipped task is refused wrongly."},
		{"Adopt the branch_on_main gate on shipping → done, already filed as an experiment.", "a worker whose fast-forward was refused could not reach done, so it would stay in shipping and retry, and the lead's tick would not close it mid-repair", "Run workflow/experiment-shipping-to-done-refuses-unless-main-holds-the-work-branch-branch-on-main as filed: shipping → done refuses unless work/<slug>'s head is an ancestor of local main. Evidence to add: on 2026-11-04 two issues reached done in the same minute with a refused fast-forward, both workers were closed mid-repair, and the lead reported both as shipped. Measure: done issues whose branch is not on main."},
		{"Try a rule on shipping → done that refuses while the issue's branch head is not an ancestor of main.", "A worker whose fast-forward was refused can't reach done on ticked boxes; transition names the missing merge, so a chained ff-and-check can't close unshipped work.", "Add a validate rule to shipping → done (name it branch_on_main) that passes only when the work branch head is an ancestor of the local integration branch. The shipping prompt's dry run comes before the ff, so the dry run must report this rule as not yet met without failing its \"references the issue\" line, or the dry run moves after step 4. Measure: done issues whose merge_sha is not on main, and the refusals at the rule."},
		{"Gate shipping → done on main holding the branch's own commits.", "a worker whose ff-only failed, or who ticked S4 early, gets refused at done and fast-forwards main, instead of closing an issue whose work is only on its branch.", "Add a validate rule (e.g. `branch_on_main`) to the shipping → done edge of workflow.yaml and the development template. It passes when `git rev-list --no-merges main..work/<slug>`, bounded below by the dispatch base, is empty, and its refusal names the ff-only command. Measure on the benchmark how many done-but-unmerged closes it prevents (three today)."},
		{"Refuse shipping → done unless the branch head is an ancestor of local main", "a worker that ticked \"fast-forwarded\" without doing it is refused at done and fast-forwards instead of closing an issue whose fix never reached main", "add a validate rule on the shipping → done transition (e.g. `branch_on_main`: `git merge-base --is-ancestor work/<slug> main` in the main checkout; skipped for types with no commits such as report) and refuse with \"work/<slug> is not on local main: merge main, validate, then git -C <main checkout> merge --ff-only work/<slug>\""},
	}
	var items []Item
	for i, c := range copies {
		it := item(string(rune('a'+i)), PlaceWorkflow, c[0])
		it.Differently, it.Text = c[1], c[2]
		items = append(items, it)
	}
	same := items[3]
	same.Issue, same.Place = "y", PlaceMemory // the same words in another place
	again := items[0]                         // the same issue: not a copy of itself
	items = append(items,
		item("x", PlaceWorkflow, "Make the report type's box say what happens to follow-ups that wait on the owner."),
		same, again,
	)
	places := GroupItems(items)
	if len(places) != 2 || places[0].Place != PlaceWorkflow || places[0].Count != 7 {
		t.Fatalf("places: %+v", places)
	}
	gs := places[0].Groups
	if len(gs) != 2 || gs[0].Alone || !gs[1].Alone {
		t.Fatalf("groups: %+v", gs)
	}
	if len(gs[0].Items) != 6 || gs[0].Items[0].Issue != "a" {
		t.Errorf("the copies: %+v", gs[0].Items)
	}
	if len(gs[1].Items) != 1 || gs[1].Items[0].Issue != "x" {
		t.Errorf("the rest: %+v", gs[1].Items)
	}
	if gs := places[1].Groups; len(gs) != 1 || !gs[0].Alone {
		t.Errorf("another place joined the copies: %+v", gs)
	}
}

// Copies are the actions' own words: an issue's title says nothing.
func TestGroupReadsTheActionNotItsIssue(t *testing.T) {
	a := item("a", PlaceMemory, "Remember the coupon rounding rule")
	b := item("b", PlaceMemory, "Remember the coupon rounding rule")
	c := item("c", PlaceMemory, "Record the receipt printer stub")
	a.IssueTitle, b.IssueTitle, c.IssueTitle = "Cart", "Till", "Cart"
	gs := GroupItems([]Item{a, b, c})[0].Groups
	if len(gs) != 2 || len(gs[0].Items) != 2 || gs[0].Items[1].Issue != "b" || !gs[1].Alone {
		t.Fatalf("groups: %+v", gs)
	}
}

// AC2: refusing part of a post-mortem leaves the rest open, for the page's
// one round to answer.
func TestRefuseLeavesTheRestOpen(t *testing.T) {
	dir := t.TempDir()
	path := saved(t, dir, "cart", queueDay)
	line, err := Refuse(path, "a2", "owner", "  a  duplicate ", queueDay)
	if err != nil || line != "2026-11-05 (owner): a duplicate" {
		t.Fatalf("Refuse: %q %v", line, err)
	}
	if _, err := Refuse(path, "A2", "owner", "", queueDay); err == nil || !strings.Contains(err.Error(), "already decided") {
		t.Fatalf("second refuse: %v", err)
	}
	raw, _ := os.ReadFile(path)
	acts := Actions(string(raw))
	if len(Pending(acts)) != 3 || acts[1].Refused != line || acts[1].Done {
		t.Fatalf("actions: %+v", acts)
	}
	got, err := Decide(Env{RetroPath: path, Owner: "owner", Now: queueDay}, []Answer{{ID: "A1"}, {ID: "A3"}, {ID: "A4"}})
	if err != nil || len(got) != 3 {
		t.Fatalf("the round after a partial refuse: %+v %v", got, err)
	}
}

// AC3: an overtaken action stops waiting everywhere Pending is read, and
// the box stays open: nothing was applied.
func TestOvertakeStopsItWaiting(t *testing.T) {
	dir := t.TempDir()
	path := saved(t, dir, "cart", queueDay)
	line, err := Overtake(path, "A1", "8a2b70ed", "owner", "fixed the cause", queueDay)
	if err != nil || line != "8a2b70ed · 2026-11-05 (owner): fixed the cause" {
		t.Fatalf("Overtake: %q %v", line, err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "  Overtaken: "+line+"\n") {
		t.Fatalf("no line in the retro:\n%s", raw)
	}
	acts := Actions(string(raw))
	if acts[0].Overtaken != line || !acts[0].Decided() || acts[0].Done || acts[0].Place != PlaceMemory {
		t.Fatalf("A1: %+v", acts[0])
	}
	if w := WaitingIn(dir); len(w) != 1 || w[0].Pending != 3 {
		t.Fatalf("WaitingIn: %+v", w)
	}
	for _, it := range Queue(dir) {
		if it.ID == "A1" {
			t.Fatalf("A1 is still queued: %+v", it)
		}
	}
	if _, err := Decide(Env{RetroPath: path, Owner: "owner"}, []Answer{{ID: "A1"}}); err == nil || !strings.Contains(err.Error(), "not an open action") {
		t.Fatalf("a round on an overtaken action: %v", err)
	}
}

func TestOvertakenShaIsCheckedOnMain(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "maintenance.auto=false", "-c", "gc.auto=0", "-c", "user.email=t@example.com", "-c", "user.name=T", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "on main")
	onMain := git("rev-parse", "HEAD")
	git("checkout", "-q", "-b", "side")
	git("commit", "-q", "--allow-empty", "-m", "off main")
	offMain := git("rev-parse", "HEAD")

	if got, err := OnMain(dir, "main", onMain[:10]); err != nil || got != gitdir.ShortSHA(onMain) {
		t.Fatalf("a commit on main: %q %v", got, err)
	}
	for _, sha := range []string{offMain, "deadbeef", "abc", "", "--help", "HEAD", "main", onMain + "^"} {
		if got, err := OnMain(dir, "main", sha); err == nil {
			t.Errorf("OnMain(%q) passed: %q", sha, got)
		}
	}
	if _, err := OnMain(dir, "--output=x", onMain); err == nil {
		t.Error("a branch name that reads as an option passed")
	}
}

// AC3: the undo is in the file, with the time and the line it undid; the
// action counts as open again, and a second mark and undo add to the record.
func TestReopenKeepsTheHistory(t *testing.T) {
	path := saved(t, t.TempDir(), "cart", queueDay)
	if _, err := Refuse(path, "A1", "owner", "dup", queueDay); err != nil {
		t.Fatal(err)
	}
	at := queueDay.Add(90 * time.Minute)
	line, err := Reopen(path, "a1", "owner", at)
	if err != nil {
		t.Fatal(err)
	}
	want := "2026-11-05T10:30:00Z (owner), was Refused: 2026-11-05 (owner): dup"
	if line != want {
		t.Fatalf("line: %q", line)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "\n  Reopened: "+want+"\n") || strings.Contains(string(raw), "\n  Refused:") {
		t.Fatalf("the file:\n%s", raw)
	}
	acts := Actions(string(raw))
	if acts[0].Decided() || acts[0].Refused != "" || len(acts[0].Reopened) != 1 {
		t.Fatalf("A1: %+v", acts[0])
	}
	if strings.Contains(acts[0].Text, "Reopened") {
		t.Fatalf("the line ran into Text: %q", acts[0].Text)
	}
	if n := len(Queue(filepath.Dir(path))); n != len(Actions(good())) {
		t.Fatalf("queue: %d", n)
	}

	if _, err := Overtake(path, "A1", "9b8b225", "owner", "", queueDay); err != nil {
		t.Fatal(err)
	}
	if _, err := Reopen(path, "A1", "owner", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	acts = Actions(string(raw))
	if len(acts[0].Reopened) != 2 || !strings.Contains(acts[0].Reopened[1], "was Overtaken: 9b8b225 · 2026-11-05 (owner)") {
		t.Fatalf("history: %q", acts[0].Reopened)
	}
	if !strings.HasPrefix(acts[0].Reopened[0], "2026-11-05T10:30:00Z") {
		t.Fatalf("history out of order: %q", acts[0].Reopened)
	}
}

// Applied made something a line can't take back; an open action has
// nothing to undo.
func TestReopenRefusesAnApplied(t *testing.T) {
	path := saved(t, t.TempDir(), "cart", queueDay)
	raw, _ := os.ReadFile(path)
	next, err := Mark(string(raw), "A1", true, "memory m-1 · agreed 2026-11-05 (owner)")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"A1", "A2", "A9"} {
		if _, err := Reopen(path, id, "owner", queueDay); err == nil {
			t.Errorf("%s reopened", id)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != next {
		t.Fatal("a refused undo changed the file")
	}
}

// What Undo offers: the owner's marks of the last MarkedDays calendar days,
// newest first, on the post-mortems Queue reads.
func TestMarkedIsTheOwnersRecentMarks(t *testing.T) {
	dir := t.TempDir()
	now := queueDay
	older := saved(t, dir, "cart", now)
	newest := saved(t, dir, "cart", now.Add(time.Minute))
	moved := archived(t, saved(t, dir, "till", now.Add(2*time.Minute)))
	mustRefuse := func(path, id, who string, at time.Time) {
		t.Helper()
		if _, err := Refuse(path, id, who, "", at); err != nil {
			t.Fatal(err)
		}
	}
	mustRefuse(older, "A1", "owner", now)                    // moved past: not offered
	mustRefuse(newest, "A1", "owner", now.AddDate(0, 0, -7)) // the window's first day
	mustRefuse(newest, "A2", "owner", now.AddDate(0, 0, -8)) // a day too old
	mustRefuse(newest, "A3", "lead-acme", now)               // not the owner's
	if _, err := Overtake(moved, "A1", "9b8b225", "owner", "", now); err != nil {
		t.Fatal(err)
	}
	got := Marked(dir, "owner", now)
	if len(got) != 2 || got[0].Issue != "till" || got[0].ID != "A1" || got[1].Issue != "cart" || got[1].ID != "A1" {
		t.Fatalf("marked: %+v", got)
	}
	if got[0].Path != moved || got[0].Mark() != "Overtaken: 9b8b225 · 2026-11-05 (owner)" {
		t.Fatalf("first: %+v", got[0])
	}
}
