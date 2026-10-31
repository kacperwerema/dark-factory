package critique

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseFindings(t *testing.T) {
	out := `Here is my review.

- blocks internal/x/y.go:42 — the empty list returns nil and the caller dereferences it
should docs/foo.md:10-14 - claims "always" with no decision cited
**note** cmd/issue-cli/cmd_x.go:7: flag name differs from its siblings
blocks the whole design is unsafe
should internal/a.go:3
Blocks of prose like this are not findings.
Coverage: read 6 of 8 files
Not read: static/a.js, templates/b.html
`
	r := Parse(out)
	if len(r.Findings) != 5 {
		t.Fatalf("findings = %d, want 5: %+v", len(r.Findings), r.Findings)
	}
	f := r.Findings[0]
	if f.Severity != Blocks || f.Path != "internal/x/y.go" || f.Line != 42 || !strings.HasPrefix(f.Text, "the empty list") {
		t.Errorf("first = %+v", f)
	}
	if g := r.Findings[1]; g.Severity != Should || g.Where() != "docs/foo.md:10-14" {
		t.Errorf("range = %+v (%s)", g, g.Where())
	}
	if g := r.Findings[2]; g.Severity != Note || g.Line != 7 {
		t.Errorf("bold note = %+v", g)
	}
	// Malformed lines are reported, kept at their severity, not dropped.
	mal := r.Malformed()
	if len(mal) != 2 {
		t.Fatalf("malformed = %+v", mal)
	}
	if mal[0].Severity != Blocks || mal[0].Problem != "no path:line" || !strings.Contains(mal[0].Raw, "whole design") {
		t.Errorf("malformed blocks = %+v", mal[0])
	}
	if mal[1].Severity != Should || mal[1].Problem != "no text after path:line" {
		t.Errorf("malformed should = %+v", mal[1])
	}
	if r.Count(Blocks) != 2 {
		t.Errorf("blocks count = %d, want 2 (a malformed blocks still counts)", r.Count(Blocks))
	}
	if r.Coverage != "read 6 of 8 files" || strings.Join(r.Unread, "|") != "static/a.js|templates/b.html" {
		t.Errorf("coverage %q unread %v", r.Coverage, r.Unread)
	}
	if len(r.Problems) != 0 {
		t.Errorf("problems = %v", r.Problems)
	}
}

func TestParseWithoutCoverageIsAProblem(t *testing.T) {
	r := Parse("should a.go:1 — x\n")
	if len(r.Problems) != 1 {
		t.Fatalf("problems = %v", r.Problems)
	}
	run := &Run{Report: r}
	if !run.Holds() {
		t.Error("a run with no Coverage line must hold the gate")
	}
	if !(&Run{Report: Parse("")}).Holds() {
		t.Error("empty output must hold")
	}
	if (&Run{Report: Parse("note a.go:1 — x\nCoverage: all\nNot read: none")}).Holds() {
		t.Error("notes only must not hold")
	}
}

