package main

import (
	"os"
	"strings"
	"testing"
)

// naProject is the simple project at testing, with a Testing section, a
// box marked "(owner)" and a Human Testing box.
func naProject(t *testing.T) (ctx *Context, slug, issuePath string) {
	t.Helper()
	proj, slug, issuePath := makeSimpleProject(t, "testing")
	data := readFile(t, issuePath) + "\n## Testing\n- [ ] Tests passing\n- [ ] `make ux-test REPEAT=2` passed (or N/A: no playtest session, with reason)\n- [ ] Repro re-run and the bug is gone (owner)\n\n## Human Testing\n- [ ] Human verification complete if required\n"
	if err := os.WriteFile(issuePath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, _, _ = newTestContext(proj, false)
	return ctx, slug, issuePath
}

func TestCheckNATicksTheBoxWithItsReason(t *testing.T) {
	asWorker(t)
	ctx, slug, issuePath := naProject(t)
	stdout := ctx.Stdout.(interface{ String() string })
	if err := runCheck(ctx, []string{slug, "T2", "--na", "no page changes"}); err != nil {
		t.Fatalf("check --na: %v", err)
	}
	// The template's own "(or N/A: …)" words are replaced, not stacked.
	want := "- [x] `make ux-test REPEAT=2` passed (N/A: no page changes)\n"
	assertContains(t, readFile(t, issuePath), want)
	assertContains(t, stdout.String(), "✓ Checked: T2 [Testing #2] `make ux-test REPEAT=2` passed (N/A: no page changes)")

	if err := runCheck(ctx, []string{slug, "T1", "--na", "the repro is an automated test (TestFoo)"}); err != nil {
		t.Fatalf("check --na with a test name: %v", err)
	}
	assertContains(t, readFile(t, issuePath), "- [x] Tests passing (N/A: the repro is an automated test (TestFoo))\n")
}

func TestCheckNARefusesAReasonOutOfForm(t *testing.T) {
	asWorker(t)
	ctx, slug, issuePath := naProject(t)
	before := readFile(t, issuePath)
	for reason, want := range map[string]string{
		"the repro is an automated test": "needs the test's name",
		"none":                           "too short",
		"covered by <TestName>":          "placeholder <TestName>",
	} {
		err := runCheck(ctx, []string{slug, "T2", "--na", reason})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("--na %q: want error containing %q, got %v", reason, want, err)
			continue
		}
		// The error lists the forms to write.
		assertContains(t, err.Error(), "N/A: no page changes")
		assertContains(t, err.Error(), "N/A: runner-internal, covered by <TestName>")
	}
	if err := runCheck(ctx, []string{slug, "T1", "T2", "--na", "no page changes"}); err == nil || !strings.Contains(err.Error(), "--na ticks one box by id") {
		t.Errorf("--na with two ids: %v", err)
	}
	if readFile(t, issuePath) != before {
		t.Error("a refused check --na changed the file")
	}
}

// An N/A never takes a box from the owner, whoever runs it.
func TestCheckNARefusesTheOwnersBoxes(t *testing.T) {
	for _, who := range []struct {
		name string
		as   func(*testing.T)
	}{{"worker", asWorker}, {"owner", asOwner}} {
		t.Run(who.name, func(t *testing.T) {
			who.as(t)
			ctx, slug, issuePath := naProject(t)
			before := readFile(t, issuePath)
			for id, why := range map[string]string{"T3": `marked "(owner)"`, "HT1": "a Human Testing box"} {
				err := runCheck(ctx, []string{slug, id, "--na", "no page changes"})
				if err == nil {
					t.Fatalf("check %s --na: want a refusal", id)
				}
				assertContains(t, err.Error(), id+" is the owner's box ("+why+"): an N/A can't skip it")
			}
			if readFile(t, issuePath) != before {
				t.Error("a refused check --na changed the file")
			}
		})
	}
}

func TestCheckEditRefusesAnNAOnTheOwnersBox(t *testing.T) {
	asWorker(t)
	ctx, slug, issuePath := naProject(t)
	before := readFile(t, issuePath)
	for _, c := range []struct{ id, text string }{
		{"T3", "Repro re-run and the bug is gone (owner) (N/A: no page changes)"},
		{"T3", "Repro re-run and the bug is gone"},
		{"HT1", "Human verification complete if required (n/a: CLI only change here)"},
	} {
		err := runCheckEdit(ctx, []string{slug, c.id, "--text", c.text})
		if err == nil || !strings.Contains(err.Error(), "is the owner's box") {
			t.Errorf("check-edit %s %q: want a refusal, got %v", c.id, c.text, err)
		}
	}
	if readFile(t, issuePath) != before {
		t.Error("a refused check-edit changed the file")
	}
	// Rewording an owner box, keeping its marker, still works.
	if err := runCheckEdit(ctx, []string{slug, "T3", "--text", "Repro steps re-run, bug gone (owner)"}); err != nil {
		t.Errorf("rewording an owner box: %v", err)
	}
	// The owner may edit their own boxes as they like.
	asOwner(t)
	ctx, _, _ = newTestContext(ctx.Project, false)
	if err := runCheckEdit(ctx, []string{slug, "HT1", "--text", "Human verification (n/a: nothing to see on a page)"}); err != nil {
		t.Errorf("the owner editing HT1: %v", err)
	}
}
