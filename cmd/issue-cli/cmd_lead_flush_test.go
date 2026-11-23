package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/leadstate"
	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/ownerstep"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const flushRunSlug = "lead-flush-2026-10-29"

// goodNote is a note the flush may write, padded to lines lines.
func goodNote(lines int) string {
	var b strings.Builder
	for _, h := range tracker.HandoffHeadings {
		b.WriteString("## " + h + "\n\nThe " + h + " of the day.\n")
	}
	for strings.Count(b.String(), "\n") < lines {
		b.WriteString("More of the day.\n")
	}
	return b.String()
}

// makeFlushProject is a project whose Product entry has the lead-flush job,
// with today's run written and this process its worker. A stand-in lead is
// live until close kills it, and close's start of it again is recorded as
// "spawn <lead>" after the kill; the pane reads pane.
func makeFlushProject(t *testing.T, pane string) (*tracker.Project, *[]string) {
	t.Helper()
	proj := makeJobProject(t)
	proj.Product = &tracker.Department{Name: tracker.ProductName, Jobs: []tracker.Job{{
		ID: "lead-flush", Name: "Lead flush", Every: "day", At: "23:30", Brief: jobs.LeadFlushBrief,
	}}}
	body := "---\ntitle: \"Lead flush · 2026-10-29\"\nstatus: \"in progress\"\ncreated: \"2026-10-29\"\nsystem: \"Product\"\njob: \"product/lead-flush\"\nrun: \"2026-10-29\"\nslug: \"" + flushRunSlug + "\"\n---\n\nBrief.\n"
	if err := os.WriteFile(filepath.Join(proj.IssueDir, flushRunSlug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", flushRunSlug)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	live := map[string]bool{agentrun.Target(proj, tracker.LeadSessionName(proj)): true}
	var killed []string
	origHas, origPane, origKill := agentrun.HasSession, agentrun.CapturePane, closeLeadSession
	agentrun.HasSession = func(target string) bool { return live[target] }
	agentrun.CapturePane = func(string) (string, error) { return pane, nil }
	closeLeadSession = func(_ *tracker.Project, name string) error {
		killed = append(killed, name)
		delete(live, agentrun.Target(proj, name))
		return nil
	}
	origSpawn := leadFlushSpawn
	leadFlushSpawn = func(ctx *Context) error {
		lead := tracker.LeadSessionName(ctx.Project)
		killed = append(killed, "spawn "+lead)
		live[agentrun.Target(proj, lead)] = true
		return nil
	}
	t.Cleanup(func() {
		agentrun.HasSession, agentrun.CapturePane, closeLeadSession, leadFlushSpawn = origHas, origPane, origKill, origSpawn
	})
	return proj, &killed
}

func runFlush(t *testing.T, proj *tracker.Project, args ...string) (string, error) {
	t.Helper()
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return time.Date(2026, 10, 30, 0, 5, 0, 0, time.Local) } // past midnight
	err := runLead(ctx, append([]string{"flush"}, args...))
	return stdout.String(), err
}

func writeTemp(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "in.md")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The whole run with stand-ins for the lead's session: note under the cap,
// summary, then the close. The files carry the run's day, not the clock's.
func TestLeadFlush_RunWritesNoteAndSummaryThenClosesTheLead(t *testing.T) {
	proj, killed := makeFlushProject(t, "idle prompt\n> ")
	lead := tracker.LeadSessionName(proj)
	note := filepath.Join(proj.WorkDir, "sessions", "2026-10-29-"+lead+".md")
	sum := filepath.Join(proj.WorkDir, "sessions", "2026-10-29-"+lead+".summary.md")

	if _, err := runFlush(t, proj, "note", "--run", flushRunSlug, "--file", writeTemp(t, goodNote(tracker.HandoffNoteMaxLines))); err != nil {
		t.Fatal(err)
	}
	if _, err := runFlush(t, proj, "summary", "--run", flushRunSlug, "--file", writeTemp(t, "The owner asked for the flush.")); err != nil {
		t.Fatal(err)
	}
	out, err := runFlush(t, proj, "close", "--run", flushRunSlug)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Closed "+lead) || strings.Join(*killed, ",") != lead+",spawn "+lead {
		t.Fatalf("close then start the lead again: out %q, events %v", out, *killed)
	}
	data, err := os.ReadFile(note)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; n > tracker.HandoffNoteMaxLines {
		t.Fatalf("note is %d lines", n)
	}
	if got, err := os.ReadFile(sum); err != nil || !strings.Contains(string(got), "asked for the flush") {
		t.Fatalf("summary %q, %v", got, err)
	}
	// The fresh lead is live: a second close would close it again, so
	// only a flush with no lead says there is nothing to close.
	closeLeadSession(proj, lead)
	if out, err := runFlush(t, proj, "close", "--run", flushRunSlug); err != nil || !strings.Contains(out, "No lead live") {
		t.Fatalf("second close: %q, %v", out, err)
	}
}