func TestStoreLatestAndOverride(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)
	bad := &Run{Slug: "s", From: "testing", To: "human-testing", At: t0, Report: Parse("blocks a.go:1 — x\nCoverage: all")}
	other := &Run{Slug: "other", From: "testing", At: t0.Add(time.Hour), Report: Parse("Coverage: all")}
	for _, r := range []*Run{bad, other} {
		if err := Record(dir, r); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := Load(dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	latest := Latest(runs, "s", "testing")
	if latest == nil || latest.ID != bad.ID || !latest.Holds() {
		t.Fatalf("latest = %+v", latest)
	}
	if Latest(runs, "s", "in design") != nil {
		t.Error("a run from another status counts")
	}
	err = GateError(latest, "s", "testing")
	if err == nil || !strings.Contains(err.Error(), "blocks a.go:1 — x") || !strings.Contains(err.Error(), "issue-cli critique s") {
		t.Fatalf("gate = %v", err)
	}
	if err := RecordOverride(dir, bad.ID, Override{At: t0, Quote: ""}); err == nil {
		t.Error("an override without words was recorded")
	}
	if err := RecordOverride(dir, bad.ID, Override{At: t0, Quote: "ship it"}); err != nil {
		t.Fatal(err)
	}
	runs, _ = Load(dir, "s")
	latest = Latest(runs, "s", "testing")
	if latest.Holds() || latest.Verdict() != "overridden" || GateError(latest, "s", "testing") != nil {
		t.Errorf("after override: %s", latest.Verdict())
	}
	if GateError(nil, "s", "testing") == nil {
		t.Error("no run must hold")
	}
	failed := &Run{Failed: true, Error: "timed out", Report: Parse("Coverage: all")}
	if !failed.Holds() || !strings.Contains(GateError(failed, "s", "x").Error(), "timed out") {
		t.Error("a failed run must hold and say why")
	}
}

func TestBriefCarriesPathsOnly(t *testing.T) {
	in := Input{Slug: "f/x", From: "testing", To: "human-testing", Issue: "/i/f/x.md", Diff: ".git-critique/branch.diff",
		Changed: []string{"a.go", "docs/b.md"}, Rules: []string{"docs/departments.md"}, Root: "/w"}
	b := Brief(in)
	for _, want := range []string{"Assume they are wrong", "/i/f/x.md", ".git-critique/branch.diff", "a.go", "docs/b.md  (doc changed by the branch)", "docs/departments.md  (settled rules)", "Coverage:", "Not read:", "blocks <path>:<line>"} {
		if !strings.Contains(b, want) {
			t.Errorf("brief lacks %q:\n%s", want, b)
		}
	}
}

// loadFixture reads testdata/spec-flow-runs.jsonl: the six critique records
// of the critic's first real use (factory/spec-flow-in-use-…, 2026-10-31),
// copied unchanged from the dark-factory run log.
func loadFixture(t *testing.T) []*Run {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "spec-flow-runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LogPath(dir), data, 0o644); err != nil {
		t.Fatal(err)
	}
	runs, err := Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 6 {
		t.Fatalf("fixture runs = %d, want 6", len(runs))
	}
	return runs
}

// Every finding of the five real runs parses again as one well-formed
// finding, the multi-line ones (path:29,114, path:14-16,144-147) with
// every line kept. The originals were marked "no text after path:line".
func TestParseMultiLineRefsFromTheFiveRunFixture(t *testing.T) {
	runs := loadFixture(t)
	wasMalformed := 0
	for _, run := range runs {
		for _, f := range run.Report.Findings {
			line := f.String()
			if f.Malformed {
				wasMalformed++
				line = f.Raw
			}
			r := Parse(line + "\nCoverage: all\nNot read: none")
			if len(r.Findings) != 1 {
				t.Fatalf("%q: findings = %+v", line, r.Findings)
			}
			g := r.Findings[0]
			if g.Malformed {
				t.Errorf("%q still malformed: %s", line, g.Problem)
				continue
			}
			if g.Severity != f.Severity || !strings.HasPrefix(g.Path, "issues/Factory/spec-flow-in-use-") || g.Text == "" {
				t.Errorf("%q parsed as %+v", line, g)
			}
			if !f.Malformed && (g.Where() != f.Where() || g.Text != f.Text) {
				t.Errorf("well-formed %q changed: %s — %s", line, g.Where(), g.Text)
			}
		}
	}
	if wasMalformed != 14 {
		t.Errorf("fixture malformed findings = %d, want 14", wasMalformed)
	}
	newest := runs[len(runs)-1]
	r := Parse(newest.Report.Findings[2].Raw + "\nCoverage: all")
	if got := r.Findings[0].Spans(); len(got) != 3 || got[0].Line != 115 || got[1].Line != 127 || got[2].Line != 134 {
		t.Errorf(":115,127,134 spans = %+v", got)
	}
	if !strings.Contains(r.Findings[0].Where(), ".md:115,127,134") {
		t.Errorf("where = %s", r.Findings[0].Where())
	}
	ranges := Parse("should issue.md:14-16, 144-147 — the only owner words\nCoverage: all").Findings[0]
	if ranges.Malformed || ranges.Where() != "issue.md:14-16,144-147" {
		t.Errorf("ranges = %+v (%s)", ranges, ranges.Where())
	}
	if f := Parse("blocks a.go:0,3 — zero line\nCoverage: all").Findings[0]; !f.Malformed {
		t.Errorf("line 0 accepted: %+v", f)
	}
}

