package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseIssue_Valid(t *testing.T) {
	data := []byte(`---
title: "My Issue"
status: "in progress"
system: "Combat"
version: "0.1"
labels:
  - bug
  - enhancement
priority: "high"
assignee: "alice"
created: "2025-01-15"
---

This is the body.
`)

	issue, err := ParseIssue("test.md", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if issue.Title != "My Issue" {
		t.Errorf("title = %q, want %q", issue.Title, "My Issue")
	}
	if issue.Status != "in progress" {
		t.Errorf("status = %q, want %q", issue.Status, "in progress")
	}
	if issue.System != "Combat" {
		t.Errorf("system = %q, want %q", issue.System, "Combat")
	}
	if issue.Version != "0.1" {
		t.Errorf("version = %q, want %q", issue.Version, "0.1")
	}
	if len(issue.Labels) != 2 || issue.Labels[0] != "bug" || issue.Labels[1] != "enhancement" {
		t.Errorf("labels = %v, want [bug enhancement]", issue.Labels)
	}
	if issue.Priority != "high" {
		t.Errorf("priority = %q, want %q", issue.Priority, "high")
	}
	if issue.Assignee != "alice" {
		t.Errorf("assignee = %q, want %q", issue.Assignee, "alice")
	}
	if issue.Slug != "my-issue" {
		t.Errorf("slug = %q, want %q", issue.Slug, "my-issue")
	}
	if !strings.Contains(issue.BodyHTML(), "This is the body.") {
		t.Errorf("BodyHTML missing body text: %q", issue.BodyHTML())
	}
	if !strings.Contains(issue.BodyRaw, "This is the body.") {
		t.Errorf("BodyRaw missing body text: %q", issue.BodyRaw)
	}
}

func TestParseIssue_ExtraFields(t *testing.T) {
	data := []byte(`---
title: "My Issue"
status: "backlog"
jira: "https://example.com/browse/TICKET-123"
pr: "https://github.com/org/repo/pull/456"
pr_author: "someone"
risk: "low"
participants:
  - alice
  - bob
---

body
`)
	issue, err := ParseIssue("test.md", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	byKey := map[string]ExtraField{}
	for _, ef := range issue.ExtraFields {
		byKey[ef.Key] = ef
	}

	if jira, ok := byKey["jira"]; !ok {
		t.Error("expected jira field")
	} else if !jira.IsURL {
		t.Errorf("jira.IsURL = false, want true")
	} else if jira.Value != "https://example.com/browse/TICKET-123" {
		t.Errorf("jira.Value = %q", jira.Value)
	}

	if pr, ok := byKey["pr"]; !ok {
		t.Error("expected pr field")
	} else if !pr.IsURL {
		t.Errorf("pr.IsURL = false, want true")
	}

	if author, ok := byKey["pr_author"]; !ok {
		t.Error("expected pr_author field")
	} else if author.Label != "Pr Author" {
		t.Errorf("pr_author.Label = %q, want %q", author.Label, "Pr Author")
	} else if author.IsURL || author.IsList {
		t.Errorf("pr_author should be plain text")
	}

	if risk, ok := byKey["risk"]; !ok {
		t.Error("expected risk field")
	} else if risk.Value != "low" {
		t.Errorf("risk.Value = %q, want %q", risk.Value, "low")
	}

	if parts, ok := byKey["participants"]; !ok {
		t.Error("expected participants field")
	} else if !parts.IsList {
		t.Errorf("participants.IsList = false, want true")
	} else if len(parts.Values) != 2 || parts.Values[0] != "alice" || parts.Values[1] != "bob" {
		t.Errorf("participants.Values = %v, want [alice bob]", parts.Values)
	}

	// Known fields should not appear in ExtraFields
	for _, ef := range issue.ExtraFields {
		if knownFrontmatterFields[ef.Key] {
			t.Errorf("known field %q leaked into ExtraFields", ef.Key)
		}
	}
}

func TestParseIssue_MissingFrontmatter(t *testing.T) {
	data := []byte("Just some text without frontmatter")
	_, err := ParseIssue("test.md", data)
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestParseIssue_StatusNormalization(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"In Progress", "in progress"},
		{"  DONE  ", "done"},
		{"Backlog", "backlog"},
		{"idea", "idea"},
	}

	for _, tt := range tests {
		data := []byte("---\ntitle: \"Test\"\nstatus: \"" + tt.input + "\"\n---\n\nbody")
		issue, err := ParseIssue("test.md", data)
		if err != nil {
			t.Fatalf("unexpected error for status %q: %v", tt.input, err)
		}
		if issue.Status != tt.want {
			t.Errorf("status %q normalized to %q, want %q", tt.input, issue.Status, tt.want)
		}
	}
}

func TestParseIssue_PriorityNormalization(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"High", "high"},
		{"  LOW  ", "low"},
		{"CRITICAL", "critical"},
	}

	for _, tt := range tests {
		data := []byte("---\ntitle: \"Test\"\npriority: \"" + tt.input + "\"\n---\n\nbody")
		issue, err := ParseIssue("test.md", data)
		if err != nil {
			t.Fatalf("unexpected error for priority %q: %v", tt.input, err)
		}
		if issue.Priority != tt.want {
			t.Errorf("priority %q normalized to %q, want %q", tt.input, issue.Priority, tt.want)
		}
	}
}

