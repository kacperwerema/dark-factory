package spend

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/bench"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// now is a Friday; this week starts Monday 2026-10-26.
var now = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

var prices = map[string]bench.Price{"claude-test": {In: 1, Out: 10, CacheRead: 0.1, CacheWrite: 2}}

type fixture struct {
	t        *testing.T
	projects string
	p        *tracker.Project
	issues   []*tracker.Issue
}

// newFixture: Product, GTM over Marketing (system SEO) and Sales, a budget
// on Marketing.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	ResetCache()
	root := t.TempDir()
	p := &tracker.Project{
		Slug: "acme", IssueDir: filepath.Join(root, "issues"), WorkDir: root,
		Departments: []tracker.Department{
			{Name: "GTM"},
			{Name: "Marketing", Systems: []string{"SEO"}, Parent: "GTM", Budget: &tracker.Budget{USD: 1, Period: "week"}},
			{Name: "Sales", Systems: []string{"Leads"}, Parent: "GTM"},
		},
	}
	f := &fixture{t: t, projects: t.TempDir(), p: p}
	f.issue("seo/page-refresh", "SEO")
	f.issue("seo/blog", "SEO")
	f.issue("ui/button", "UI")
	return f
}

func (f *fixture) issue(slug, system string) {
	f.issues = append(f.issues, &tracker.Issue{Slug: slug, Title: "T " + slug, System: system})
}

// msg is one assistant line; id "" leaves the message id out.
func msg(id string, at time.Time, model string, in, out, cr, cw int64) string {
	return fmt.Sprintf(`{"type":"assistant","sessionId":"s","timestamp":%q,"message":{"id":%q,"model":%q,"usage":{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d},"content":[{"type":"text","text":"secret words"}]}}`,
		at.Format(time.RFC3339Nano), id, model, in, out, cr, cw)
}

