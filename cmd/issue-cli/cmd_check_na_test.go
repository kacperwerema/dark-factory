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

// naRefused asserts a check --na refusal: a non-zero exit, output that
// starts with "✗ not ticked:" and says why, and an unchanged file.
func naRefused(t *testing.T, err error, issuePath, before string, why ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("want a refusal, got a tick")
	}
	if code := processExitCode(err); code == 0 {
		t.Errorf("exit code %d, want non-zero", code)
	}
	if !strings.HasPrefix(err.Error(), "✗ not ticked: ") {
		t.Errorf("refusal doesn't start with %q:\n%s", "✗ not ticked: ", err)
	}
	for _, w := range why {
		assertContains(t, err.Error(), w)
	}
	if readFile(t, issuePath) != before {
		t.Error("a refused check --na changed the file")
	}
}

// AC1: a short reason says the minimum.
func TestCheckNAShortReasonNamesTheMinimum(t *testing.T) {
	asWorker(t)
	ctx, slug, issuePath := naProject(t)
	before := readFile(t, issuePath)
	err := runCheck(ctx, []string{slug, "T2", "--na", "none"})
	naRefused(t, err, issuePath, before, `"none" is too short: 1 word`, "at least 3 words")
}

// AC2: a placeholder left in the reason, such as <project>, is refused and
// named; the box stays unticked.
func TestCheckNAPlaceholderSaysWhich(t *testing.T) {
	asWorker(t)
	ctx, slug, issuePath := naProject(t)
	before := readFile(t, issuePath)
	err := runCheck(ctx, []string{slug, "T2", "--na", "docs reachable via issue-cli --project <project> help"})
	naRefused(t, err, issuePath, before, "placeholder <project>", "without the <>")
	assertContains(t, readFile(t, issuePath), "- [ ] `make ux-test REPEAT=2` passed")
}

// Every refusal, not only the reason's, starts with "✗ not ticked:".
func TestCheckNAEveryRefusalSaysNotTicked(t *testing.T) {
	asWorker(t)
	ctx, slug, issuePath := naProject(t)
	before := readFile(t, issuePath)
	naRefused(t, runCheck(ctx, []string{slug, "ZZ9", "--na", "no page changes"}), issuePath, before, "unknown checkbox id: ZZ9")
	naRefused(t, runCheck(ctx, []string{slug, "T3", "--na", "no page changes"}), issuePath, before, "the owner's box")
	naRefused(t, runCheck(ctx, []string{slug, "T1", "T2", "--na", "no page changes"}), issuePath, before, "--na ticks one box by id")
}

// Shipping boxes have no N/A form: the refusal says to tick them plainly.
func TestCheckNAShippingBoxSaysTickPlainly(t *testing.T) {
	asWorker(t)
	ctx, slug, issuePath := naProject(t)
	data := readFile(t, issuePath) + "\n## Shipping\n- [ ] Check scripts removed in a last commit before the merge, or none\n"
	if err := os.WriteFile(issuePath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runCheck(ctx, []string{slug, "S1", "--na", "no check scripts were written"})
	naRefused(t, err, issuePath, data, "shipping steps have no N/A form", "check "+slug+" S1")
	if strings.Contains(err.Error(), "N/A: no page changes") {
		t.Errorf("a Shipping refusal lists the N/A forms:\n%s", err)
	}
	if err := runCheck(ctx, []string{slug, "S1"}); err != nil {
		t.Fatalf("plain check of S1: %v", err)
	}
}

// AC3: the CLI-only form ticks a Test Plan box, and the page-state form
// ticks one too.
func TestCheckNANewForms(t *testing.T) {
	asWorker(t)
	ctx, slug, issuePath := naProject(t)
	data := strings.Replace(readFile(t, issuePath), "- thing two\n", "- thing two\n- [ ] Manual check written as a playtest session (or N/A: why)\n", 1)
	if err := os.WriteFile(issuePath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runCheck(ctx, []string{slug, "TP1", "--na", "cli flow covered by TestX"}); err != nil {
		t.Fatalf("cli flow form: %v", err)
	}
	assertContains(t, readFile(t, issuePath), "- [x] Manual check written as a playtest session (N/A: cli flow covered by TestX)\n")
	if err := runCheck(ctx, []string{slug, "T1", "--na", "page state a scenario can't build (a live worker pane), shown by TestCrewPane"}); err != nil {
		t.Fatalf("page state form: %v", err)
	}
	before := readFile(t, issuePath)
	for reason, want := range map[string]string{
		"cli flow covered by":                                       "needs the test that walks the flow",
		"page state a scenario can't build, shown by X":             "needs what state, in parens",
		"page state a scenario can't build (git history) from logs": "needs what shows the change instead",
	} {
		if err := runCheck(ctx, []string{slug, "T2", "--na", reason}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("--na %q: want %q, got %v", reason, want, err)
		}
	}
	if readFile(t, issuePath) != before {
		t.Error("a refused check --na changed the file")
	}
}