func TestParseIssue_EmptyLabels(t *testing.T) {
	data := []byte("---\ntitle: \"Test\"\n---\n\nbody")
	issue, err := ParseIssue("test.md", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.Labels != nil {
		t.Errorf("labels = %v, want nil", issue.Labels)
	}
}

func TestLoadIssues(t *testing.T) {
	dir := t.TempDir()

	// Create a root-level issue
	os.WriteFile(filepath.Join(dir, "root-issue.md"), []byte(`---
title: "Root Issue"
status: "idea"
---

Root body.
`), 0644)

	// Create a subdirectory with an issue
	subDir := filepath.Join(dir, "Combat")
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, "sub-issue.md"), []byte(`---
title: "Sub Issue"
status: "backlog"
system: "Combat"
---

Sub body.
`), 0644)

	issues, err := LoadIssues(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}

	// Check that subdirectory slug includes the directory prefix
	slugs := map[string]bool{}
	for _, iss := range issues {
		slugs[iss.Slug] = true
	}
	if !slugs["root-issue"] {
		t.Error("missing slug 'root-issue'")
	}
	if !slugs["combat/sub-issue"] {
		t.Errorf("missing slug 'combat/sub-issue', got slugs: %v", slugs)
	}
}

func TestLoadIssues_SystemFromSubdir(t *testing.T) {
	dir := t.TempDir()

	// Issue in subdirectory without system in frontmatter
	subDir := filepath.Join(dir, "Combat")
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, "no-system.md"), []byte(`---
title: "No System Field"
status: "idea"
---

Body.
`), 0644)

	// Issue in subdirectory WITH system in frontmatter (should keep frontmatter value)
	os.WriteFile(filepath.Join(subDir, "has-system.md"), []byte(`---
title: "Has System Field"
status: "idea"
system: "OverrideSystem"
---

Body.
`), 0644)

	issues, err := LoadIssues(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, iss := range issues {
		switch iss.Title {
		case "No System Field":
			if iss.System != "Combat" {
				t.Errorf("expected system 'Combat' from subdir, got %q", iss.System)
			}
		case "Has System Field":
			if iss.System != "OverrideSystem" {
				t.Errorf("expected system 'OverrideSystem' from frontmatter, got %q", iss.System)
			}
		}
	}
}

func TestLoadIssues_SlugCollision(t *testing.T) {
	dir := t.TempDir()

	// Two issues with the same title
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("---\ntitle: \"Same Title\"\n---\n\nbody a"), 0644)
	os.WriteFile(filepath.Join(dir, "b.md"), []byte("---\ntitle: \"Same Title\"\n---\n\nbody b"), 0644)

	issues, err := LoadIssues(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}

	slugs := map[string]bool{}
	for _, iss := range issues {
		slugs[iss.Slug] = true
	}
	// One should be "same-title" and the other "same-title-2"
	if len(slugs) != 2 {
		t.Errorf("expected 2 unique slugs, got %v", slugs)
	}
}

func TestLoadIssues_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	issues, err := LoadIssues(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("got %d issues, want 0", len(issues))
	}
}