// write puts a transcript for cwd (session name; "" for none).
func (f *fixture) write(cwd, file, agent string, lines ...string) string {
	f.t.Helper()
	folders := bench.TranscriptFolders(f.projects, cwd)
	folder := filepath.Join(f.projects, strings.NewReplacer("/", "-", ".", "-").Replace(cwd))
	if len(folders) == 1 {
		folder = folders[0]
	}
	path := filepath.Join(folder, file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if agent != "" {
		lines = append([]string{fmt.Sprintf(`{"type":"agent-name","agentName":%q,"sessionId":"s"}`, agent)}, lines...)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return path
}

func (f *fixture) worktree(slug string) string { return filepath.Join(f.p.Root(), ".worktrees", slug) }

func (f *fixture) load() *Report {
	return Load(f.p, f.issues, now, Options{ProjectsDir: f.projects, Prices: prices})
}

var (
	thisWeek = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	lastWeek = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	longAgo  = time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
)

func TestLoadAttributesByWorktreeAndAgentName(t *testing.T) {
	f := newFixture(t)
	// A Marketing worker in its worktree, with a subagent; a resumed file
	// repeating message m1 counts it once.
	f.write(f.worktree("seo/page-refresh"), "a.jsonl", "",
		msg("m1", thisWeek, "claude-test", 1000, 100, 0, 0),
		msg("m2", lastWeek, "claude-test", 500, 0, 0, 0))
	f.write(f.worktree("seo/page-refresh"), "b.jsonl", "", msg("m1", thisWeek, "claude-test", 1000, 100, 0, 0))
	f.write(f.worktree("seo/page-refresh"), "a/subagents/x.jsonl", "", msg("m3", thisWeek, "claude-test", 10, 0, 0, 0))
	// Product's worker; an old message outside the window.
	f.write(f.worktree("ui/button"), "c.jsonl", "", msg("p1", thisWeek, "claude-test", 2000, 0, 0, 0), msg("p0", longAgo, "claude-test", 9999, 0, 0, 0))
	root := f.p.Root()
	f.write(root, "ceo.jsonl", "lead-acme", msg("c1", thisWeek, "claude-test", 300, 0, 0, 0))
	f.write(root, "mkt.jsonl", "lead-acme-marketing", msg("l1", thisWeek, "claude-test", 40, 0, 0, 0))
	f.write(root, "gtm.jsonl", "lead-acme-gtm", msg("g1", thisWeek, "claude-test", 7, 0, 0, 0))
	f.write(root, "worker.jsonl", "agent-seo-blog", msg("w1", thisWeek, "claude-test", 60, 0, 0, 0))
	f.write(root, "owner.jsonl", "", msg("o1", thisWeek, "claude-test", 5, 0, 0, 0))
	// A name record written with spaces still names the session; a message
	// quoting one does not.
	f.write(root, "spaced.jsonl", "", `{"type": "agent-name", "agentName": "lead-acme-sales"}`, msg("s1", thisWeek, "claude-test", 3, 0, 0, 0))
	f.write(root, "quoted.jsonl", "", `{"type":"user","message":{"content":"{\"type\":\"agent-name\",\"agentName\":\"lead-acme\"}"}}`, msg("q1", thisWeek, "claude-test", 2, 0, 0, 0))
	f.write(root, "other.jsonl", "lead-other", msg("x1", thisWeek, "claude-test", 77777, 0, 0, 0))

	rep := f.load()
	tok := func(key string, week int) int64 { return rep.Find(key).Weeks[week].Tokens() }
	// Marketing: worker 1100+10 + lead 40 + root worker 60 this week; 500 last.
	if got := tok("marketing", 0); got != 1210 {
		t.Errorf("marketing this week = %d, want 1210", got)
	}
	if got := tok("marketing", 1); got != 500 {
		t.Errorf("marketing last week = %d, want 500", got)
	}
	if got := tok("product", 0); got != 2000 {
		t.Errorf("product = %d, want 2000 (the old message is outside the weeks)", got)
	}
	if got := tok("sales", 0); got != 3 {
		t.Errorf("sales = %d, want 3", got)
	}
	if got := tok("gtm", 0); got != 1220 {
		t.Errorf("gtm = %d, want its lead 7 + marketing 1210 + sales 3", got)
	}
	if got := tok("hq", 0); got != 300+2000+1220 {
		t.Errorf("hq = %d, want CEO + product + gtm", got)
	}
	if got := rep.Unattributed[0].Tokens(); got != 7 {
		t.Errorf("unattributed = %d, want the owner's 5 + the quoting session's 2 (another project's lead skipped)", got)
	}
	m := rep.Find("Marketing")
	if len(m.Top) != 2 || m.Top[0].Slug != "seo/page-refresh" || m.Top[0].Tokens() != 1610 || m.Top[1].Slug != "seo/blog" {
		t.Errorf("marketing top = %+v", m.Top)
	}
	if g := rep.Find("gtm"); strings.Join(g.RolledFrom, ",") != "marketing,sales" {
		t.Errorf("gtm rolled from %v", g.RolledFrom)
	}
	// Cost: m1 1000*1+100*10 = 2000e-6, m3 10e-6, lead 40e-6, worker 60e-6.
	if c := m.ThisWeek().USD; c < 0.00210 || c > 0.00212 {
		t.Errorf("marketing cost = %f", c)
	}
	if !m.ThisWeek().CostKnown() {
		t.Error("every model has a price")
	}
	if b := m.Budget; b == nil || b.Over || b.Text != "$0.00 of $1.00 this week (est.)" {
		t.Errorf("budget = %+v", b)
	}
	if rep.Weeks0() != "2026-10-26" {
		t.Errorf("this week starts %s", rep.Weeks0())
	}
}

// Weeks0 is the first week's Monday (test helper).
func (r *Report) Weeks0() string { return r.Boxes[0].Weeks[0].Start }

func TestUnpricedModelAndTokenBudget(t *testing.T) {
	f := newFixture(t)
	f.p.Departments[2].Budget = &tracker.Budget{Tokens: 100, Period: "month"}
	f.write(f.p.Root(), "s.jsonl", "lead-acme-sales",
		msg("a", thisWeek, "claude-new", 150, 0, 0, 0),
		msg("b", time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC), "claude-test", 1, 0, 0, 0))
	rep := f.load()
	s := rep.Find("sales")
	if s.ThisWeek().Priced() || CostText(s.ThisWeek()) != "cost unknown" {
		t.Errorf("unpriced week: %+v %q", s.ThisWeek(), CostText(s.ThisWeek()))
	}
	if len(rep.Unpriced) != 1 || rep.Unpriced[0] != "claude-new" {
		t.Errorf("unpriced = %v", rep.Unpriced)
	}
	// The month counts 2 October before the 4 weeks start (5 October).
	if b := s.Budget; !b.Over || b.Text != "151 of 100 tokens this month" {
		t.Errorf("budget = %+v", b)
	}
	if got := CostText(rep.Find("hq").ThisWeek()); got != "cost unknown" {
		t.Errorf("hq cost = %q", got)
	}
}

func TestOldFilesSkippedAndCacheFollowsChanges(t *testing.T) {
	f := newFixture(t)
	path := f.write(f.worktree("ui/button"), "c.jsonl", "", msg("p1", thisWeek, "claude-test", 10, 0, 0, 0))
	if got := f.load().Find("product").ThisWeek().Tokens(); got != 10 {
		t.Fatalf("product = %d", got)
	}
	f.write(f.worktree("ui/button"), "c.jsonl", "", msg("p1", thisWeek, "claude-test", 10, 0, 0, 0), msg("p2", thisWeek, "claude-test", 5, 0, 0, 0))
	later := time.Now().Add(time.Minute)
	os.Chtimes(path, later, later)
	if got := f.load().Find("product").ThisWeek().Tokens(); got != 15 {
		t.Errorf("after the file grew: %d, want 15", got)
	}
	old := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	os.Chtimes(path, old, old)
	if got := f.load().Find("product").ThisWeek().Tokens(); got != 0 {
		t.Errorf("a file last changed before the window is skipped: %d", got)
	}
}

func TestEmptyAndBudgetErr(t *testing.T) {
	f := newFixture(t)
	f.p.Departments[1].Budget = &tracker.Budget{Err: "budget: is not a mapping"}
	rep := f.load()
	if !rep.Empty() || !rep.Find("marketing").Empty() {
		t.Error("nothing written: empty")
	}
	if b := rep.Find("marketing").Budget; b == nil || b.Text != "unreadable (budget: is not a mapping)" || b.Over {
		t.Errorf("budget = %+v", b)
	}
	if rep.Find("nope") != nil {
		t.Error("unknown box")
	}
}

func TestFormat(t *testing.T) {
	for n, want := range map[int64]string{950: "950", 12300: "12.3k", 4_100_000: "4.1M", 2_000_000_000: "2B", 150_000_000: "150M"} {
		if got := FormatTokens(n); got != want {
			t.Errorf("FormatTokens(%d) = %q, want %q", n, got, want)
		}
	}
	if got := FormatUSD(1234.4); got != "$1,234" {
		t.Errorf("FormatUSD = %q", got)
	}
	if got := Line(Tally{Input: 2000, USD: 0.5}); got != "2k tokens · $0.50 est." {
		t.Errorf("Line = %q", got)
	}
	if got := CostText(Tally{Input: 10, Output: 10, Unpriced: 10, USD: 1}); got != "≥ $1.00 est." {
		t.Errorf("CostText part-priced = %q", got)
	}
	if got := WeekStart(time.Date(2026, 11, 1, 23, 0, 0, 0, time.UTC)); got.Format("2006-01-02") != "2026-10-26" {
		t.Errorf("Sunday's week starts %s", got)
	}
}

func TestLoadPricesFromFileAndBenchmarks(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", filepath.Join(cfg, "ai-workflow-engine", "bench"))
	os.MkdirAll(filepath.Join(cfg, "ai-workflow-engine", "bench"), 0o755)
	os.WriteFile(PricesPath(), []byte("prices:\n  claude-a: {in: 3, out: 15}\n"), 0o644)
	os.WriteFile(filepath.Join(cfg, "ai-workflow-engine", "bench", "b.yaml"), []byte("name: b\nprices:\n  claude-a: {in: 99, out: 99}\n  claude-b: {in: 1, out: 5}\ntasks: []\n"), 0o644)
	got := LoadPrices()
	if got["claude-a"].In != 3 || got["claude-b"].Out != 5 {
		t.Errorf("prices = %+v (the prices file wins, benchmarks fill the rest)", got)
	}
	if !strings.HasPrefix(PricesPath(), cfg) {
		t.Errorf("PricesPath = %s", PricesPath())
	}
}

// Nested groups: Sales under Outbound under GTM; Sales's spend counts in
// Outbound and in GTM, and HQ counts each box once
// (factory/nested-groups-a-group-inside-a-group-with-goals-rolling-up-and-the-map-drawing-it#D-1).
func TestNestedGroupSpendRollsUpEveryLevel(t *testing.T) {
	f := newFixture(t)
	f.p.Departments = []tracker.Department{
		{Name: "GTM"},
		{Name: "Outbound", Parent: "gtm"},
		{Name: "Sales", Systems: []string{"Leads"}, Parent: "outbound"},
		{Name: "Marketing", Systems: []string{"SEO"}, Parent: "GTM"},
	}
	root := f.p.Root()
	f.write(root, "sales.jsonl", "lead-acme-sales", msg("s1", thisWeek, "claude-test", 100, 0, 0, 0))
	f.write(root, "out.jsonl", "lead-acme-outbound", msg("o1", thisWeek, "claude-test", 20, 0, 0, 0))
	f.write(root, "gtm.jsonl", "lead-acme-gtm", msg("g1", thisWeek, "claude-test", 3, 0, 0, 0))
	f.write(root, "mkt.jsonl", "lead-acme-marketing", msg("m1", thisWeek, "claude-test", 5000, 0, 0, 0))
	rep := f.load()
	tok := func(key string) int64 { return rep.Find(key).Weeks[0].Tokens() }
	if got := tok("outbound"); got != 120 {
		t.Errorf("outbound = %d, want its lead 20 + sales 100", got)
	}
	if got := tok("gtm"); got != 5123 {
		t.Errorf("gtm = %d, want 3 + outbound 120 + marketing 5000", got)
	}
	if got := tok("hq"); got != 5123 {
		t.Errorf("hq = %d, want every box once: 5123", got)
	}
	if g := rep.Find("gtm"); strings.Join(g.RolledFrom, ",") != "outbound,sales,marketing" {
		t.Errorf("gtm rolled from %v", g.RolledFrom)
	}
	if o := rep.Find("outbound"); strings.Join(o.RolledFrom, ",") != "sales" || o.Parent != "gtm" {
		t.Errorf("outbound = parent %q rolled from %v", o.Parent, o.RolledFrom)
	}
}
