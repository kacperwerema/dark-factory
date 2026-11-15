package tracker

import (
	"testing"
)

func TestTemplateForStatus(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "idea", Template: "## Ideas\n- Item 1\n"},
			{Name: "done", Template: ""},
		},
	}

	t.Run("existing with template", func(t *testing.T) {
		tmpl := wf.TemplateForStatus("idea")
		if tmpl != "## Ideas\n- Item 1" {
			t.Errorf("template = %q", tmpl)
		}
	})

	t.Run("existing without template", func(t *testing.T) {
		tmpl := wf.TemplateForStatus("done")
		if tmpl != "" {
			t.Errorf("template = %q, want empty", tmpl)
		}
	})

	t.Run("missing status", func(t *testing.T) {
		tmpl := wf.TemplateForStatus("unknown")
		if tmpl != "" {
			t.Errorf("template = %q, want empty", tmpl)
		}
	})
}

func TestAppendTemplate(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "testing", Template: "## Test Plan\n- [ ] Test 1\n"},
			{Name: "idea", Template: ""},
		},
	}

	t.Run("append to body", func(t *testing.T) {
		body, appended := wf.AppendTemplate("Existing content", "testing")
		if !appended {
			t.Fatal("expected template to be appended")
		}
		if body != "Existing content\n\n## Test Plan\n- [ ] Test 1\n" {
			t.Errorf("body = %q", body)
		}
	})

	t.Run("append to empty body", func(t *testing.T) {
		body, appended := wf.AppendTemplate("", "testing")
		if !appended {
			t.Fatal("expected template to be appended")
		}
		if body != "## Test Plan\n- [ ] Test 1\n" {
			t.Errorf("body = %q", body)
		}
	})

	t.Run("duplicate guard", func(t *testing.T) {
		body, appended := wf.AppendTemplate("## Test Plan\n- [x] Test 1", "testing")
		if appended {
			t.Fatal("should not append duplicate")
		}
		if body != "## Test Plan\n- [x] Test 1" {
			t.Errorf("body changed: %q", body)
		}
	})

	t.Run("existing section gets the missing boxes", func(t *testing.T) {
		body, appended := wf.AppendTemplate("## Test Plan\nAlready here", "testing")
		if !appended {
			t.Fatal("expected the missing box to be added")
		}
		if body != "## Test Plan\nAlready here\n\n- [ ] Test 1\n" {
			t.Errorf("body = %q", body)
		}
	})

	t.Run("heading text in prose or a fence is not the section", func(t *testing.T) {
		in := "Mentions ## Test Plan inline.\n\n```\n## Test Plan\n```"
		body, appended := wf.AppendTemplate(in, "testing")
		if !appended {
			t.Fatal("expected template to be appended")
		}
		if body != in+"\n\n## Test Plan\n- [ ] Test 1\n" {
			t.Errorf("body = %q", body)
		}
	})

	t.Run("no template for status", func(t *testing.T) {
		body, appended := wf.AppendTemplate("content", "idea")
		if appended {
			t.Fatal("should not append empty template")
		}
		if body != "content" {
			t.Errorf("body = %q", body)
		}
	})
}

func TestAppendToSection(t *testing.T) {
	t.Run("creates section when missing", func(t *testing.T) {
		body, changed := appendToSection("## Existing\n- item", "Testing", "- [ ] add test")
		if !changed {
			t.Fatal("expected section append")
		}
		want := "## Existing\n- item\n\n## Testing\n- [ ] add test\n"
		if body != want {
			t.Errorf("body = %q, want %q", body, want)
		}
	})

	t.Run("reuses existing section", func(t *testing.T) {
		body, changed := appendToSection("## Testing\n- [ ] existing", "Testing", "- [ ] new item")
		if changed {
			t.Fatal("expected no change when section already exists")
		}
		want := "## Testing\n- [ ] existing"
		if body != want {
			t.Errorf("body = %q, want %q", body, want)
		}
	})

	t.Run("does not duplicate identical content", func(t *testing.T) {
		body, changed := appendToSection("## Testing\n- [ ] same", "Testing", "- [ ] same")
		if changed {
			t.Fatal("expected no change for duplicate content")
		}
		if body != "## Testing\n- [ ] same" {
			t.Errorf("body changed: %q", body)
		}
	})

	t.Run("does not append into existing section with different content", func(t *testing.T) {
		body, changed := appendToSection("## Design\n- [ ] existing item", "Design", "- [ ] another item")
		if changed {
			t.Fatal("expected existing section to be left unchanged")
		}
		if body != "## Design\n- [ ] existing item" {
			t.Errorf("body changed: %q", body)
		}
	})

	t.Run("matches normalized headings", func(t *testing.T) {
		body, changed := appendToSection("###   Design  \nexisting detail", "## design", "new detail")
		if changed {
			t.Fatal("expected normalized heading match to be treated as existing section")
		}
		want := "###   Design  \nexisting detail"
		if body != want {
			t.Errorf("body = %q, want %q", body, want)
		}
	})
}

func TestFindHeadingMatches_IgnoresFencedCodeBlocks(t *testing.T) {
	body := "intro\n" +
		"\n" +
		"```markdown\n" +
		"## Documentation\n" +
		"- [ ] inside fence\n" +
		"```\n" +
		"\n" +
		"## Implementation\n" +
		"- [x] real heading\n"

	if got := findHeadingMatches(body, "Documentation"); len(got) != 0 {
		t.Fatalf("findHeadingMatches matched fenced heading: %#v", got)
	}
	if got := findHeadingMatches(body, "Implementation"); len(got) != 1 {
		t.Fatalf("findHeadingMatches missed real heading: %#v", got)
	}

	// Tilde fences and indentation should also be respected.
	tildeBody := "  ~~~\n" +
		"## Documentation\n" +
		"  ~~~\n"
	if got := findHeadingMatches(tildeBody, "Documentation"); len(got) != 0 {
		t.Fatalf("findHeadingMatches matched heading inside tilde fence: %#v", got)
	}
}