func TestDeleteIssue(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")
	os.WriteFile(fp, []byte("---\ntitle: \"Test\"\n---\n"), 0644)

	err := DeleteIssue(fp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(fp); !os.IsNotExist(err) {
		t.Error("file should have been deleted")
	}
}

func TestDeleteIssue_WithCommentSidecar(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")
	commentFp := filepath.Join(dir, "test.comments.yaml")
	os.WriteFile(fp, []byte("---\ntitle: \"Test\"\n---\n"), 0644)
	os.WriteFile(commentFp, []byte("some comments"), 0644)

	err := DeleteIssue(fp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(fp); !os.IsNotExist(err) {
		t.Error("issue file should have been deleted")
	}
	if _, err := os.Stat(commentFp); !os.IsNotExist(err) {
		t.Error("comment sidecar should have been deleted")
	}
}

func TestCreateIssueFile(t *testing.T) {
	dir := t.TempDir()

	fp, slug, err := CreateIssueFile(dir, "My New Issue", "backlog", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if slug != "my-new-issue" {
		t.Errorf("slug = %q, want %q", slug, "my-new-issue")
	}

	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("failed to read created file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "My New Issue") {
		t.Error("file missing title")
	}
	if !strings.Contains(content, "backlog") {
		t.Error("file missing status")
	}
}

func TestCreateIssueFileOpts_WritesCreated(t *testing.T) {
	dir := t.TempDir()
	fp, _, err := CreateIssueFileOpts(dir, CreateIssueOpts{Title: "Dated", Now: time.Date(2026, 10, 24, 9, 0, 0, 0, time.Local)})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(fp)
	issue, err := ParseIssue(fp, data)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Created != "2026-10-24" {
		t.Errorf("created = %q, want 2026-10-24", issue.Created)
	}
}

func TestCreateIssueFile_WithSystem(t *testing.T) {
	dir := t.TempDir()

	fp, slug, err := CreateIssueFile(dir, "System Issue", "idea", "Combat", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if slug != "combat/system-issue" {
		t.Errorf("slug = %q, want %q", slug, "combat/system-issue")
	}
	if !strings.Contains(fp, "Combat") {
		t.Errorf("file path should contain system dir: %s", fp)
	}

	data, _ := os.ReadFile(fp)
	if !strings.Contains(string(data), `system: "Combat"`) {
		t.Error("file missing system field")
	}
}

func TestCreateIssueFile_EmptyTitle(t *testing.T) {
	dir := t.TempDir()
	_, _, err := CreateIssueFile(dir, "", "idea", "", "")
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestCreateIssueFile_DefaultStatus(t *testing.T) {
	dir := t.TempDir()
	fp, _, err := CreateIssueFile(dir, "Default Status", "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(fp)
	if !strings.Contains(string(data), `status: "idea"`) {
		t.Error("default status should be 'idea'")
	}
}

func TestCollectFilterValues(t *testing.T) {
	issues := []*Issue{
		{Status: "idea", System: "Combat", Priority: "high", Labels: []string{"bug"}, Assignee: "alice"},
		{Status: "done", System: "UI", Priority: "low", Labels: []string{"bug", "ui"}, Assignee: "bob"},
		{Status: "idea", System: "Combat", Priority: "high", Labels: []string{"enhancement"}},
	}

	statuses, systems, priorities, labels, assignees := CollectFilterValues(issues)

	if len(statuses) != 2 {
		t.Errorf("statuses = %v, want 2 items", statuses)
	}
	if len(systems) != 2 {
		t.Errorf("systems = %v, want 2 items", systems)
	}
	if len(priorities) != 2 {
		t.Errorf("priorities = %v, want 2 items", priorities)
	}
	if len(labels) != 3 {
		t.Errorf("labels = %v, want 3 items", labels)
	}
	if len(assignees) != 2 {
		t.Errorf("assignees = %v, want 2 items", assignees)
	}
}

func TestParseIssueSlugPin(t *testing.T) {
	iss, err := ParseIssue("x.md", []byte("---\ntitle: \"A New Title\"\nslug: \"Old Name\"\n---\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	if iss.Slug != "old-name" {
		t.Fatalf("slug = %q, want the pin slugified (old-name)", iss.Slug)
	}
	for _, f := range iss.ExtraFields {
		if f.Key == "slug" {
			t.Fatal("slug: shows as a custom field")
		}
	}
	iss, _ = ParseIssue("x.md", []byte("---\ntitle: \"A New Title\"\n---\n"))
	if iss.Slug != "a-new-title" {
		t.Fatalf("without a pin the slug comes from the title, got %q", iss.Slug)
	}
}

func TestRetitleUpdate(t *testing.T) {
	iss := &Issue{Title: "old", Slug: "cli/old"}
	if u := RetitleUpdate(iss, "new", false); u.Slug == nil || *u.Slug != "old" {
		t.Fatalf("retitle should pin the current slug, got %v", u.Slug)
	}
	if u := RetitleUpdate(iss, "Old", false); u.Slug != nil {
		t.Fatal("a title with the same slug needs no pin")
	}
	iss.SlugPin = "old"
	if u := RetitleUpdate(iss, "newer", false); u.Slug != nil {
		t.Fatal("an existing pin is kept as is")
	}
	if u := RetitleUpdate(iss, "newer", true); u.Slug == nil || *u.Slug != "" {
		t.Fatal("--rename removes the pin")
	}
}