func TestLeadFlushCloseRefusesWithoutNote(t *testing.T) {
	proj, killed := makeFlushProject(t, "> ")
	_, err := runFlush(t, proj, "close", "--run", flushRunSlug)
	if err == nil || !strings.Contains(err.Error(), "stays open") {
		t.Fatalf("close with no note: %v", err)
	}
	if _, err := runFlush(t, proj, "note", "--run", flushRunSlug, "--file", writeTemp(t, goodNote(10))); err != nil {
		t.Fatal(err)
	}
	if _, err := runFlush(t, proj, "close", "--run", flushRunSlug); err == nil || !strings.Contains(err.Error(), ".summary.md") {
		t.Fatalf("close with no summary: %v", err)
	}
	if len(*killed) != 0 {
		t.Fatalf("killed %v", *killed)
	}
}

func TestLeadFlushCloseWaitsForATurn(t *testing.T) {
	proj, killed := makeFlushProject(t, "chat\n✻ Pondering… (32s · esc to interrupt)\n\n> ")
	for _, c := range [][]string{{"note", "--file", writeTemp(t, goodNote(10))}, {"summary", "--file", writeTemp(t, "Nothing.")}} {
		if _, err := runFlush(t, proj, append(c, "--run", flushRunSlug)...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runFlush(t, proj, "close", "--run", flushRunSlug); err == nil || !strings.Contains(err.Error(), "middle of a turn") {
		t.Fatalf("close mid turn: %v", err)
	}
	if len(*killed) != 0 {
		t.Fatalf("killed %v", *killed)
	}
}

func TestLeadFlushNoteRefusals(t *testing.T) {
	proj, _ := makeFlushProject(t, "> ")
	for name, tc := range map[string]struct{ text, want string }{
		"over the cap":    {goodNote(tracker.HandoffNoteMaxLines + 5), "the cap is 80"},
		"missing section": {strings.Replace(goodNote(10), "## Rules in force", "## Other", 1), "## Rules in force"},
		"a copied state":  {goodNote(10) + "## Queue\n\n- #7 the queued one\n", "renders workers, the queue and the owner's asks live"},
		"secret":          {goodNote(10) + "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789\n", "secret"},
	} {
		_, err := runFlush(t, proj, "note", "--run", flushRunSlug, "--file", writeTemp(t, tc.text))
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "Nothing was written") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(proj.WorkDir, "sessions")); err == nil {
		t.Fatal("a refused note wrote something")
	}
}

func TestLeadFlushOnlyItsOwnRun(t *testing.T) {
	proj, _ := makeFlushProject(t, "> ")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "high")
	if _, err := runFlush(t, proj, "pack", "--run", flushRunSlug); err == nil || !strings.Contains(err.Error(), "not your run") {
		t.Fatalf("another worker: %v", err)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "high")
	if _, err := runFlush(t, proj, "close", "--run", "high"); err == nil || !strings.Contains(err.Error(), "not a recurring job's run") {
		t.Fatalf("not a run: %v", err)
	}
}

