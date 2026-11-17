package main

import (
	"strings"
	"testing"
)

// runDecisionsOut runs decisions with args and returns stdout, or fails.
func runDecisionsOut(t *testing.T, ctx *Context, args ...string) string {
	t.Helper()
	ctx.Stdout.(*strings.Builder).Reset()
	if err := runDecisions(ctx, args); err != nil {
		t.Fatalf("decisions %q: %v", args, err)
	}
	return ctx.Stdout.(*strings.Builder).String()
}

// TestDecisionsFlagsTakeTheSessionIssue: in a session (ISSUE_VIEWER_ISSUE_SLUG
// set) --in-force and --touched with no value, or with the empty value the
// briefing's "$ISSUE_VIEWER_ISSUE_SLUG" expands to elsewhere, print what the
// same flag with the session's slug prints. A flag after the bare one stays
// a flag, and a slug given wins.
func TestDecisionsFlagsTakeTheSessionIssue(t *testing.T) {
	proj := makeRespectedProject(t, "We change the store.") // session issue: work
	mustWrite(t, proj.IssueDir+"/other.md", "---\ntitle: \"Other\"\nstatus: \"backlog\"\n---\n\nOther.\n")
	for _, flag := range []string{"--in-force", "--touched"} {
		for _, json := range []bool{false, true} {
			ctx, _, _ := newTestContext(proj, json)
			ctx.Stdout = &strings.Builder{}
			want := runDecisionsOut(t, ctx, flag, "work")
			if want == "" {
				t.Fatalf("%s work printed nothing", flag)
			}
			for _, args := range [][]string{{flag}, {flag, ""}, {flag + "="}, {flag, "--"}} {
				if got := runDecisionsOut(t, ctx, args...); got != want {
					t.Errorf("decisions %q (json %v):\n%s\nwant what %s work prints:\n%s", args, json, got, flag, want)
				}
			}
		}
	}

	ctx, _, _ := newTestContext(proj, false)
	ctx.Stdout = &strings.Builder{}
	want := runDecisionsOut(t, ctx, "--in-force", "work", "--status", "in design")
	if got := runDecisionsOut(t, ctx, "--in-force", "--status", "in design"); got != want {
		t.Errorf("--in-force --status: a flag after the bare one is a flag:\n%s\nwant:\n%s", got, want)
	}
	if got := runDecisionsOut(t, ctx, "--in-force", "other"); !strings.Contains(got, "for other") {
		t.Errorf("a slug given wins over the session's:\n%s", got)
	}
}

// TestDecisionsFlagWithoutASlugAsksForOne: with no session and no value the
// flags ask for the slug and name the flag, never "issue not found: --".
func TestDecisionsFlagWithoutASlugAsksForOne(t *testing.T) {
	proj := makeRespectedProject(t, "We change the store.")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	for _, flag := range []string{"--in-force", "--touched"} {
		for _, args := range [][]string{{flag}, {flag, ""}, {flag + "="}, {flag, "--json"}} {
			ctx, _, _ := newTestContext(proj, false)
			err := runDecisions(ctx, args)
			if err == nil {
				t.Fatalf("decisions %q: no error", args)
			}
			msg := err.Error()
			if strings.Contains(msg, "not found") || !strings.Contains(msg, "give the issue's slug") || !strings.Contains(msg, flag+" <slug>") {
				t.Errorf("decisions %q: %q, want it to ask for the slug and name %s", args, msg, flag)
			}
		}
	}
}
