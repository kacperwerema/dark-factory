package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/retro"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// markSweep logs a sweep whose mark is until, as retro archive does.
func markSweep(t *testing.T, proj *tracker.Project, until time.Time) {
	t.Helper()
	dir := filepath.Join(projectRoot(proj), "retros")
	if _, err := retro.Archive(dir, nil, nil, retro.Sweep{At: until, By: "test", Until: until}); err != nil {
		t.Fatal(err)
	}
}

// AC1: with no retros since the last sweep's mark the tick queues no
// sweep and files no run; it says so once. A retro after the mark gets
// the sweep queued.
func TestLeadTickQueuesNoSweepWithNoRetrosSinceTheMark(t *testing.T) {
	proj := sweepProject(t) // one retro, 2026-11-01T09:00:00Z
	autostartOnFor(t, proj)
	yoloOnFor(t, proj)
	mustQ(t, proj, "add", "high", "--quote", queueWords)
	mark := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	markSweep(t, proj, mark)

	out := tick(t, proj)
	assertContains(t, out, "queue low: no retros since 2026-11-02T00:00:00Z, no sweep queued")
	assertNotContains(t, out, "queued sweep")
	if items := sweepItems(t, proj); len(items) != 0 {
		t.Fatalf("queued %+v with no retros", items)
	}
	if m, _ := filepath.Glob(filepath.Join(proj.IssueDir, "Product", "*.md")); len(m) != 0 {
		t.Fatalf("filed %v with no retros", m)
	}
	if out := tick(t, proj); strings.Contains(out, "no retros") {
		t.Fatalf("said twice:\n%s", out)
	}

	retroFile(t, proj, "20261103-090000-r2.md", "", "2026-11-03T09:00:00Z", "Base workflow: slow")
	out = tick(t, proj)
	assertContains(t, out, "queued sweep product/queue-sweep-"+time.Now().Format("2006-01-02")+" as item 2")
	if items := sweepItems(t, proj); len(items) != 1 {
		t.Fatalf("queued %+v with a retro since the mark", items)
	}
}

// AC1: no sweep ever logged and retros/ empty: the line says the start.
func TestLeadTickQueuesNoSweepWithAnEmptyRetros(t *testing.T) {
	proj := sweepProject(t)
	os.RemoveAll(filepath.Join(projectRoot(proj), "retros"))
	autostartOnFor(t, proj)
	yoloOnFor(t, proj)
	mustQ(t, proj, "add", "high", "--quote", queueWords)
	out := tick(t, proj)
	assertContains(t, out, "queue low: no retros since the start (no sweep recorded yet), no sweep queued")
	if items := sweepItems(t, proj); len(items) != 0 {
		t.Fatalf("queued %+v", items)
	}
}

var retrosHeadingRE = regexp.MustCompile(`== Retros since the last sweep \(([^)]+)\) ==`)

// splitWords splits a brief's command line as a shell would its quoted
// words (no expansion: the caller has expanded what it needs).
func splitWords(line string) []string {
	var out []string
	var cur strings.Builder
	quote, in := byte(0), false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0 && c == quote:
			quote = 0
		case quote != 0:
			cur.WriteByte(c)
		case c == '"' || c == '\'':
			quote, in = c, true
		case c == ' ':
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteByte(c)
			in = true
		}
	}
	if in {
		out = append(out, cur.String())
	}
	return out
}

// AC2: the retro-sweep brief's no-retros path, walked as a worker does on
// a report issue: retro list prints none, the brief's commands shelve a
// one-line report and post the report: comment, and the report gates let
// the run reach human-testing.
func TestRetroSweepBriefNoRetrosPathReachesHumanTesting(t *testing.T) {
	proj, slug, issuePath := reportProject(t)
	now := time.Now()
	yoloOn(t, proj, now, time.Hour)
	retroFile(t, proj, "20261101-090000-r1.md", "", "2026-11-01T09:00:00Z", "Base workflow: fine")
	markSweep(t, proj, time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC))

	if _, _, err := runYOLOTransition(t, proj, now, slug, "--to", "in progress"); err != nil {
		t.Fatalf("idea → in progress: %v", err)
	}

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runRetro(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	list := stdout.String()
	assertContains(t, list, "  none")
	m := retrosHeadingRE.FindStringSubmatch(list)
	if m == nil {
		t.Fatalf("no mark in list's heading:\n%s", list)
	}
	mark := m[1]

	text, err := jobs.BriefText(proj, tracker.Job{Brief: jobs.QueueSweepBrief})
	if err != nil {
		t.Fatal(err)
	}
	_, step, ok := strings.Cut(text, `If it prints "none"`)
	if !ok {
		t.Fatalf("the brief has no no-retros step:\n%s", text)
	}
	step, _, _ = strings.Cut(step, "\n2. ")
	tmp := t.TempDir()
	var ran []string
	for _, line := range strings.Split(step, "\n") {
		if !strings.HasPrefix(line, "       ") {
			continue
		}
		line = strings.TrimSpace(line)
		line = strings.NewReplacer("{{slug}}", slug, "{{period}}", "2026-11-22", "<mark>", mark).Replace(line)
		if strings.HasPrefix(line, "printf ") {
			cmd := exec.Command("sh", "-c", line)
			cmd.Env = append(os.Environ(), "TMPDIR="+tmp)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", line, err, out)
			}
			ran = append(ran, "printf")
			continue
		}
		words := splitWords(strings.ReplaceAll(line, "${TMPDIR:-/tmp}", tmp))
		if len(words) < 2 || words[0] != "issue-cli" {
			t.Fatalf("not an issue-cli line in the no-retros step: %q", line)
		}
		c := lookupCommand(words[1])
		if c == nil {
			t.Fatalf("unknown command in %q", line)
		}
		ctx, out, _ := newTestContext(proj, false)
		if err := c.Run(ctx, words[2:]); err != nil {
			t.Fatalf("%s: %v\n%s", line, err, out.String())
		}
		ran = append(ran, words[1])
	}
	if strings.Join(ran, ",") != "printf,output,comment" {
		t.Fatalf("the no-retros step ran %v, want printf, output add, comment", ran)
	}

	list2, _ := outputs.Load(proj)
	var shelved *outputs.Output
	for _, o := range list2 {
		if o.Issue == slug && o.Kind == "report" {
			shelved = o
		}
	}
	if shelved == nil {
		t.Fatalf("no report on the shelf for %s: %+v", slug, list2)
	}
	assertContains(t, lastComment(t, proj, slug), "report: no retros since "+mark+"; follow-ups: none")

	tickSection(t, proj, slug, issuePath, "Research")
	if _, _, err := runYOLOTransition(t, proj, now, slug, "--to", "human-testing"); err != nil {
		t.Fatalf("in progress → human-testing: %v", err)
	}
	assertContains(t, readFile(t, issuePath), "status: \"human-testing\"")
}

// AC3: the brief and the job docs say the no-retros rule.
func TestNoRetrosSweepRuleIsInTheBriefAndDocs(t *testing.T) {
	text, err := jobs.BriefText(&tracker.Project{}, tracker.Job{Brief: jobs.QueueSweepBrief})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, text, `report: no retros since <mark>; follow-ups: none`)
	assertNotContains(t, text, "progress: no retros")
	for _, f := range []string{"../../docs/agent-dispatch.md", "../../docs/patterns/lead-session.md"} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := string(data)
		for _, want := range []string{"queue low: no retros since <mark>, no sweep queued", "report: no retros since <mark>; follow-ups: none"} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: no %q", f, want)
			}
		}
	}
}