func TestLeadFlushPackReadsTheLeadsTranscript(t *testing.T) {
	proj, _ := makeFlushProject(t, "> ")
	lead := tracker.LeadSessionName(proj)
	folder := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", strings.NewReplacer("/", "-", ".", "-", "_", "-").Replace(proj.WorkDir))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(typ, at string, content any) string {
		data, _ := json.Marshal(map[string]any{"type": typ, "timestamp": at, "message": map[string]any{"content": content}})
		return string(data)
	}
	recent := time.Date(2026, 10, 29, 20, 0, 0, 0, time.Local).Format(time.RFC3339)
	old := time.Date(2026, 10, 20, 20, 0, 0, 0, time.Local).Format(time.RFC3339)
	write := func(name, agent string, lines ...string) {
		text := `{"type":"agent-name","agentName":"` + agent + `"}` + "\n" + strings.Join(lines, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(folder, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.jsonl", lead,
		line("user", recent, "Keep the flush short"),
		line("assistant", recent, []map[string]string{{"type": "text", "text": "Will do"}, {"type": "tool_use"}}),
		line("user", recent, []map[string]any{{"type": "tool_result", "content": "tool noise"}}),
		line("user", recent, "<system-reminder>harness noise</system-reminder>"),
		line("user", old, "Last week's words"))
	write("b.jsonl", "agent-someone", line("user", recent, "A worker's chat"))

	out, err := runFlush(t, proj, "pack", "--run", flushRunSlug)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"owner: Keep the flush short", "lead: Will do", "transcript", "2026-10-29-" + lead + ".md"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	for _, not := range []string{"tool noise", "harness noise", "Last week", "A worker's chat"} {
		if strings.Contains(out, not) {
			t.Errorf("%q in\n%s", not, out)
		}
	}
}

func TestLeadSpawnPromptCarriesNoteSummaryAndPins(t *testing.T) {
	proj, _ := makeFlushProject(t, "> ")
	lead := tracker.LeadSessionName(proj)
	dir := filepath.Join(proj.WorkDir, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"2026-10-28-" + lead + ".md":         "OLD NOTE",
		"2026-10-28-" + lead + ".summary.md": "OLD SUMMARY",
		"2026-10-29-" + lead + ".md":         "NEWEST NOTE",
		"2026-10-29-" + lead + ".summary.md": "NEWEST SUMMARY",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store := memory.ProjectStore(proj.IssueDir)
	res, err := store.Add(memory.Draft{Text: "Pinned fact for every lead", Kind: memory.KindFact, Scope: memory.ScopeProject, Author: "t"}, memory.AddOptions{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Pin(res.Memory.ID, "t", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	spec := agentrun.LeadSpec(proj, proj.LoadWorkflow(), proj.WorkDir, jobNow)
	for _, want := range []string{"NEWEST NOTE", "NEWEST SUMMARY", "Pinned fact for every lead", "Where the last lead left off"} {
		if !strings.Contains(spec.Prompt, want) {
			t.Errorf("no %q in the first prompt", want)
		}
	}
	for _, not := range []string{"OLD NOTE", "OLD SUMMARY"} {
		if strings.Contains(spec.Prompt, not) {
			t.Errorf("%q in the first prompt", not)
		}
	}
}

func TestLeadFlushBriefRules(t *testing.T) {
	text, err := jobs.BriefText(nil, tracker.Job{Brief: jobs.LeadFlushBrief})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"flush pack --run {{slug}}", "flush note --run {{slug}}", "flush summary --run {{slug}}", "memory consolidate", "flush close --run {{slug}}",
		"start nothing", "send nothing", "decide nothing"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in the brief", want)
		}
	}
	// The note's sections are the three the note command insists on, and
	// the brief asks for no copy of what the state file renders live.
	step := text[strings.Index(text, "2. **Write the note**"):strings.Index(text, "3. **Write the summary**")]
	only := step[strings.Index(step, "these sections only"):strings.Index(step, ". No ")]
	for _, h := range tracker.HandoffHeadings {
		if !strings.Contains(only, h) {
			t.Errorf("the brief's sections don't name %s: %q", h, only)
		}
	}
	for _, h := range tracker.LegacyHandoffHeadings {
		if strings.Contains(only, h) {
			t.Errorf("the brief still asks for %s: %q", h, only)
		}
		if !strings.Contains(step, "No State, Queue, Waiting on the owner or How to resume") {
			t.Errorf("the brief doesn't say the note leaves %s out:\n%s", h, step)
		}
	}
	if !strings.Contains(step, "live from the state file") {
		t.Errorf("the brief doesn't say where workers and the queue are:\n%s", step)
	}
	if got := jobs.BuiltinType(jobs.LeadFlushBrief); got != tracker.ChoreType {
		t.Fatalf("type %q", got)
	}
}

// The bundled template's chore type walks a run to done by itself: no
// approval, no commit, no post-mortem.
func TestChoreRunClosesItselfWithoutAPostmortem(t *testing.T) {
	proj, _ := makeFlushProject(t, "> ")
	tmpl, err := os.ReadFile("templates/workflow/development.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj.WorkDir, "workflow.yaml"), []byte(tmpl), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := jobs.Find(proj, "product/lead-flush")
	if err != nil {
		t.Fatal(err)
	}
	wf := proj.LoadWorkflow()
	if typ, err := jobs.RunType(wf, r.Job); err != nil || typ != tracker.ChoreType {
		t.Fatalf("type %q, %v", typ, err)
	}
	path := filepath.Join(proj.IssueDir, flushRunSlug+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(string(data), "status: \"in progress\"", "status: \"idea\"\ntype: \"chore\"\nassignee: \"agent-"+flushRunSlug+"\"", 1)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	do := func(run func(*Context, []string) error, args ...string) string {
		t.Helper()
		ctx, stdout, _ := newTestContext(proj, false)
		if err := run(ctx, args); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, stdout)
		}
		return stdout.String()
	}
	do(runTransition, flushRunSlug, "--to", "in progress")
	do(runComment, flushRunSlug, "progress: flushed, lead closed")
	do(runCheck, flushRunSlug, "Every step of the brief done, or why it stopped named in a progress: comment")
	if out := do(runTransition, flushRunSlug, "--to", "done"); strings.Contains(out, "Post-mortem") {
		t.Fatalf("a chore's close offered a post-mortem:\n%s", out)
	}
}

// The lead flush tidies memory for the next lead, so its worker reads the
// lead scope (list, search, show, consolidate) and changes none of it; any
// other worker still reads none of it.
func TestLeadFlushReadsTheLeadScopeAndChangesNone(t *testing.T) {
	memoryAs(t, "")
	proj, _ := makeFlushProject(t, "")
	other := "---\ntitle: \"Engine fix\"\nstatus: \"in progress\"\nslug: \"engine-fix\"\n---\n\nBrief.\n"
	if err := os.WriteFile(filepath.Join(proj.IssueDir, "engine-fix.md"), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "") // the owner writes the rule
	ctx, out := goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"add", "--scope", "lead", "--kind", "convention", "hold new starts at the stop point"}); err != nil {
		t.Fatal(err)
	}
	m := onlyMemory(t, proj)
	if err := runMemory(ctx, []string{"pin", m.ID}); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", flushRunSlug)
	for _, args := range [][]string{{"list", "--scope", "lead"}, {"list", "--pinned"}, {"search", "stop", "point"}, {"show", m.ID}} {
		out.Reset()
		if err := runMemory(ctx, args); err != nil {
			t.Fatalf("flush %v: %v", args, err)
		}
		if !strings.Contains(out.String(), "hold new starts") && !strings.Contains(out.String(), m.ID) {
			t.Errorf("the flush %v doesn't read it:\n%s", args, out.String())
		}
	}
	for _, args := range [][]string{
		{"edit", m.ID, "--text", "hold new starts at 90%"},
		{"forget", m.ID, "--reason", "stale"},
		{"add", "hold new starts at 90%", "--scope", "lead", "--supersedes", m.ID},
		{"add", "hold new starts at the stop point, always", "--scope", "lead", "--updates", m.ID},
		{"pin", m.ID},
	} {
		err := runMemory(ctx, args)
		if err == nil || !(strings.Contains(err.Error(), "only a lead or the owner changes") || strings.Contains(err.Error(), "only leads and the owner")) {
			t.Errorf("flush %v = %v, want refused", args, err)
		}
	}
	if got := onlyMemory(t, proj); got.Text != m.Text || got.State(ctx.now()) != "valid" || !got.Pinned {
		t.Fatalf("the flush changed it: %+v", got)
	}

	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "engine-fix")
	for _, args := range [][]string{{"list", "--scope", "lead"}, {"list", "--pinned"}, {"search", "stop", "point"}, {"consolidate"}} {
		out.Reset()
		if err := runMemory(ctx, args); err != nil {
			t.Fatalf("worker %v: %v", args, err)
		}
		if strings.Contains(out.String(), "hold new starts") {
			t.Errorf("another worker %v read it:\n%s", args, out.String())
		}
	}
	if err := runMemory(ctx, []string{"show", m.ID}); err == nil || !strings.Contains(err.Error(), "can't read") {
		t.Errorf("another worker show = %v", err)
	}
	if err := runMemory(ctx, []string{"forget", m.ID, "--reason", "stale"}); err == nil || !strings.Contains(err.Error(), "only a lead or the owner changes") {
		t.Errorf("another worker forget = %v", err)
	}

	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", tracker.LeadScopeProject)
	if err := runMemory(ctx, []string{"edit", m.ID, "--text", "hold new starts at the stop point (98%)"}); err != nil {
		t.Fatalf("the lead edits it: %v", err)
	}
}

