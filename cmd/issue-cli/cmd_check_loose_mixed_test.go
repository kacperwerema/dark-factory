package main

import (
	"os"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// looseImplFixture is tickFixture with two boxes above any section (listed
// by checklist as 1 and 2) and a single open Implementation box, I1.
func looseImplFixture(t *testing.T) (*tracker.Project, string) {
	t.Helper()
	proj, issuePath := tickFixture(t)
	data := strings.Replace(readFile(t, issuePath),
		"## Implementation\n- [x] Plan written\n- [ ] Code changes complete\n- [ ] Tests added\n",
		"- [ ] loose first\n- [ ] loose second\n\n## Implementation\n- [ ] Plan written\n", 1)
	if !strings.Contains(data, "loose first") {
		t.Fatal("fixture: the Implementation section was not rewritten")
	}
	if err := os.WriteFile(issuePath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return proj, issuePath
}

func TestCheckLooseNumberBesideSectionId(t *testing.T) {
	proj, issuePath := looseImplFixture(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCheck(ctx, []string{"cli/sample", "1", "I1"}); err != nil {
		t.Fatalf("check 1 I1: %v\n%s", err, stdout)
	}
	body := readFile(t, issuePath)
	for _, want := range []string{"- [x] loose first\n", "- [ ] loose second\n", "- [x] Plan written\n", "- [ ] Owner looked at it\n"} {
		assertContains(t, body, want)
	}

	ctx, stdout, _ = newTestContext(proj, false)
	if err := runCheck(ctx, []string{"cli/sample", "#2"}); err != nil {
		t.Fatalf("check #2: %v\n%s", err, stdout)
	}
	assertContains(t, readFile(t, issuePath), "- [x] loose second\n")
}

func TestTransitionCheckLooseNumberBesideSectionId(t *testing.T) {
	for _, ids := range [][]string{{"1,I1"}, {"1", "I1"}, {"#1", "I1"}} {
		proj, issuePath := looseImplFixture(t)
		asWorkerOn(t, "cli/sample")
		ctx, stdout, _ := newTestContext(proj, false)
		if err := runTransition(ctx, append([]string{"cli/sample", "--to", "testing", "--check"}, ids...)); err != nil {
			t.Fatalf("transition --check %v: %v\n%s", ids, err, stdout)
		}
		body := readFile(t, issuePath)
		for _, want := range []string{"- [x] loose first\n", "- [ ] loose second\n", "- [x] Plan written\n"} {
			assertContains(t, body, want)
		}
		if got := statusOf(t, issuePath); got != "testing" {
			t.Fatalf("--check %v: status = %q, want testing", ids, got)
		}
	}
}

func TestCheckUnknownIdBesideLooseNumberTicksNothing(t *testing.T) {
	proj, issuePath := looseImplFixture(t)
	before := readFile(t, issuePath)
	ctx, stdout, _ := newTestContext(proj, false)
	err := runCheck(ctx, []string{"cli/sample", "1", "I9"})
	if err == nil || !strings.Contains(err.Error(), "I9") {
		t.Fatalf("check 1 I9: want the unknown-id refusal naming I9, got %v\n%s", err, stdout)
	}
	if got := readFile(t, issuePath); got != before {
		t.Fatalf("a refused check changed the issue:\n%s", got)
	}

	asWorkerOn(t, "cli/sample")
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "testing", "--check", "1,I9"}); err == nil {
		t.Fatalf("transition --check 1,I9: want a refusal\n%s", stdout)
	}
	if got := readFile(t, issuePath); got != before {
		t.Fatalf("a refused transition --check changed the issue:\n%s", got)
	}
}
