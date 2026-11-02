package tracker

import (
	"strings"
	"testing"
	"time"
)

func inForceIssue(slug, system, typ, parent, body string) *Issue {
	var fields []ExtraField
	if parent != "" {
		fields = append(fields, ExtraField{Key: "parent", Value: parent})
	}
	return &Issue{Slug: slug, System: system, Type: typ, Status: "in progress", BodyRaw: body, ExtraFields: fields}
}

// TestInForce: what reaches an issue at a status, and what does not.
func TestInForce(t *testing.T) {
	d := func(n int, rest string) string {
		return "- **D-" + string(rune('0'+n)) + "** · 2026-10-0" + string(rune('0'+n)) + " · chat: " + rest
	}
	epic := inForceIssue("ui/epic", "UI", "epic", "", "## Decisions\n\n"+
		d(1, `Epic-wide rule, "yes"`)+"\n")
	other := inForceIssue("ops/other", "Ops", "feature", "", "## Decisions\n\n"+
		d(1, `Project rule, "yes" {scope: project; hard; paths: cmd/**}`)+"\n"+
		d(2, `UI rule, "yes" {scope: system:UI}`)+"\n"+
		d(3, `Ops rule {scope: system:Ops}`)+"\n"+
		d(4, `Cited rule`)+"\n"+
		d(5, `Old project rule {scope: project}`)+"\n"+
		d(6, `Replacer of D-5 (replaces D-5)`)+"\n"+
		"- **D-7** · 2026-10-07 · deferred · chat: Later {scope: project}\n"+
		d(8, `Design-only rule {scope: project; at: in design}`)+"\n"+
		d(9, `Expired rule {scope: project; expires: 2026-10-30}`)+"\n")
	child := inForceIssue("ui/child", "UI", "feature", "ui/epic", "Builds on ops/other#D-4.\n\n## Decisions\n\n"+d(1, "Own rule")+"\n")
	dept := inForceIssue("seo/page", "SEO", "piece", "", "## Decisions\n\n"+d(1, `Marketing rule {scope: department:marketing}`)+"\n"+d(2, `Pieces rule {scope: type:piece}`)+"\n")
	x := BuildDecisionIndex([]*Issue{epic, other, child, dept}, nil)
	o := InForceOptions{Today: time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local), Department: func(i *Issue) string {
		if i.System == "SEO" {
			return "Marketing"
		}
		return ""
	}}
	got := map[string]string{}
	var order []string
	for _, e := range InForce(x, child, "in progress", o) {
		got[e.Entry.Ref] = e.Why
		order = append(order, e.Entry.Ref)
	}
	want := map[string]string{
		"ops/other#D-1": "project", "ops/other#D-2": "system:UI", "ops/other#D-4": "cites",
		"ui/epic#D-1": "parent ui/epic", "ui/child#D-1": "own",
	}
	for ref, why := range want {
		if got[ref] != why {
			t.Errorf("%s: why %q, want %q", ref, got[ref], why)
		}
	}
	for _, ref := range []string{"ops/other#D-3", "ops/other#D-5", "ops/other#D-6", "ops/other#D-7", "ops/other#D-8", "ops/other#D-9", "seo/page#D-1", "seo/page#D-2"} {
		if _, ok := got[ref]; ok {
			t.Errorf("%s should not be in force for ui/child (%q)", ref, got[ref])
		}
	}
	if order[0] != "ops/other#D-1" {
		t.Errorf("hard first: %v", order)
	}
	// D-6 replaced D-5 and is issue-scoped to ops/other: in force there.
	refs := []string{}
	for _, e := range InForce(x, other, "in design", o) {
		refs = append(refs, e.Entry.Ref)
	}
	if s := strings.Join(refs, " "); !strings.Contains(s, "ops/other#D-8") || !strings.Contains(s, "ops/other#D-6") || strings.Contains(s, "ops/other#D-5") {
		t.Errorf("ops/other in design: %v", refs)
	}
	refs = refs[:0]
	for _, e := range InForce(x, dept, "in progress", o) {
		refs = append(refs, e.Entry.Ref+"="+e.Why)
	}
	if s := strings.Join(refs, " "); !strings.Contains(s, "seo/page#D-1=department:marketing") {
		t.Errorf("seo/page: %v", refs)
	}
	// Department and type scopes reach a non-owning issue.
	page2 := inForceIssue("seo/page2", "SEO", "piece", "", "")
	x = BuildDecisionIndex([]*Issue{dept, page2}, nil)
	refs = refs[:0]
	for _, e := range InForce(x, page2, "in progress", o) {
		refs = append(refs, e.Entry.Ref+"="+e.Why)
	}
	if s := strings.Join(refs, " "); s != "seo/page#D-2=type:piece seo/page#D-1=department:marketing" {
		t.Errorf("seo/page2: %v", refs)
	}
}