// Proves: AC6 — the open owner steps reach the next lead: lead flush pack
// prints each with its id, due line and title and the command for their
// text, and the rotate's handoff note lists them under ## Waiting on the
// owner; a ticked step is in neither.
func TestOwnerStepInHandoffNote(t *testing.T) {
	proj, _ := makeFlushProject(t, "> ")
	now := time.Date(2026, 10, 30, 0, 5, 0, 0, time.Local)
	add := func(id, title, due string, repeat int) {
		t.Helper()
		ev := ownerstep.Event{Op: ownerstep.OpAdd, ID: id, At: now.AddDate(0, 0, -3).Format(time.RFC3339), Title: title, Body: "exact text", Due: due, Repeat: repeat}
		if err := ownerstep.Append(proj.IssueDir, ev); err != nil {
			t.Fatal(err)
		}
	}
	add("st-111111", "Reinstall the phone build", "2026-10-29", 7)
	add("st-222222", "Request indexing", "2026-11-02", 0)
	add("st-333333", "Already done", "2026-10-30", 0)
	if err := ownerstep.Append(proj.IssueDir, ownerstep.Event{Op: ownerstep.OpDone, ID: "st-333333", At: now.Format(time.RFC3339), By: "owner"}); err != nil {
		t.Fatal(err)
	}

	out, err := runFlush(t, proj, "pack", "--run", flushRunSlug)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"== Open owner steps",
		"- step st-111111 · overdue: was due 2026-10-29 (1 day ago) · repeats 7d: Reinstall the phone build",
		"- step st-222222 · due 2026-11-02: Request indexing",
		"owner-step list",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("pack: no %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "st-333333") {
		t.Errorf("pack lists a ticked step:\n%s", out)
	}

	steps, err := ownerstep.LoadOpen(proj.IssueDir, now)
	if err != nil {
		t.Fatal(err)
	}
	v := leadstate.View{Lead: "lead-x", At: now, OwnerSteps: ownerstep.Lines(steps, now), Commands: leadstate.Commands{OwnerSteps: "issue-cli owner-step list"}}
	// The steps are the state file's (its Owner asks, which the fresh
	// lead's brief renders live); the note copies none of them.
	state := leadstate.Render(v, leadstate.Store{})
	asks := state[strings.Index(state, "## Owner asks"):]
	asks = asks[:strings.Index(asks, "## Today")]
	for _, want := range []string{"- step st-111111 · overdue", "- step st-222222 · due 2026-11-02: Request indexing", "- the steps' exact text: issue-cli owner-step list"} {
		if !strings.Contains(asks, want) {
			t.Errorf("state: no %q in\n%s", want, asks)
		}
	}
	note := leadstate.HandoffNote(v, leadstate.Store{}, leadstate.Builds{}, "issue-cli lead state")
	if strings.Contains(note, "st-111111") {
		t.Errorf("the note copies the owner steps:\n%s", note)
	}
	if err := tracker.CheckHandoffNote(note); err != nil {
		t.Errorf("the rotate note fails its own check: %v", err)
	}
}

