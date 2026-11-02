package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/connections"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// withParkWorkflow gives proj a workflow idea → in progress → done with a
// parked status every status moves to (reason required).
func withParkWorkflow(t *testing.T, proj *tracker.Project) *tracker.Project {
	t.Helper()
	path := filepath.Join(proj.WorkDir, "workflow.yaml")
	mustWrite(t, path, "statuses:\n  - name: \"idea\"\n  - name: \"in progress\"\n  - name: \"done\"\n  - name: \"parked\"\n    optional: true\n    global: true\ntransitions:\n  - from: \"idea\"\n    to: \"in progress\"\n  - from: \"in progress\"\n    to: \"done\"\n  - from: \"*\"\n    to: \"parked\"\n    fields:\n      - name: \"parked_reason\"\n        prompt: \"Why?\"\n        target: \"section:Parked Record\"\n        type: \"multiline\"\n        required: true\n")
	proj.WorkflowFile = path
	return proj
}

func handoffIssue(t *testing.T, proj *tracker.Project, slug string) *tracker.Issue {
	t.Helper()
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	x := tracker.FindIssue(issues, slug)
	if x == nil {
		t.Fatalf("no issue %s", slug)
	}
	return x
}

func commentsOf(t *testing.T, x *tracker.Issue) string {
	t.Helper()
	cs, err := tracker.LoadComments(x.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(c.Text + " [" + tracker.CommentAuthor(c) + "]\n")
	}
	return b.String()
}

func handoffState(t *testing.T, proj *tracker.Project, slug string) connections.Handoff {
	t.Helper()
	x := handoffIssue(t, proj, slug)
	cs, _ := tracker.LoadComments(x.FilePath)
	h, ok := connections.HandoffOf(proj, x, cs)
	if !ok {
		t.Fatalf("%s is no handoff", slug)
	}
	return h
}

