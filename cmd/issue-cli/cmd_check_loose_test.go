package main

import (
	"os"
	"strings"
	"testing"
)

// looseBoxProject is makeSimpleProject with two boxes above the first
// heading, which checklist lists as "1." and "2." under (no section).
func looseBoxProject(t *testing.T) (func() *Context, func() string) {
	t.Helper()
	proj, _, issuePath := makeSimpleProject(t, "in progress")
	data := strings.Replace(readFile(t, issuePath), "## Design", "- [ ] loose first\n- [ ] loose second\n\n## Design", 1)
	if err := os.WriteFile(issuePath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return func() *Context { ctx, _, _ := newTestContext(proj, false); return ctx }, func() string { return readFile(t, issuePath) }
}

func TestCheckTicksLooseBoxByNumber(t *testing.T) {
	for _, ref := range []string{"2", "#2", "2."} {
		newCtx, read := looseBoxProject(t)
		ctx := newCtx()
		if err := runCheck(ctx, []string{"cli/sample", ref}); err != nil {
			t.Fatalf("check %s: %v", ref, err)
		}
		body := read()
		assertContains(t, body, "- [x] loose second\n")
		assertContains(t, body, "- [ ] loose first\n")
		assertContains(t, ctx.Stdout.(interface{ String() string }).String(), "✓ Checked: [#2] loose second")
	}
}

func TestCheckMixesLooseNumbersWithIds(t *testing.T) {
	newCtx, read := looseBoxProject(t)
	ctx := newCtx()
	if err := runCheck(ctx, []string{"cli/sample", "D1", "1", "#2"}); err != nil {
		t.Fatalf("check D1 1 #2: %v", err)
	}
	body := read()
	for _, want := range []string{"- [x] loose first\n", "- [x] loose second\n", "- [x] first task\n", "- [ ] second task\n"} {
		assertContains(t, body, want)
	}

	// A number past the loose boxes is an unknown id: nothing is ticked.
	newCtx, read = looseBoxProject(t)
	if err := runCheck(newCtx(), []string{"cli/sample", "D1", "3"}); err == nil || !strings.Contains(err.Error(), "3") {
		t.Fatalf("check D1 3: want the unknown-id error naming 3, got %v", err)
	}
	assertContains(t, read(), "- [ ] first task\n")
}

func TestUncheckTakesLooseBoxNumber(t *testing.T) {
	newCtx, read := looseBoxProject(t)
	if err := runCheck(newCtx(), []string{"cli/sample", "1"}); err != nil {
		t.Fatal(err)
	}
	if err := runUncheck(newCtx(), []string{"cli/sample", "#1"}); err != nil {
		t.Fatalf("uncheck #1: %v", err)
	}
	assertContains(t, read(), "- [ ] loose first\n")
}