// The close rotates each department or group lead that ran 20 h or more
// (a marketing lead ran 8 days because the flush closed only the CEO),
// leaves a younger one and a department with no live lead alone, and a
// failed rotate is said without failing the close.
func TestLeadFlushCloseRotatesOldDepartmentLeads(t *testing.T) {
	proj, killed := makeFlushProject(t, "idle prompt\n> ")
	proj.Departments = []tracker.Department{{Name: "Marketing"}, {Name: "Sales"}, {Name: "GTM"}, {Name: "Support"}}
	lead := tracker.LeadSessionName(proj)
	marketing := tracker.DepartmentLeadName(proj, "Marketing")
	sales := tracker.DepartmentLeadName(proj, "Sales")
	gtm := tracker.DepartmentLeadName(proj, "GTM")
	now := time.Date(2026, 10, 30, 0, 5, 0, 0, time.Local)
	started := map[string]time.Time{marketing: now.AddDate(0, 0, -8), sales: now.Add(-2 * time.Hour), gtm: now.Add(-30 * time.Hour)}
	origHas := agentrun.HasSession
	agentrun.HasSession = func(target string) bool {
		for name := range started {
			if target == agentrun.Target(proj, name) {
				return true
			}
		}
		return origHas(target)
	}
	origStarted, origRotate := leadSessionStarted, leadFlushRotate
	leadSessionStarted = func(_ *tracker.Project, name string) (time.Time, bool) { at, ok := started[name]; return at, ok }
	var rotated []string
	leadFlushRotate = func(_ *Context, name string, _ []*tracker.Issue, noStart bool) error {
		if noStart {
			t.Errorf("%s rotated with --no-start: it must come back", name)
		}
		rotated = append(rotated, name)
		if name == gtm {
			return os.ErrPermission
		}
		return nil
	}
	t.Cleanup(func() { agentrun.HasSession, leadSessionStarted, leadFlushRotate = origHas, origStarted, origRotate })

	runFlush(t, proj, "note", "--run", flushRunSlug, "--file", writeTemp(t, goodNote(10)))
	runFlush(t, proj, "summary", "--run", flushRunSlug, "--file", writeTemp(t, "A day."))
	out, err := runFlush(t, proj, "close", "--run", flushRunSlug)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(*killed, ",") != lead+",spawn "+lead {
		t.Errorf("the CEO: %v", *killed)
	}
	if strings.Join(rotated, ",") != marketing+","+gtm {
		t.Errorf("rotated %v, want %s and %s (Sales is 2 h old, Support has no live lead)", rotated, marketing, gtm)
	}
	if !strings.Contains(out, "! "+gtm+" not rotated") || !strings.Contains(out, "lead rotate --lead "+gtm) {
		t.Errorf("a failed rotate isn't said with its command:\n%s", out)
	}
}