func TestHandoffViaHQWaitsForTheCEOsForward(t *testing.T) {
	proj := routedProject(t)
	asDepartmentLead(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runHandoff(ctx, []string{"product", "--title", "Brand fields", "--why", "brand.md needs a home", "--system", "Factory", "--for", "seo-pass"}); err != nil {
		t.Fatal(err)
	}
	file := createdFile(t, out.String())
	for _, want := range []string{`requested_by: "Marketing"`, `handoff_route: "marketing → hq → product"`, `handoff_for: "seo-pass"`, "## Handoff", "- Route: Marketing → HQ → Product", "- Why: brand.md needs a home", `status: "idea"`} {
		assertContains(t, file, want)
	}
	s := out.String()
	assertContains(t, s, "✓ Handoff Marketing → HQ → Product: factory/brand-fields")
	assertContains(t, s, "Waiting on HQ to forward it (lead-acme runs issue-cli handoff forward factory/brand-fields")
	assertContains(t, commentsOf(t, handoffIssue(t, proj, "seo-pass")), "handoff: asked Product for factory/brand-fields (Marketing → HQ → Product) [lead-acme-marketing]")

	h := handoffState(t, proj, "factory/brand-fields")
	if h.State != connections.StateWaiting || h.Word != "waiting on HQ to forward" {
		t.Errorf("state %+v", h)
	}
	if via := connections.RelayPending(proj, handoffIssue(t, proj, "factory/brand-fields"), nil); via != connections.HQ {
		t.Errorf("RelayPending = %q, want hq", via)
	}

	// Marketing's lead may not forward its own handoff.
	ctx, _ = goalsCtx(t, proj, false)
	err := runHandoff(ctx, []string{"forward", "factory/brand-fields"})
	if err == nil || !strings.Contains(err.Error(), "only lead-acme (HQ's lead) or the owner may forward") {
		t.Errorf("forward by the asker: %v", err)
	}

	// The CEO's waiting list has it; the CEO forwards.
	asLead(t)
	ctx, out = goalsCtx(t, proj, false)
	if err := runHandoffs(ctx, []string{"--waiting"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "factory/brand-fields · Marketing → HQ → Product · waiting on HQ to forward")
	assertContains(t, out.String(), "lead-acme: issue-cli handoff forward factory/brand-fields")
	ctx, out = goalsCtx(t, proj, false)
	if err := runHandoff(ctx, []string{"forward", "factory/brand-fields", "--note", "fits the plan"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ Forwarded factory/brand-fields to Product (it stays at idea; no gate passed)")
	if h := handoffState(t, proj, "factory/brand-fields"); h.State != connections.StateOpen || h.Word != "at idea" {
		t.Errorf("after forward: %+v", h)
	}
	if status := handoffIssue(t, proj, "factory/brand-fields").Status; status != "idea" {
		t.Errorf("forward moved the issue to %s", status)
	}
	ctx, _ = goalsCtx(t, proj, false)
	if err := runHandoff(ctx, []string{"forward", "factory/brand-fields"}); err == nil || !strings.Contains(err.Error(), "changed nothing: it is at idea") {
		t.Errorf("second forward: %v", err)
	}
	ctx, out = goalsCtx(t, proj, false)
	if err := runHandoffs(ctx, []string{"--waiting"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "No handoffs here.")
}

func TestHandoffDeclineParksAndTellsTheAsker(t *testing.T) {
	proj := withParkWorkflow(t, routedProject(t))
	asDepartmentLead(t)
	ctx, _ := goalsCtx(t, proj, false)
	if err := runHandoff(ctx, []string{"product", "--title", "Brand fields", "--why", "w", "--system", "Factory", "--for", "seo-pass"}); err != nil {
		t.Fatal(err)
	}
	asLead(t)
	ctx, _ = goalsCtx(t, proj, false)
	if err := runHandoff(ctx, []string{"decline", "factory/brand-fields"}); err == nil || !strings.Contains(err.Error(), "needs --reason") {
		t.Errorf("decline without a reason: %v", err)
	}
	ctx, out := goalsCtx(t, proj, false)
	if err := runHandoff(ctx, []string{"decline", "factory/brand-fields", "--reason", "covered by the brand foundation"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "✓ Declined factory/brand-fields: idea → parked (covered by the brand foundation)")
	assertContains(t, s, "Tell lead-acme-marketing (Marketing asked for it): SendMessage to lead-acme-marketing")
	assertContains(t, commentsOf(t, handoffIssue(t, proj, "seo-pass")), "handoff: factory/brand-fields (asked of Product) is declined: covered by the brand foundation [lead-acme]")
	h := handoffState(t, proj, "factory/brand-fields")
	if h.State != connections.StateDeclined || h.Reason != "covered by the brand foundation" {
		t.Errorf("after decline: %+v", h)
	}
}

func TestHandoffOffTheRoutesAndAcrossProjectsIsRefused(t *testing.T) {
	proj := routedProject(t)
	asLeadOf(t, "sales")
	ctx, _ := goalsCtx(t, proj, false)
	err := runHandoff(ctx, []string{"product", "--title", "CRM", "--why", "w", "--system", "Factory"})
	if err == nil || !strings.Contains(err.Error(), "Sales → Product is not a declared request route") {
		t.Errorf("off the routes: %v", err)
	}
	ctx, _ = goalsCtx(t, proj, false)
	err = runHandoff(ctx, []string{"other/marketing", "--title", "CRM", "--why", "w"})
	if err == nil || !strings.Contains(err.Error(), "Handoffs stay inside one project.") {
		t.Errorf("across projects: %v", err)
	}
	ctx, _ = goalsCtx(t, proj, false)
	if err = runHandoff(ctx, []string{"sales", "--title", "CRM", "--why", "w"}); err == nil || !strings.Contains(err.Error(), "can't hand off to itself") {
		t.Errorf("to itself: %v", err)
	}
	if m, _ := filepath.Glob(filepath.Join(proj.IssueDir, "*", "crm.md")); len(m) != 0 {
		t.Errorf("a refused handoff writes nothing: %v", m)
	}
}

func TestHandoffWithoutDeclaredRoutes(t *testing.T) {
	proj := goalsProject(t)
	// A department's engine request goes via HQ (D-8) even undeclared.
	asDepartmentLead(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runHandoff(ctx, []string{"product", "--title", "Engine ask", "--why", "w", "--system", "Factory"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, createdFile(t, out.String()), `handoff_route: "marketing → hq → product"`)

	// The CEO hands Marketing work straight, into its inbox.
	asLead(t)
	ctx, out = goalsCtx(t, proj, false)
	if err := runHandoff(ctx, []string{"marketing", "--title", "Launch post", "--why", "release is out"}); err != nil {
		t.Fatal(err)
	}
	file := createdFile(t, out.String())
	assertContains(t, file, `handoff_route: "hq → marketing"`)
	assertContains(t, file, `department: "Marketing"`)
	assertContains(t, out.String(), "In Marketing's inbox. Tell lead-acme-marketing")
	ctx, _ = goalsCtx(t, proj, false)
	if err := runHandoff(ctx, []string{"forward", "launch-post"}); err == nil || !strings.Contains(err.Error(), "goes straight to Marketing") {
		t.Errorf("forward on a straight route: %v", err)
	}

	ctx, out = goalsCtx(t, proj, true)
	if err := runHandoffs(ctx, []string{"--to", "marketing"}); err != nil {
		t.Fatal(err)
	}
	var list []connections.Handoff
	if err := json.Unmarshal(out.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Slug != "launch-post" || list[0].From != "hq" || list[0].State != connections.StateOpen {
		t.Errorf("inbox: %+v", list)
	}
}

func TestHandoffFromAWorkerIsForItsIssue(t *testing.T) {
	proj := goalsProject(t)
	asOwner(t)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "seo-pass")
	ctx, out := goalsCtx(t, proj, false)
	if err := runHandoff(ctx, []string{"product", "--title", "Sitemap hook", "--why", "w", "--system", "Factory"}); err != nil {
		t.Fatal(err)
	}
	file := createdFile(t, out.String())
	assertContains(t, file, `requested_by: "Marketing"`)
	assertContains(t, file, `handoff_for: "seo-pass"`)
	ctx, _ = goalsCtx(t, proj, false)
	if err := runHandoff(ctx, []string{"product", "--title", "X", "--why", "w", "--from", "hq"}); err == nil || !strings.Contains(err.Error(), "hands off from its own box") {
		t.Errorf("--from by a worker: %v", err)
	}
}

func TestHandoffDoneTellsTheAskerOnce(t *testing.T) {
	proj := withParkWorkflow(t, goalsProject(t))
	if err := os.MkdirAll(filepath.Join(proj.IssueDir, "Factory"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(proj.IssueDir, "Factory", "sitemap.md"), "---\ntitle: \"Sitemap\"\nstatus: \"in progress\"\nsystem: \"Factory\"\nrequested_by: \"Marketing\"\nhandoff_route: \"marketing → hq → product\"\nhandoff_for: \"seo-pass\"\n---\nBody.\n")
	asLead(t)
	ctx, out := goalsCtx(t, proj, false)
	wf, err := proj.WorkflowFor("")
	if err != nil {
		t.Fatal(err)
	}
	if err := runTransition(ctx, []string{"factory/sitemap", "--to", "done"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ Told the asker on seo-pass: handoff: factory/sitemap (asked of Product) is done")
	assertContains(t, out.String(), "Tell lead-acme-marketing (Marketing asked for it)")
	out.Reset()
	handoffEnded(ctx, out, wf, "factory/sitemap", lastStatus(wf))
	if out.Len() != 0 {
		t.Errorf("a second end tells nobody: %s", out)
	}
	if n := strings.Count(commentsOf(t, handoffIssue(t, proj, "seo-pass")), "is done"); n != 1 {
		t.Errorf("asker told %d times", n)
	}
	// A transition to a middle status ends nothing.
	out.Reset()
	handoffEnded(ctx, out, wf, "engine", "backlog")
	if out.Len() != 0 {
		t.Errorf("not a handoff: %s", out)
	}
}