// The critic's output of a real run (20261031T153532.805403000Z): its
// fifth finding is a lead-in line ending in ':' followed by three bullet
// lines. The record in testdata/ceo-chat-files-run.jsonl, from the
// dark-factory run log, kept only "Some cases are not covered:". Both
// files are anonymised (D-3): the issue path, slug and Coverage are
// generic and the other findings' texts are short stand-ins; severities,
// lines and the fifth finding are as the critic wrote them.
func TestParseKeepsAFindingsContinuationLines(t *testing.T) {
	out, err := os.ReadFile(filepath.Join("testdata", "ceo-chat-files-critic-output.txt"))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := os.ReadFile(filepath.Join("testdata", "ceo-chat-files-run.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LogPath(dir), rec, 0o644); err != nil {
		t.Fatal(err)
	}
	runs, err := Load(dir, "")
	if err != nil || len(runs) != 1 {
		t.Fatalf("record: %v, %d runs", err, len(runs))
	}
	old := runs[0].Report

	r := Parse(string(out))
	if len(r.Findings) != len(old.Findings) || len(r.Problems) != 0 || r.Coverage != old.Coverage {
		t.Fatalf("parsed %d findings (record %d), problems %v, coverage %q", len(r.Findings), len(old.Findings), r.Problems, r.Coverage)
	}
	for i, f := range r.Findings {
		o := old.Findings[i]
		if f.Malformed || f.Severity != o.Severity || f.Where() != o.Where() {
			t.Errorf("finding %d = %+v, record %+v", i, f, o)
		}
		if i != 4 && f.Text != o.Text {
			t.Errorf("finding %d text changed:\n%s\nrecord:\n%s", i, f.Text, o.Text)
		}
	}
	cut := r.Findings[4]
	if old.Findings[4].Text != "Some cases are not covered:" {
		t.Fatalf("record's fifth finding = %q", old.Findings[4].Text)
	}
	lines := strings.Split(cut.Text, "\n")
	if len(lines) != 4 || lines[0] != "Some cases are not covered:" ||
		!strings.HasPrefix(lines[1], "- The temp-file name is not specified") ||
		!strings.HasPrefix(lines[2], "- The rule does not say what happens when the extension") ||
		!strings.HasSuffix(lines[3], "several uploads at once could fill memory.") {
		t.Errorf("fifth finding text = %q", cut.Text)
	}

	// The one-line forms indent the continuation under the finding, so a
	// bullet stays the finding's in the comment, and reads back the same.
	s := cut.String()
	if !strings.Contains(s, "Some cases are not covered:\n  - The temp-file name") {
		t.Errorf("String = %q", s)
	}
	if back := Parse(s + "\nCoverage: all").Findings; len(back) != 1 || back[0].Text != cut.Text {
		t.Errorf("String does not read back: %+v", back)
	}
	c := CommentText(&Run{From: "in design", To: "backlog", Report: r})
	if !strings.Contains(c, ":44,64,72 — Some cases are not covered:\n  - The temp-file name") ||
		!strings.Contains(c, "\n  - Nothing says how a 50 MB multipart body is read") {
		t.Errorf("comment loses the bullets:\n%s", c)
	}
}

// Only a lead-in (text ending in ':') takes the bullet lines right after
// it. The critic's prose stays ignored: right after a finding that is
// not a lead-in, and right after a lead-in's bullets. A bullet that
// is a finding, Coverage or Not read is never a case.
func TestParseContinuationOnlyUnderALeadIn(t *testing.T) {
	r := Parse("should a.go:3 — two things:\n- one\n  - two\nshould b.go:4 — next\n\nSome closing prose.\nCoverage: all\nNot read: none")
	if len(r.Findings) != 2 || r.Findings[0].Text != "two things:\n- one\n- two" || r.Findings[1].Text != "next" {
		t.Errorf("findings = %+v", r.Findings)
	}

	r = Parse("blocks a.go:3 — nil on empty\nshould b.go:4 — cite D-2\nOverall the design is sound.\nCoverage: read 2 of 2\nNot read: none")
	if len(r.Findings) != 2 || r.Findings[0].Text != "nil on empty" || r.Findings[1].Text != "cite D-2" || r.Coverage != "read 2 of 2" {
		t.Errorf("prose right after the last finding joined it: %+v", r)
	}

	r = Parse("should a.go:3 — two cases:\n- one\n- two\nOverall the design is sound.\nCoverage: all")
	if len(r.Findings) != 1 || r.Findings[0].Text != "two cases:\n- one\n- two" {
		t.Errorf("prose right after a lead-in's bullets joined it: %+v", r.Findings)
	}

	// A bullet the parser reads as something else stays that: a lead-in
	// never takes a finding (a malformed blocks still holds the gate),
	// Coverage or Not read.
	// A case after such a bullet still joins the lead-in.
	in := "should a.go:3 — cases left open:\n- one\n- blocks the retry path, no file named\n- two\n- should the rename fail, nothing retries\n- three\n- Coverage: the new tests miss X\n- four\nCoverage: read 1 of 1\nNot read: none"
	r = Parse(in)
	if len(r.Findings) != 3 || r.Findings[0].Text != "cases left open:\n- one\n- two\n- three\n- four" || r.Coverage != "read 1 of 1" || len(r.Problems) != 0 {
		t.Fatalf("report = %+v", r)
	}
	if b := r.Findings[1]; b.Severity != Blocks || !b.Malformed || r.Count(Blocks) != 1 || !(&Run{Report: r}).Holds() {
		t.Errorf("a malformed blocks bullet under a lead-in no longer holds: %+v", r.Findings)
	}
	if f := r.Findings[2]; f.Severity != Should || !f.Malformed {
		t.Errorf("should bullet = %+v", f)
	}
	r = Parse("should a.go:3 — cases:\n- one\n- Coverage: the new tests miss X")
	if r.Coverage != "the new tests miss X" || r.Findings[0].Text != "cases:\n- one" || len(r.Problems) != 0 {
		t.Errorf("a Coverage bullet reads as before the lead-in rule: %+v", r)
	}

	r = Parse("- should a.go:3 — two cases:\n- one\n- note b.go:9 — a new finding\nCoverage: all")
	if len(r.Findings) != 2 || r.Findings[0].Text != "two cases:\n- one" || r.Findings[1].Severity != Note {
		t.Errorf("a well-formed bulleted finding did not end the lead-in: %+v", r.Findings)
	}

	r = Parse("intro prose\nshould these cases:\n- one\nshould a.go:3\n  more words\nCoverage: all")
	if f := r.Findings[0]; !f.Malformed || f.Raw != "should these cases:\n- one" {
		t.Errorf("malformed lead-in = %+v", f)
	}
	if len(r.Findings) != 2 || r.Findings[1].Raw != "should a.go:3" {
		t.Errorf("malformed without lead-in took prose: %+v", r.Findings)
	}
}

// A bulleted output whose last finding is a lead-in, with no blank line
// before its "- Coverage:" / "- Not read:": they are the run's Coverage
// and Not read, as before the lead-in rule.
func TestParseBulletedCoverageAfterALeadIn(t *testing.T) {
	r := Parse("- blocks a.go:3 — nil on empty\n- should b.go:4 — two cases:\n- one\n- two\n- Coverage: read 6 of 8\n- Not read: c.go, d.go")
	if len(r.Problems) != 0 || r.Coverage != "read 6 of 8" || strings.Join(r.Unread, ",") != "c.go,d.go" {
		t.Fatalf("report = %+v", r)
	}
	if got := r.Findings[1].Text; got != "two cases:\n- one\n- two" {
		t.Errorf("lead-in text = %q", got)
	}
}

// The transition's refusal puts a blocks finding's bullets one level
// under it, not at the findings' depth.
func TestGateErrorIndentsContinuationUnderTheFinding(t *testing.T) {
	run := &Run{ID: "r1", Report: Parse("blocks a.go:3 — two holes:\n- one\n- two\nblocks b.go:4 — three\nCoverage: all")}
	err := GateError(run, "x", "testing")
	if err == nil {
		t.Fatal("no error for a blocks run")
	}
	if !strings.Contains(err.Error(), "  blocks a.go:3 — two holes:\n    - one\n    - two\n  blocks b.go:4 — three\n") {
		t.Errorf("refusal:\n%s", err)
	}
}
