package main

import (
	"encoding/json"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/spend"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// filesUnder lists every file under dir with its size and mtime, so a
// call that writes anything there shows up as a difference.
func filesUnder(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			out[p] = info.ModTime().String() + " " + strconv.FormatInt(info.Size(), 10)
		}
		return nil
	})
	return out
}

// inWorkerShell makes this test's shell a worker's, as a dispatched
// worker's is: its slug in the env and its tmux name.
func inWorkerShell(t *testing.T, slug string) {
	t.Helper()
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", slug)
	orig := currentTmuxNames
	currentTmuxNames = func() (string, string) { return tracker.AgentSessionName(slug), "" }
	t.Cleanup(func() { currentTmuxNames = orig })
}

// noStart fails the test when anything starts an agent.
func noStart(t *testing.T) {
	t.Helper()
	orig := startAgent
	startAgent = func(_ *tracker.Project, spec agentrun.Spec) agentrun.Response {
		t.Errorf("lead brief started %s", spec.Session)
		return agentrun.Response{Status: "error"}
	}
	t.Cleanup(func() { startAgent = orig })
}

// AC1: from a worker's shell, lead brief --render prints the brief a fresh
// lead gets (the lead's role, not the worker's), starting and writing
// nothing.
func TestLeadBriefRender_FromAWorkerShellPrintsTheLeadsBrief(t *testing.T) {
	proj := makeLeadProject(t)
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	inWorkerShell(t, "ui/child")
	noStart(t)
	if me := whoAmI(proj, nil); me.Role != tracker.RoleWorker {
		t.Fatalf("stand-in shell is %s, want a worker's", me.Role)
	}
	before, beforeState := filesUnder(t, projectRoot(proj)), filesUnder(t, state)

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runLead(ctx, []string{"brief", "--render"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	want := agentrun.LeadSpec(proj, proj.LoadWorkflow(), projectRoot(proj), ctx.now()).Prompt
	if strings.TrimRight(out, "\n") != strings.TrimRight(want, "\n") {
		t.Errorf("render is not lead spawn's prompt:\n%s", out)
	}
	if !strings.Contains(out, "- Role: project lead (lead-proj).") {
		t.Errorf("render lacks the lead's role line:\n%s", out)
	}
	if strings.Contains(out, "Role: worker") || strings.Contains(out, "agent-ui-child") {
		t.Errorf("render carries the worker's identity:\n%s", out)
	}
	if after := filesUnder(t, projectRoot(proj)); !maps.Equal(before, after) {
		t.Errorf("render wrote into the project: %d files before, %d after", len(before), len(after))
	}
	if after := filesUnder(t, state); !maps.Equal(beforeState, after) {
		t.Errorf("render wrote state: %v", after)
	}

	// Without --render or --sections it says both.
	ctx, _, _ = newTestContext(proj, false)
	if err := runLead(ctx, []string{"brief"}); err == nil || !strings.Contains(err.Error(), "--render") || !strings.Contains(err.Error(), "--sections") {
		t.Errorf("bare lead brief: err = %v", err)
	}
}

// AC2: --sections names each section as spend --context does (its
// LeadSectionNames, in that order), each at chars/2.7, adding up to the
// brief.
func TestLeadBriefSections_NamesAsSpendContext(t *testing.T) {
	proj := makeLeadProject(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	inWorkerShell(t, "ui/child")
	noStart(t)

	ctx, stdout, _ := newTestContext(proj, true)
	if err := runLead(ctx, []string{"brief", "--render", "--sections"}); err != nil {
		t.Fatal(err)
	}
	var got leadBriefSectionsJSON
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	prompt := agentrun.LeadSpec(proj, proj.LoadWorkflow(), projectRoot(proj), ctx.now()).Prompt
	if got.Kind != spend.BriefLead || got.Session != "lead-proj" || got.Chars != len(prompt) {
		t.Fatalf("kind %q session %q chars %d (prompt %d)", got.Kind, got.Session, got.Chars, len(prompt))
	}
	_, want := spend.SplitBriefText(prompt)
	sum, last := 0, -1
	var names []string
	for i, s := range got.Sections {
		names = append(names, s.Name)
		at := slices.Index(spend.LeadSectionNames, s.Name)
		if at <= last {
			t.Errorf("section %q is no spend --context lead section in order (%v)", s.Name, spend.LeadSectionNames)
		}
		last = at
		if i >= len(want) || s != want[i] {
			t.Errorf("section %d = %+v, spend's split says %+v", i, s, want)
		}
		if s.Tokens != spend.EstimateTokens(s.Chars) || s.Tokens != int64(float64(s.Chars)/2.7+0.5) {
			t.Errorf("%s: %d tokens for %d chars, want chars/2.7", s.Name, s.Tokens, s.Chars)
		}
		sum += s.Chars
	}
	for _, must := range []string{spend.SectionRole, spend.SectionRules} {
		if !slices.Contains(names, must) {
			t.Errorf("sections %v lack %s", names, must)
		}
	}
	if sum != got.Chars {
		t.Errorf("sections add up to %d chars, brief %d", sum, got.Chars)
	}

	// The text form prints the same names, one line each, and a total.
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runLead(ctx, []string{"brief", "--sections"}); err != nil {
		t.Fatal(err)
	}
	for _, n := range append(names, "total") {
		if !strings.Contains(stdout.String(), "\n  "+n+" ") {
			t.Errorf("text form lacks %q:\n%s", n, stdout)
		}
	}
}
