package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestUpdateIssueFrontmatter(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")

	original := `---
title: "Test Issue"
status: "idea"
priority: "low"
---

Body text.
`
	os.WriteFile(fp, []byte(original), 0644)

	newStatus := "in progress"
	newPriority := "high"
	newAssignee := "bob"
	newVersion := "1.0"
	err := UpdateIssueFrontmatter(fp, IssueUpdate{
		Status:   &newStatus,
		Priority: &newPriority,
		Assignee: &newAssignee,
		Version:  &newVersion,
		Labels:   []string{"bug", "ui"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(fp)
	content := string(data)

	if !strings.Contains(content, "in progress") {
		t.Error("status not updated")
	}
	if !strings.Contains(content, "high") {
		t.Error("priority not updated")
	}
	if !strings.Contains(content, "bob") {
		t.Error("assignee not updated")
	}
	if !strings.Contains(content, "1.0") {
		t.Error("version not updated")
	}
	if !strings.Contains(content, "bug") || !strings.Contains(content, "ui") {
		t.Error("labels not updated")
	}
}

func TestUpdateIssueFrontmatter_ClearFields(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")

	original := `---
title: "Test"
priority: "high"
version: "1.0"
assignee: "alice"
labels:
  - bug
---

Body.
`
	os.WriteFile(fp, []byte(original), 0644)

	empty := ""
	err := UpdateIssueFrontmatter(fp, IssueUpdate{
		Priority: &empty,
		Version:  &empty,
		Assignee: &empty,
		Labels:   []string{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(fp)
	content := string(data)

	// The cleared fields should be removed from frontmatter
	if strings.Contains(content, "priority") {
		t.Error("priority should have been removed")
	}
	if strings.Contains(content, "version") {
		t.Error("version should have been removed")
	}
	if strings.Contains(content, "assignee") {
		t.Error("assignee should have been removed")
	}
	if strings.Contains(content, "labels") {
		t.Error("labels should have been removed")
	}
}

func TestUpdateIssueFrontmatter_UpdateBody(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")

	original := `---
title: "Test"
---

Old body.
`
	os.WriteFile(fp, []byte(original), 0644)

	newBody := "New body content."
	err := UpdateIssueFrontmatter(fp, IssueUpdate{
		Body: &newBody,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(fp)
	content := string(data)
	if !strings.Contains(content, "New body content.") {
		t.Error("body not updated")
	}
}

func TestUpdateIssueFrontmatter_HumanApprovalReplacesLegacyApproval(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")

	original := `---
title: "Test"
approved_for: "backlog"
---

Body.
`
	os.WriteFile(fp, []byte(original), 0644)

	approval := "documentation"
	err := UpdateIssueFrontmatter(fp, IssueUpdate{
		HumanApproval: &approval,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(fp)
	content := string(data)
	if strings.Contains(content, "approved_for") {
		t.Error("legacy approved_for should have been removed")
	}
	if !strings.Contains(content, `human_approval: "documentation"`) {
		t.Error("human_approval should have been written")
	}
}

func TestUpdateIssueFrontmatter_ConcurrentWritesRemainParseable(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")

	original := `---
title: "Test"
status: "idea"
---

Body.
`
	os.WriteFile(fp, []byte(original), 0644)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := "Body update " + string(rune('A'+i))
			status := "in progress"
			if i%2 == 0 {
				status = "testing"
			}
			if err := UpdateIssueFrontmatter(fp, IssueUpdate{Status: &status, Body: &body}); err != nil {
				t.Errorf("update %d failed: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("read final file: %v", err)
	}
	if _, err := ParseIssue("test.md", data); err != nil {
		t.Fatalf("final file should still parse after concurrent writes: %v\n%s", err, string(data))
	}
}

func TestSetFrontmatterField_AddsNewKey(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")
	os.WriteFile(fp, []byte(`---
title: "Test"
status: "in progress"
---

Body.
`), 0644)

	if err := SetFrontmatterField(fp, "waiting", "design review", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(fp)
	content := string(data)
	if !strings.Contains(content, `waiting: "design review"`) {
		t.Errorf("waiting field not written:\n%s", content)
	}
	issue, err := ParseIssue("test.md", data)
	if err != nil {
		t.Fatalf("file no longer parses: %v", err)
	}
	if issue.Title != "Test" || issue.Status != "in progress" {
		t.Errorf("existing fields corrupted: title=%q status=%q", issue.Title, issue.Status)
	}
}

func TestSetFrontmatterField_UpdatesExistingKey(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")
	os.WriteFile(fp, []byte(`---
title: "Test"
waiting: "old blocker"
---

Body.
`), 0644)

	if err := SetFrontmatterField(fp, "waiting", "new blocker", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(fp)
	content := string(data)
	if strings.Contains(content, "old blocker") {
		t.Errorf("old value not replaced:\n%s", content)
	}
	if !strings.Contains(content, `waiting: "new blocker"`) {
		t.Errorf("new value missing:\n%s", content)
	}
}

func TestSetFrontmatterField_ClearRemovesKey(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")
	os.WriteFile(fp, []byte(`---
title: "Test"
waiting: "blocker"
priority: "high"
---

Body.
`), 0644)

	if err := SetFrontmatterField(fp, "waiting", "", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(fp)
	content := string(data)
	if strings.Contains(content, "waiting") {
		t.Errorf("waiting key should have been removed:\n%s", content)
	}
	if !strings.Contains(content, "high") {
		t.Errorf("unrelated fields should survive:\n%s", content)
	}
}

func TestSetFrontmatterField_ClearMissingKeyIsNoop(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")
	original := `---
title: "Test"
---

Body.
`
	os.WriteFile(fp, []byte(original), 0644)

	if err := SetFrontmatterField(fp, "waiting", "", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(fp)
	if _, err := ParseIssue("test.md", data); err != nil {
		t.Fatalf("file should still parse: %v", err)
	}
}

func TestSetFrontmatterField_RejectsProtectedKeys(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")
	os.WriteFile(fp, []byte(`---
title: "Test"
status: "backlog"
---

Body.
`), 0644)

	for key := range ProtectedFrontmatterFields {
		if err := SetFrontmatterField(fp, key, "hacked", false); err == nil {
			t.Errorf("expected protected key %q to be refused", key)
		}
	}

	data, _ := os.ReadFile(fp)
	if strings.Contains(string(data), "hacked") {
		t.Errorf("protected field was mutated:\n%s", string(data))
	}
}

func TestSetFrontmatterField_RejectsInvalidKey(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")
	os.WriteFile(fp, []byte("---\ntitle: \"T\"\n---\n\nbody\n"), 0644)

	bad := []string{"", "Waiting", "wait ing", "9nine", "with-dash", "with.dot"}
	for _, key := range bad {
		if err := SetFrontmatterField(fp, key, "x", false); err == nil {
			t.Errorf("expected invalid key %q to be refused", key)
		}
	}
}

func TestSetFrontmatterField_EscapesSpecialChars(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")
	os.WriteFile(fp, []byte("---\ntitle: \"T\"\n---\n\nbody\n"), 0644)

	value := `line "with quotes" and \backslash`
	if err := SetFrontmatterField(fp, "note", value, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(fp)
	issue, err := ParseIssue("test.md", data)
	if err != nil {
		t.Fatalf("file should parse after escaping: %v\n%s", err, string(data))
	}
	var got string
	for _, ef := range issue.ExtraFields {
		if ef.Key == "note" {
			got = ef.Value
		}
	}
	if got != value {
		t.Errorf("round-trip value = %q, want %q", got, value)
	}
}

// TestFrontmatterScalarWrites_RoundTrip writes awkward values through both
// scalar writers — UpdateIssueFrontmatter (update --title, the UI edit) and
// SetFrontmatterField (set-meta) — and reads them back. A second write after
// each one catches a value that spilled onto more than one line.
func TestFrontmatterScalarWrites_RoundTrip(t *testing.T) {
	values := []struct{ name, value string }{
		{"dollar group", `cost $1`},
		{"dollar named", `cost ${x}`},
		{"quotes", `say "hi"`},
		{"backslash", `back\slash`},
		{"newline", "line1\nline2"},
		{"carriage return", "line1\r\nline2"},
		{"tab", "a\tb"},
		{"colon hash", `a: #b`},
		{"leading dash", `- lead`},
	}
	noteOf := func(issue *Issue) string {
		for _, ef := range issue.ExtraFields {
			if ef.Key == "note" {
				return ef.Value
			}
		}
		return ""
	}

	for _, tc := range values {
		t.Run(tc.name, func(t *testing.T) {
			fp := filepath.Join(t.TempDir(), "test.md")
			os.WriteFile(fp, []byte("---\ntitle: \"old\"\nstatus: \"backlog\"\nnote: \"old\"\n---\n\nbody\n"), 0644)

			title := tc.value
			if err := UpdateIssueFrontmatter(fp, IssueUpdate{Title: &title}); err != nil {
				t.Fatalf("UpdateIssueFrontmatter: %v", err)
			}
			if err := SetFrontmatterField(fp, "note", tc.value, false); err != nil {
				t.Fatalf("SetFrontmatterField: %v", err)
			}
			data, _ := os.ReadFile(fp)
			issue, err := ParseIssue("test.md", data)
			if err != nil {
				t.Fatalf("file no longer parses: %v\n%s", err, data)
			}
			if issue.Title != tc.value {
				t.Errorf("title = %q, want %q", issue.Title, tc.value)
			}
			if got := noteOf(issue); got != tc.value {
				t.Errorf("note = %q, want %q", got, tc.value)
			}

			later, again := "later", "again"
			if err := UpdateIssueFrontmatter(fp, IssueUpdate{Title: &later}); err != nil {
				t.Fatalf("second UpdateIssueFrontmatter: %v", err)
			}
			if err := SetFrontmatterField(fp, "note", again, false); err != nil {
				t.Fatalf("second SetFrontmatterField: %v", err)
			}
			data, _ = os.ReadFile(fp)
			issue, err = ParseIssue("test.md", data)
			if err != nil {
				t.Fatalf("file no longer parses after a second write: %v\n%s", err, data)
			}
			if issue.Title != later || noteOf(issue) != again || issue.Status != "backlog" {
				t.Errorf("second write: title=%q note=%q status=%q\n%s", issue.Title, noteOf(issue), issue.Status, data)
			}
		})
	}
}

// TestFrontmatterSplitHandlesEmbeddedDashes guards against the bare-"---" split
// bug: if the frontmatter contains a "---" substring without a leading newline
// (typical case: a YAML value mentioning a dash separator), the bare split
// breaks at the wrong position and the file gets corrupted on the next write.
// Both SetFrontmatterField and UpdateIssueFrontmatter must split on "\n---" so
// only the closing fence — always preceded by a newline — terminates the
// frontmatter.
func TestFrontmatterSplitHandlesEmbeddedDashes(t *testing.T) {
	// `note` carries a literal `---` substring with no leading newline. With the
	// bare-"---" split this is misidentified as the closing fence and the value
	// is partially relocated into the body on write.
	original := "---\n" +
		"title: \"Embedded\"\n" +
		"status: \"in progress\"\n" +
		"note: \"see ---x for context\"\n" +
		"---\n\n" +
		"body line\n"

	t.Run("SetFrontmatterField", func(t *testing.T) {
		dir := t.TempDir()
		fp := filepath.Join(dir, "test.md")
		if err := os.WriteFile(fp, []byte(original), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}

		if err := SetFrontmatterField(fp, "due", "2026-05-01", false); err != nil {
			t.Fatalf("SetFrontmatterField: %v", err)
		}

		data, err := os.ReadFile(fp)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		issue, err := ParseIssue("test.md", data)
		if err != nil {
			t.Fatalf("ParseIssue after update: %v\n%s", err, string(data))
		}
		var note, due string
		for _, ef := range issue.ExtraFields {
			switch ef.Key {
			case "note":
				note = ef.Value
			case "due":
				due = ef.Value
			}
		}
		if note != "see ---x for context" {
			t.Errorf("note round-trip = %q, want %q\nfile:\n%s", note, "see ---x for context", string(data))
		}
		if due != "2026-05-01" {
			t.Errorf("due = %q, want 2026-05-01", due)
		}
		if strings.TrimSpace(issue.BodyRaw) != "body line" {
			t.Errorf("body corrupted = %q, want %q\nfile:\n%s", issue.BodyRaw, "body line", string(data))
		}
	})

	t.Run("UpdateIssueFrontmatter", func(t *testing.T) {
		dir := t.TempDir()
		fp := filepath.Join(dir, "test.md")
		if err := os.WriteFile(fp, []byte(original), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}

		newPriority := "critical"
		if err := UpdateIssueFrontmatter(fp, IssueUpdate{Priority: &newPriority}); err != nil {
			t.Fatalf("UpdateIssueFrontmatter: %v", err)
		}

		data, err := os.ReadFile(fp)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		issue, err := ParseIssue("test.md", data)
		if err != nil {
			t.Fatalf("ParseIssue after update: %v\n%s", err, string(data))
		}
		if issue.Priority != "critical" {
			t.Errorf("priority = %q, want critical", issue.Priority)
		}
		var note string
		for _, ef := range issue.ExtraFields {
			if ef.Key == "note" {
				note = ef.Value
			}
		}
		if note != "see ---x for context" {
			t.Errorf("note round-trip = %q, want %q\nfile:\n%s", note, "see ---x for context", string(data))
		}
		if strings.TrimSpace(issue.BodyRaw) != "body line" {
			t.Errorf("body corrupted = %q, want %q\nfile:\n%s", issue.BodyRaw, "body line", string(data))
		}
	})
}

func TestRewriteIssueFile_PreservesBytesExactly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "issue.md")
	original := []byte(`---
title: "Original"
status: "idea"
# a comment that yaml round-trip would drop
priority: "high"
---

Original body.
`)
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}

	rewritten := []byte(`---
title: "Edited"
status: "idea"
# a comment that yaml round-trip would drop
priority: "low"
labels:
  - bug
custom_field: "added"
---

New body content.
`)
	if err := RewriteIssueFile(path, rewritten); err != nil {
		t.Fatalf("RewriteIssueFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(rewritten) {
		t.Errorf("file bytes differ from input\nwant:\n%s\ngot:\n%s", rewritten, got)
	}
}

func TestRewriteIssueFile_RejectsInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "issue.md")
	original := []byte(`---
title: "Original"
status: "idea"
---

Body.
`)
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}

	bad := []byte(`---
title: "Broken
status: [unterminated
---

Body.
`)
	if err := RewriteIssueFile(path, bad); err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Errorf("file changed despite validation error\nwant:\n%s\ngot:\n%s", original, got)
	}
}

func TestRewriteIssueFile_RejectsMissingFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "issue.md")
	original := []byte("---\ntitle: \"Original\"\n---\n\nBody.\n")
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}

	if err := RewriteIssueFile(path, []byte("just a body, no frontmatter\n")); err == nil {
		t.Fatal("expected error for content without frontmatter, got nil")
	}

	got, _ := os.ReadFile(path)
	if string(got) != string(original) {
		t.Error("file should be unchanged when content has no frontmatter")
	}
}

func TestRewriteIssueFile_HoldsLockSerially(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "issue.md")
	original := []byte("---\ntitle: \"Original\"\n---\n\nBody.\n")
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}

	const writers = 8
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		i := i
		go func() {
			defer wg.Done()
			content := []byte("---\ntitle: \"Writer " + string(rune('A'+i)) + "\"\n---\n\nBody.\n")
			if err := RewriteIssueFile(path, content); err != nil {
				t.Errorf("writer %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	issue, err := ParseIssue("issue.md", got)
	if err != nil {
		t.Fatalf("final file does not parse: %v\ncontent:\n%s", err, got)
	}
	if !strings.HasPrefix(issue.Title, "Writer ") {
		t.Errorf("final title %q is not from any writer — possible torn write", issue.Title)
	}
}
