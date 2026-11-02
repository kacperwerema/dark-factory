package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// sampleComments writes four comments on cli/sample with fixed dates and
// authors; #2 is done.
func sampleComments(t *testing.T, issuePath string) {
	t.Helper()
	comments := []tracker.Comment{
		{ID: 1, Date: "2026-10-29T08:00:00Z", Text: "progress: started", Source: "cli", Author: "agent-cli-sample"},
		{ID: 2, Date: "2026-10-30T09:00:00Z", Text: "question: which flag?", Source: "cli", Author: "agent-cli-sample", Status: "done"},
		{ID: 3, Date: "2026-10-30T10:00:00Z", Text: "Use --ref.\nSecond line.", Source: "web", Author: tracker.OwnerAuthor},
		{ID: 4, Date: "2026-10-31", Text: "question: and the docs?", Source: "cli", Author: "lead-test"},
	}
	if err := tracker.SaveComments(issuePath, comments); err != nil {
		t.Fatal(err)
	}
}

func TestCommentsListsWithPageNumbers(t *testing.T) {
	proj, _, issuePath := makeSimpleProject(t, "in progress")
	sampleComments(t, issuePath)

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runComments(ctx, []string{"cli/sample"}); err != nil {
		t.Fatalf("comments: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "== Comments on cli/sample (4 of 4) ==")
	assertContains(t, out, "#1 · 2026-10-29 · agent-cli-sample · progress\n    progress: started")
	assertContains(t, out, "#2 · 2026-10-30 · agent-cli-sample · question · done")
	assertContains(t, out, "#3 · 2026-10-30 · owner\n    Use --ref.\n    Second line.")
	assertContains(t, out, "#4 · 2026-10-31 · lead-test · question")
	if strings.Contains(out, "== Body ==") || strings.Contains(out, "first task") {
		t.Errorf("comments printed the body:\n%s", out)
	}
}

func TestCommentsFilters(t *testing.T) {
	proj, _, issuePath := makeSimpleProject(t, "in progress")
	sampleComments(t, issuePath)

	ids := func(args ...string) []int {
		t.Helper()
		ctx, stdout, _ := newTestContext(proj, true)
		if err := runComments(ctx, append([]string{"cli/sample"}, args...)); err != nil {
			t.Fatalf("comments %v: %v", args, err)
		}
		var rows []struct {
			ID     int    `json:"id"`
			Author string `json:"author"`
			Kind   string `json:"kind"`
			Open   bool   `json:"open"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
			t.Fatalf("json: %v\n%s", err, stdout.String())
		}
		var out []int
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out
	}
	cases := []struct {
		args []string
		want []int
	}{
		{nil, []int{1, 2, 3, 4}},
		{[]string{"--kind", "question"}, []int{2, 4}},
		{[]string{"--kind", "question", "--open"}, []int{4}},
		{[]string{"--kind", "progress,question:"}, []int{1, 2, 4}},
		{[]string{"--author", "agent-cli-sample"}, []int{1, 2}},
		{[]string{"--author", "OWNER"}, []int{3}},
		{[]string{"--since", "2026-10-30"}, []int{2, 3, 4}},
		{[]string{"--since", "2026-10-30T09:30:00Z"}, []int{3, 4}},
		{[]string{"--open"}, []int{1, 3, 4}},
		{[]string{"--author", "nobody"}, nil},
	}
	for _, c := range cases {
		got := ids(c.args...)
		if len(got) != len(c.want) {
			t.Errorf("%v: got %v, want %v", c.args, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%v: got %v, want %v", c.args, got, c.want)
				break
			}
		}
	}

	// JSON carries the fields a parser needs.
	ctx, stdout, _ := newTestContext(proj, true)
	if err := runComments(ctx, []string{"cli/sample", "--kind", "question"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), `"kind": "question"`)
	assertContains(t, stdout.String(), `"author": "lead-test"`)
	assertContains(t, stdout.String(), `"open": false`)

	// No match says so; a bad --since is a usage error.
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runComments(ctx, []string{"cli/sample", "--author", "nobody"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "No comments on cli/sample match these filters (4 in all).")
	ctx, _, _ = newTestContext(proj, false)
	if err := runComments(ctx, []string{"cli/sample", "--since", "yesterday"}); err == nil {
		t.Error("--since yesterday: want an error")
	}
}

func TestCommentsNoneAndJSONEmptyArray(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runComments(ctx, []string{"cli/sample"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "No comments on cli/sample.")
	ctx, stdout, _ = newTestContext(proj, true)
	if err := runComments(ctx, []string{"cli/sample"}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != "[]" {
		t.Errorf("want [], got %q", stdout.String())
	}
}

func TestCommentKind(t *testing.T) {
	for text, want := range map[string]string{
		"question: x":    "question",
		"  Progress: y":  "progress",
		"red-rounds: z":  "red-rounds",
		"no prefix here": "",
		"see http://x":   "",
		"a very long prefix that is not a kind: x": "",
	} {
		if got := commentKind(text); got != want {
			t.Errorf("commentKind(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestDecideRefRoundTripsThroughShow(t *testing.T) {
	proj, _, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local) }
	if err := runDecide(ctx, []string{"cli/sample", "--text", "Follow the lead's call.", "--how", "chat",
		"--ref", "N-26", "--ref", "N-27", "--replaces", "D-9"}); err == nil {
		t.Fatal("--replaces D-9 (none): want an error")
	}
	ctx, stdout, _ = newTestContext(proj, false)
	ctx.Now = func() time.Time { return time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local) }
	if err := runDecide(ctx, []string{"cli/sample", "--text", "Follow the lead's call.", "--how", "chat",
		"--ref", "N-26", "--ref", "N-27", "--provisional"}); err != nil {
		t.Fatalf("decide: %v", err)
	}
	line := "- **D-1** · 2026-10-31 · chat, provisional: Follow the lead's call. (ref N-26, N-27)"
	assertContains(t, stdout.String(), line)
	assertContains(t, readFile(t, issuePath), line)

	// show prints the line, and its JSON carries the refs.
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runShow(ctx, []string{"cli/sample"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "(ref N-26, N-27)")
	ctx, stdout, _ = newTestContext(proj, true)
	if err := runShow(ctx, []string{"cli/sample"}); err != nil {
		t.Fatal(err)
	}
	var shown struct {
		Decisions []tracker.Decision `json:"decisions"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &shown); err != nil {
		t.Fatal(err)
	}
	if len(shown.Decisions) != 1 || strings.Join(shown.Decisions[0].Refs, ",") != "N-26,N-27" || shown.Decisions[0].Text != "Follow the lead's call." {
		t.Fatalf("show --json decisions = %+v", shown.Decisions)
	}

	// decisions lists the ref; --confirm keeps it.
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runDecisions(ctx, []string{"--issue", "cli/sample"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "    ref N-26, N-27")
	ctx, _, _ = newTestContext(proj, false)
	if err := runDecide(ctx, []string{"cli/sample", "--confirm", "D-1", "--quote", "Yes."}); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	assertContains(t, readFile(t, issuePath), `"Yes." (ref N-26, N-27)`)

	// A ref sits before the attribute group and both parse back.
	ctx, stdout, _ = newTestContext(proj, true)
	if err := runDecide(ctx, []string{"cli/sample", "--text", "Scoped call.", "--ref", "N-30", "--scope", "system:CLI"}); err != nil {
		t.Fatalf("decide --ref --scope: %v", err)
	}
	var withAttrs decideOutput
	if err := json.Unmarshal(stdout.Bytes(), &withAttrs); err != nil {
		t.Fatal(err)
	}
	if strings.Join(withAttrs.Decision.Refs, ",") != "N-30" || withAttrs.Decision.Text != "Scoped call." || !strings.Contains(withAttrs.Decision.Raw, "(ref N-30) {") {
		t.Fatalf("ref with attrs = %+v", withAttrs.Decision)
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runDecide(ctx, []string{"cli/sample", "--amend", "D-2", "--ref", "N-31", "--scope", "issue"}); err == nil {
		t.Error("--amend with --ref: want an error")
	}

	// A ref with parentheses or a comma is refused.
	for _, bad := range []string{"N-1, N-2", "(N-1)", " "} {
		ctx, _, _ = newTestContext(proj, false)
		if err := runDecide(ctx, []string{"cli/sample", "--text", "x", "--ref", bad}); err == nil {
			t.Errorf("--ref %q: want an error", bad)
		}
	}
}

func TestReplaceBodyFile(t *testing.T) {
	proj, _, issuePath := makeSimpleProject(t, "in progress")
	file := filepath.Join(t.TempDir(), "design.md")
	content := "- uses `Asteroid.NetRate` (workers × yield)\n- second $(line)\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runReplace(ctx, []string{"cli/sample", "--section", "Design", "--body-file", file}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	assertContains(t, stdout.String(), `✓ Replaced section "Design"`)
	got := readFile(t, issuePath)
	assertContains(t, got, "## Design\n- uses `Asteroid.NetRate` (workers × yield)\n- second $(line)")
	if strings.Contains(got, "first task") {
		t.Errorf("old section content kept:\n%s", got)
	}

	// From stdin.
	ctx, _, _ = newTestContext(proj, false)
	ctx.Stdin = strings.NewReader("- from stdin\n")
	if err := runReplace(ctx, []string{"cli/sample", "--section", "Design", "--body-file", "-"}); err != nil {
		t.Fatalf("replace stdin: %v", err)
	}
	assertContains(t, readFile(t, issuePath), "## Design\n- from stdin")

	// Both inline and a file, or a missing file, are refused.
	ctx, _, _ = newTestContext(proj, false)
	if err := runReplace(ctx, []string{"cli/sample", "--section", "Design", "--body", "x", "--body-file", file}); err == nil {
		t.Error("--body with --body-file: want an error")
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runReplace(ctx, []string{"cli/sample", "--section", "Design", "--body-file", filepath.Join(t.TempDir(), "none.md")}); err == nil {
		t.Error("missing --body-file: want an error")
	}
}
