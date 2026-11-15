package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func plainComments(t *testing.T, proj *tracker.Project) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(proj.IssueDir, "plain.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func retroCount(t *testing.T, proj *tracker.Project) int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(proj.WorkDir, "retros", "*"))
	if err != nil {
		t.Fatal(err)
	}
	return len(files)
}

// A flag and its value quoted as one word used to exit 0 with the flag in the
// stored text and the flag's work not done (post-mortem 115748).
func TestFlagWordWithASpaceIsRefused(t *testing.T) {
	memoryAs(t, "")
	proj := newTestProject(t)
	ctx, _, _ := testContext(proj)
	old := memAt(t, proj, "the old fact about releases", "")

	cases := []struct {
		name, flag string
		run        func() error
	}{
		{"memory add", "--supersedes", func() error {
			return runMemory(ctx, []string{"add", "the new fact", "--supersedes " + old.ID})
		}},
		{"comment", "--body", func() error { return runComment(ctx, []string{"plain", "--body hello"}) }},
		{"retrospective", "--text", func() error { return runRetrospective(ctx, []string{"plain", "--text hello"}) }},
		{"single dash", "-text", func() error { return runComment(ctx, []string{"plain", "-text hello"}) }},
		{"a newline after the flag", "--text", func() error { return runComment(ctx, []string{"plain", "--text\nhello"}) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.run()
			if err == nil || processExitCode(err) != 2 {
				t.Fatalf("want a usage error, got %v", err)
			}
			if !strings.Contains(err.Error(), c.flag) || !strings.Contains(err.Error(), "one word") {
				t.Errorf("the refusal should name %s and say what is wrong: %v", c.flag, err)
			}
		})
	}
	t.Run("ask", func(t *testing.T) {
		_, _, _, err := siblingArgs(ctx, "ask", []string{"plain", "--from", "cli/mine", "--text hello"})
		if err == nil || processExitCode(err) != 2 {
			t.Fatalf("want a usage error, got %v", err)
		}
	})

	if all := memSet(t, proj).All(); len(all) != 1 || all[0].ID != old.ID {
		t.Errorf("nothing should be stored, memories: %+v", all)
	}
	if body := plainComments(t, proj); strings.Contains(body, "hello") {
		t.Errorf("no comment should be stored:\n%s", body)
	}
	if n := retroCount(t, proj); n != 0 {
		t.Errorf("no retrospective should be written, found %d", n)
	}
}

func TestCommentMayStartWithADashedWord(t *testing.T) {
	memoryAs(t, "")
	proj := newTestProject(t)
	ctx, _, _ := testContext(proj)

	for _, text := range []string{
		"--- note",
		"- [ ] a list item",
		"--force is not a flag of comment",
		"--json output changed", // every command has it, and nobody means it here
		"--bodyish word",
	} {
		if err := runComment(ctx, []string{"plain", text}); err != nil {
			t.Errorf("comment %q: %v", text, err)
			continue
		}
		if body := plainComments(t, proj); !strings.Contains(body, text) {
			t.Errorf("comment %q not stored:\n%s", text, body)
		}
	}

	// After "--" the words are text, even one that names a flag.
	if err := runComment(ctx, []string{"plain", "--", "--body is what I meant"}); err != nil {
		t.Fatalf("after --: %v", err)
	}
	if body := plainComments(t, proj); !strings.Contains(body, "--body is what I meant") {
		t.Errorf("text after -- not stored:\n%s", body)
	}
	// A flag's value is its own: only a positional is read as a flag word.
	if err := runComment(ctx, []string{"plain", "--text", "--body as a value"}); err != nil {
		t.Fatalf("flag value: %v", err)
	}

	if err := runMemory(ctx, []string{"add", "--- a fact with a dashed start"}); err != nil {
		t.Fatalf("memory add: %v", err)
	}
	if err := runMemory(ctx, []string{"add", "--", "--kind of a fact"}); err != nil {
		t.Fatalf("memory add after --: %v", err)
	}
	if n := len(memSet(t, proj).All()); n != 2 {
		t.Errorf("want 2 memories, got %d", n)
	}
}
