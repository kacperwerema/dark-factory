package spend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var (
	kSince = time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	kUntil = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
)

// kindsFixture: the fixture's project with typed issues, a job run issue
// (lead-flush) and a sweep run, and a stats sidecar giving seo/page-refresh
// its stages.
func kindsFixture(t *testing.T) *fixture {
	f := newFixture(t)
	if err := os.MkdirAll(f.p.IssueDir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.issues[0].Type, f.issues[1].Type = "feature", "bugfix"
	f.issues[0].Status = "testing"
	f.issues[0].FilePath = filepath.Join(f.p.IssueDir, "page-refresh.md")
	stats := `{"transitions":[{"from":"idea","to":"in progress","ts":"2026-10-28T08:00:00Z"},{"from":"in progress","to":"testing","ts":"2026-10-28T10:00:00Z"}]}`
	if err := os.WriteFile(tracker.StatsSidecarPath(f.issues[0].FilePath), []byte(stats), 0o644); err != nil {
		t.Fatal(err)
	}
	f.issues = append(f.issues,
		&tracker.Issue{Slug: "jobs/flush-2026-10-28", ExtraFields: []tracker.ExtraField{{Key: "job", Value: "lead-flush"}}},
		&tracker.Issue{Slug: "jobs/sweep-2026-10-28", ExtraFields: []tracker.ExtraField{{Key: "job", Value: "retro-sweep"}}},
		&tracker.Issue{Slug: "jobs/digest-2026-10-28", ExtraFields: []tracker.ExtraField{{Key: "job", Value: "weekly-digest"}}})
	return f
}

func (f *fixture) kinds(opt KindsOptions) *KindsReport {
	opt.ProjectsDir = f.projects
	if opt.Projects == nil {
		opt.Projects = []*tracker.Project{f.p}
	}
	if opt.Issues == nil {
		opt.Issues = map[string][]*tracker.Issue{f.p.Slug: f.issues}
	}
	if opt.Book == nil {
		opt.Book = &PriceBook{Path: "/x/prices.yaml", Prices: prices, Source: map[string]string{"claude-test": "/x/prices.yaml"}}
	}
	if opt.BenchRuns == nil {
		opt.BenchRuns = func() []*bench.Run { return nil }
	}
	return LoadKinds(kSince, kUntil, opt)
}

func rowOf(rep *KindsReport, project, kind, job string) KindRow {
	for _, r := range rep.Rows {
		if r.Project == project && r.Kind == kind && r.Job == job {
			return r
		}
	}
	return KindRow{}
}

func TestLoadKindsSplitsEveryKindOfSession(t *testing.T) {
	f := kindsFixture(t)
	at := func(h int) time.Time { return time.Date(2026, 10, 28, h, 0, 0, 0, time.UTC) }
	root := f.p.Root()
	// A worker in its worktree at two stages, with a subagent; the critic
	// runs in the same worktree under its own name.
	f.write(f.worktree("seo/page-refresh"), "w.jsonl", "agent-seo-page-refresh",
		msg("w1", at(9), "claude-test", 1000, 0, 0, 0), msg("w2", at(11), "claude-test", 2000, 0, 0, 0))
	f.write(f.worktree("seo/page-refresh"), "w/subagents/s.jsonl", "", msg("s1", at(9), "claude-test", 50, 0, 0, 0))
	f.write(f.worktree("seo/page-refresh"), "c.jsonl", "critic-page-refresh", msg("c1", at(12), "claude-test", 70, 0, 0, 0))
	// An unnamed session in a worktree is its issue's worker.
	f.write(f.worktree("seo/blog"), "b.jsonl", "", msg("b1", at(9), "claude-test", 300, 0, 0, 0))
	// Job runs: lead-flush, a sweep, another job.
	f.write(f.worktree("jobs/flush-2026-10-28"), "j.jsonl", "agent-jobs-flush-2026-10-28", msg("j1", at(9), "claude-test", 11, 0, 0, 0))
	f.write(f.worktree("jobs/sweep-2026-10-28"), "j.jsonl", "", msg("j2", at(9), "claude-test", 12, 0, 0, 0))
	f.write(f.worktree("jobs/digest-2026-10-28"), "j.jsonl", "", msg("j3", at(9), "claude-test", 13, 0, 0, 0))
	// The project folder: the CEO, a department lead, a post-mortem, a
	// benchmark on the owner's HOME, a health review, the owner, a stray name.
	f.write(root, "ceo.jsonl", "lead-acme", msg("l1", at(9), "claude-test", 400, 0, 0, 0))
	f.write(root, "mkt.jsonl", "lead-acme-marketing", msg("l2", at(9), "claude-test", 40, 0, 0, 0))
	f.write(root, "pm.jsonl", "postmortem-seo-page-refresh", msg("p1", at(9), "claude-test", 20, 0, 0, 0))
	f.write(root, "bench.jsonl", "bench-page-refresh", msg("x1", at(9), "claude-test", 21, 0, 0, 0))
	f.write(root, "rev.jsonl", "review-acme", msg("r1", at(9), "claude-test", 22, 0, 0, 0))
	f.write(root, "me.jsonl", "", msg("o1", at(9), "claude-test", 5, 0, 0, 0), msg("o0", longAgo, "claude-test", 999, 0, 0, 0))
	f.write(root, "odd.jsonl", "my-own-name", msg("n1", at(9), "claude-test", 6, 0, 0, 0))
	// Another project's folder is not read without Others.
	f.write("/elsewhere/proj", "e.jsonl", "", msg("e1", at(9), "claude-test", 7000, 0, 0, 0))

	rep := f.kinds(KindsOptions{})
	want := map[[2]string]int64{
		{KindWorker, ""}: 3300, {KindSubagent, ""}: 50, {KindCritic, ""}: 70,
		{KindFlush, "lead-flush"}: 11, {KindSweep, "retro-sweep"}: 12, {KindJob, "weekly-digest"}: 13,
		{KindLead, ""}: 440, {KindPostmortem, ""}: 20, {KindBenchmark, ""}: 21, {KindReview, ""}: 22,
		{KindOwner, ""}: 5, {KindOther, ""}: 6,
	}
	for k, n := range want {
		if got := rowOf(rep, "acme", k[0], k[1]).Tokens(); got != n {
			t.Errorf("%s %s: %d tokens, want %d", k[0], k[1], got, n)
		}
	}
	if len(rep.Rows) != len(want) {
		t.Errorf("rows: %+v", rep.Rows)
	}
	if rep.Total.Tokens() != 3970 || rep.Total.Turns != 15 {
		t.Errorf("total: %d tokens, %d turns", rep.Total.Tokens(), rep.Total.Turns)
	}
	if w := rowOf(rep, "acme", KindWorker, ""); w.Sessions != 2 || w.Turns != 3 {
		t.Errorf("worker sessions/turns: %d/%d", w.Sessions, w.Turns)
	}
	stages := map[string]int64{}
	for _, w := range rep.Workers {
		stages[w.Type+"/"+w.Stage] = w.Tokens()
	}
	if stages["feature/in progress"] != 1000 || stages["feature/testing"] != 2000 || stages["bugfix/"] != 300 {
		t.Errorf("stages: %v", stages)
	}
	if s := rowOf(rep, "acme", KindWorker, "").Share; s < 0.83 || s > 0.84 {
		t.Errorf("worker share %v", s)
	}
	if rep.ShareOf != "usd" || len(rep.Prices) != 1 || rep.Prices[0].Status != "assumed" {
		t.Errorf("prices: %+v %s", rep.Prices, rep.ShareOf)
	}
	if rep.LimitNote == "" || !strings.Contains(rep.LimitNote, "weekly_limit") || rowOf(rep, "acme", KindWorker, "").LimitShare != nil {
		t.Errorf("no limit: note %q", rep.LimitNote)
	}

	// Others: the folder of no project is "other".
	all := f.kinds(KindsOptions{Others: true})
	if got := rowOf(all, OtherProject, KindOther, "").Tokens(); got != 7000 {
		t.Errorf("other: %d", got)
	}
	if len(all.ByProject) != 2 || all.ByProject[0].Project != "acme" {
		t.Errorf("by project: %+v", all.ByProject)
	}
}

func TestLoadKindsCountsSandboxBenchRunsFromTheirRecordsOnly(t *testing.T) {
	f := kindsFixture(t)
	in := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	run := func(home string, tokens int64) *bench.Run {
		return &bench.Run{ID: "r-" + home, Setup: bench.Setup{WorkerHome: home}, Tasks: []bench.TaskRecord{
			{Task: "acme/x", Project: "acme", Started: in, Usage: bench.Usage{Known: true, Input: tokens, Model: "claude-test", Turns: 4}},
			{Task: "acme/old", Project: "acme", Started: longAgo, Usage: bench.Usage{Known: true, Input: 999, Model: "claude-test"}},
			{Task: "zeta/y", Project: "zeta", Started: in, Usage: bench.Usage{Known: true, Input: 5, Model: "claude-test"}},
		}}
	}
	// The owner-HOME run's transcript is in the projects folder, named.
	f.write(f.p.Root(), "bench.jsonl", "bench-x", msg("x1", in, "claude-test", 77, 0, 0, 0))
	rep := f.kinds(KindsOptions{BenchRuns: func() []*bench.Run {
		return []*bench.Run{run(bench.WorkerHomeSandbox, 100), run(bench.WorkerHomeOwner, 200), run("", 300)}
	}})
	b := rowOf(rep, "acme", KindBenchmark, "")
	if b.Tokens() != 177 || b.Turns != 5 || rep.BenchTasks != 1 {
		t.Errorf("benchmark: %d tokens, %d turns, %d tasks (want 100 from the sandbox record + 77 from the transcript, 4+1 turns)", b.Tokens(), b.Turns, rep.BenchTasks)
	}
	if b.USD == 0 {
		t.Error("the record's tokens are priced")
	}
}

func TestLoadKindsWeeklyLimitAndConfirmedPrices(t *testing.T) {
	f := kindsFixture(t)
	f.write(f.p.Root(), "ceo.jsonl", "lead-acme", msg("l1", time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC), "claude-test-5", 1_000_000, 0, 0, 0))
	path := filepath.Join(t.TempDir(), "prices.yaml")
	if err := os.WriteFile(path, []byte("prices:\n  claude-test: {in: 1, out: 10, confirmed: true}\nweekly_limit: {usd: 7}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	book := LoadPriceBook(path)
	rep := f.kinds(KindsOptions{Book: &book})
	if len(rep.Prices) != 1 || rep.Prices[0].Status != "confirmed" || rep.Prices[0].Key != "claude-test" || rep.Prices[0].Source != path {
		t.Fatalf("prices: %+v", rep.Prices)
	}
	lead := rowOf(rep, "acme", KindLead, "")
	// $1 over a 3.5-day window against $7 a week: $1 / (7 × 3.5/7) = 28.6%.
	if lead.LimitShare == nil || *lead.LimitShare < 0.285 || *lead.LimitShare > 0.287 {
		t.Errorf("limit share: %v", lead.LimitShare)
	}
	if rep.LimitNote != "" || rep.WeeklyLimit == nil || rep.WeeklyLimit.Text() != "$7.00 a week" {
		t.Errorf("limit: %+v %q", rep.WeeklyLimit, rep.LimitNote)
	}
	tok := WeeklyLimit{Tokens: 2_000_000}
	if s, ok := tok.Share(Tally{Input: 1_000_000}, 7); !ok || s != 0.5 {
		t.Errorf("tokens limit: %v %v", s, ok)
	}
	if _, ok := (*WeeklyLimit)(nil).Share(Tally{Input: 1}, 7); ok {
		t.Error("no limit, no share")
	}
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	os.WriteFile(bad, []byte("prices: [\n"), 0o644)
	if b := LoadPriceBook(bad); b.Err == "" || len(b.Prices) != 0 {
		t.Errorf("a file that doesn't parse: %+v", b)
	}
}

func TestKindsKeepNoTranscriptText(t *testing.T) {
	f := kindsFixture(t)
	f.write(f.worktree("seo/page-refresh"), "w.jsonl", "agent-seo-page-refresh", msg("w1", time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC), "claude-test", 10, 0, 0, 0))
	data, err := json.Marshal(f.kinds(KindsOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret words") {
		t.Error("transcript text in the report")
	}
}
