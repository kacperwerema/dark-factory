package tracker

import (
	"regexp"
	"strings"
	"testing"
)

func TestPromptDeliveryMarker(t *testing.T) {
	cases := []struct{ name, prompt, want string }{
		{"last non-empty line", "first\nsecond\n\n  last one  \n\n", "last one"},
		{"single line", "only", "only"},
		{"blank", " \n\n", ""},
		{"truncated to 40 runes", "x\n" + strings.Repeat("é", 50), strings.Repeat("é", 40)},
	}
	for _, c := range cases {
		if got := PromptDeliveryMarker(c.prompt); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// The How to work section's retrospective command must name the issue's
// slug: `retrospective <System>` fuzzy-matches some other issue of that
// system and files the retro there.
func TestBuildAgentPrompt_RetrospectiveNamesSlugNotSystem(t *testing.T) {
	issue := &Issue{Slug: "campaign/fix-loot", Title: "Fix loot", Status: "idea", System: "Campaign", BodyRaw: "Body."}
	prompt := BuildAgentPrompt(nil, issue, nil, "", "")

	if !strings.Contains(prompt, `issue-cli retrospective campaign/fix-loot --body "Base workflow: ...\nSubsystem workflow for Campaign: ...`) {
		t.Fatalf("workflow review block does not name the slug:\n\n%s", prompt)
	}
	if strings.Contains(prompt, "retrospective Campaign") {
		t.Fatalf("retrospective command names the system:\n\n%s", prompt)
	}
}

// Every {{name}} in the template has a field and every field is used, so a
// renamed or misspelt placeholder can't reach an agent unfilled.
func TestAgentDispatchPromptTemplate_EveryPlaceholderFilled(t *testing.T) {
	issue := &Issue{Slug: "the-slug", System: "the-system", Title: "the-title", Priority: "the-priority", BodyRaw: "the-body"}
	fields := agentPromptFields(issue, nil, "the-guidance", "the-reminder", "the-status", false)

	used := map[string]bool{}
	for _, m := range regexp.MustCompile(`\{\{(\w+)\}\}`).FindAllStringSubmatch(AgentDispatchPromptTemplate, -1) {
		used[m[1]] = true
		if _, ok := fields[m[1]]; !ok {
			t.Errorf("template placeholder {{%s}} has no field", m[1])
		}
	}
	for name, value := range fields {
		if !used[name] {
			t.Errorf("field %q is not used by the template", name)
		}
		if !strings.Contains(renderPromptTemplate(AgentDispatchPromptTemplate, fields), value) {
			t.Errorf("rendered prompt lacks %q (field %q)", value, name)
		}
	}
	if got := renderPromptTemplate(AgentDispatchPromptTemplate, fields); strings.Contains(got, "{{") {
		t.Fatalf("rendered prompt still has a placeholder:\n\n%s", got)
	}
}

// A value is not re-scanned: an issue body quoting {{slug}} stays literal.
func TestRenderPromptTemplate_LeavesValuesAlone(t *testing.T) {
	got := renderPromptTemplate("{{slug}} / {{body}}", map[string]string{"slug": "a/b", "body": "see {{slug}}"})
	if got != "a/b / see {{slug}}" {
		t.Fatalf("got %q", got)
	}
}

// A command quoted as text or as an argument stays as written; one the
// reader is told to run gets --project (the sources: retros P18, P32, R4).
func TestInjectProjectLeavesQuotedCommandsAlone(t *testing.T) {
	proj := &Project{Slug: "p"}
	cases := []struct{ name, in, want string }{
		{"a command", "Run: issue-cli show x", "Run: issue-cli --project p show x"},
		{"in backticks", "see `issue-cli show x`.", "see `issue-cli --project p show x`."},
		{"a relative build", "`./issue-cli status`", "`./issue-cli --project p status`"},
		{"prose single quotes", "Run 'issue-cli process workflow' for details", "Run 'issue-cli --project p process workflow' for details"},
		{"an apostrophe", "the lead's issue-cli show x", "the lead's issue-cli --project p show x"},
		{"every command on a line", "issue-cli a or issue-cli b", "issue-cli --project p a or issue-cli --project p b"},
		{"every line", "issue-cli a\nissue-cli b", "issue-cli --project p a\nissue-cli --project p b"},
		{"a flag's value", `issue-cli prove s AC2 --lead "issue-cli check-script s --yes", then stop`, `issue-cli --project p prove s AC2 --lead "issue-cli check-script s --yes", then stop`},
		{"a flag's value after =", `issue-cli prove s AC2 --lead="issue-cli check-script s"`, `issue-cli --project p prove s AC2 --lead="issue-cli check-script s"`},
		{"a flag's value in single quotes", `issue-cli prove s AC2 --lead 'issue-cli check-script s'`, `issue-cli --project p prove s AC2 --lead 'issue-cli check-script s'`},
		{"a comment's text", `issue-cli comment s "question: does issue-cli show x work?"`, `issue-cli --project p comment s "question: does issue-cli show x work?"`},
		{"an escaped quote", `issue-cli comment s "a \"b\" issue-cli show"`, `issue-cli --project p comment s "a \"b\" issue-cli show"`},
		{"after a closed quote", `"text" then issue-cli show x`, `"text" then issue-cli --project p show x`},
		{"an unclosed quote is a character", `5" wide: issue-cli show x`, `5" wide: issue-cli --project p show x`},
		{"a quote ends with its line", "\"open\nissue-cli show x", "\"open\nissue-cli --project p show x"},
		{"already named", "issue-cli --project q show x", "issue-cli --project q show x"},
		{"already named with =", "issue-cli --project=q show x", "issue-cli --project=q show x"},
		{"named after --config", "issue-cli --config X --project Y show s", "issue-cli --config X --project Y show s"},
		{"named after --json and --config=", "issue-cli --json --config=X --project=Y show s", "issue-cli --json --config=X --project=Y show s"},
		{"--config without --project", "issue-cli --config X show s", "issue-cli --project p --config X show s"},
		{"--project after the command is its own flag", "issue-cli show s --project Y", "issue-cli --project p show s --project Y"},
		{"twice", InjectProject(proj, "issue-cli show x"), "issue-cli --project p show x"},
		{"the bare word", "the issue-cli binary", "the issue-cli --project p binary"},
		{"no trailing space", "use issue-cli.", "use issue-cli."},
	}
	for _, c := range cases {
		if got := InjectProject(proj, c.in); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	for _, p := range []*Project{nil, {}} {
		if got := InjectProject(p, "issue-cli show x"); got != "issue-cli show x" {
			t.Errorf("no project slug: %q", got)
		}
	}
}

// An issue-cli glued to a path or a name is no command: a brief naming
// cmd/issue-cli read back as "cmd/issue-cli --project p" (2 retros,
// 2026-11-15). The worker's own ./issue-cli build is still a command.
func TestInjectLineSkipsPathsEndingInIssueCli(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"a package path", "touches cmd/issue-cli and more", "touches cmd/issue-cli and more"},
		{"a relative package path", "go test ./cmd/issue-cli -run X", "go test ./cmd/issue-cli -run X"},
		{"a module path", "github.com/o/r/cmd/issue-cli is the CLI", "github.com/o/r/cmd/issue-cli is the CLI"},
		{"in backticks", "`cmd/issue-cli` holds it", "`cmd/issue-cli` holds it"},
		{"a hyphenated name", "the my-issue-cli tool", "the my-issue-cli tool"},
		{"a word character", "xissue-cli show", "xissue-cli show"},
		{"a dotted name", "the .issue-cli file", "the .issue-cli file"},
		{"an underscore", "a_issue-cli show", "a_issue-cli show"},
		{"the worker's build", "run ./issue-cli show x", "run ./issue-cli --project p show x"},
		{"the build at line start", "./issue-cli show x", "./issue-cli --project p show x"},
		{"the build in backticks", "`./issue-cli show x`", "`./issue-cli --project p show x`"},
		{"a path then a command", "in cmd/issue-cli run issue-cli show x", "in cmd/issue-cli run issue-cli --project p show x"},
		{"after a paren", "(issue-cli show x)", "(issue-cli --project p show x)"},
		{"a path starts no command", "cmd/issue-cli 'issue-cli show x'", "cmd/issue-cli 'issue-cli --project p show x'"},
	}
	for _, c := range cases {
		if got := injectLine("p", c.in); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// The title and the body's quoted lines are the issue's own words; a body
// line telling the worker what to run (a box a side-effect wrote) is not.
func TestAgentPromptKeepsQuotedTextAsWritten(t *testing.T) {
	proj := &Project{Slug: "proj"}
	issue := &Issue{Slug: "cli/x", Status: "idea", Priority: "high",
		Title: "issue-cli list --parent <epic> and decisions <slug>",
		BodyRaw: strings.Join([]string{
			`- "'issue-cli decisions <slug>' is not accepted" (20261031)`,
			"> issue-cli blockquoted line",
			"  > issue-cli indented blockquote",
			"```",
			"issue-cli fenced line",
			"```",
			"~~~sh",
			"issue-cli tilde fence",
			"~~~",
			`- [ ] Tests logged (` + "`issue-cli comment cli/x --text \"tests: ...\"`" + `)`,
		}, "\n")}
	got := BuildAgentPrompt(proj, issue, &WorkflowConfig{}, "", "")
	for _, kept := range []string{
		"You have been assigned this issue: cli/x",
		"  Title: issue-cli list --parent <epic> and decisions <slug>\n",
		`- "'issue-cli decisions <slug>' is not accepted" (20261031)`,
		"\n> issue-cli blockquoted line\n",
		"\n  > issue-cli indented blockquote\n",
		"\nissue-cli fenced line\n",
		"\nissue-cli tilde fence\n",
		"`issue-cli --project proj comment cli/x --text \"tests: ...\"`",
		"issue-cli --project proj start cli/x",
		"issue-cli --project proj rounds cli/x",
	} {
		if !strings.Contains(got, kept) {
			t.Errorf("prompt lost %q", kept)
		}
	}
	if strings.Contains(got, "--project proj --project") {
		t.Errorf("--project injected twice")
	}
}

// A value holding {{...}} is left as written, however the prompt is built.
func TestAgentPromptPlaceholderInAValueStays(t *testing.T) {
	issue := &Issue{Slug: "cli/x", Status: "idea", Title: "T", BodyRaw: "body {{title}} and {{slug}}"}
	wf := &WorkflowConfig{}
	got := BuildAgentPrompt(&Project{Slug: "proj"}, issue, wf, "", "")
	if !strings.Contains(got, "body {{title}} and {{slug}}") {
		t.Errorf("a body's placeholder was filled:\n%s", got)
	}
}

// The red-on-main rule has two halves that must agree: the Local CI section
// says to file a failure main already has, and "ask first" lets the worker.
func TestBriefingCarriesTheRedOnMainRule(t *testing.T) {
	old := CIStatusLine
	CIStatusLine = func(string, *WorkflowConfig) string { return "Local CI: main red since abc1234" }
	t.Cleanup(func() { CIStatusLine = old })

	section := CIPromptSection(t.TempDir(), &WorkflowConfig{})
	for _, want := range []string{
		"a failure main already has is not yours to fix",
		"If it has no issue, file it (`issue-cli create`)",
		"if it has one, name it in your `tests:` comment",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Local CI section lacks %q:\n%s", want, section)
		}
	}
	if !strings.Contains(section, "When your project's tests fail on your branch") || strings.Contains(section, "make validate") {
		t.Errorf("no ci: command: the section should fall back to the project's tests:\n%s", section)
	}
	npm := CIPromptSection(t.TempDir(), &WorkflowConfig{CI: &CIConfig{Run: []CICommand{{Cmd: "npm test"}}}})
	if !strings.Contains(npm, "When `npm test` or a test fails on your branch") || strings.Contains(npm, "make validate") {
		t.Errorf("ci: npm test: the section should name it:\n%s", npm)
	}

	prompt := BuildAgentPrompt(nil, &Issue{Slug: "s", Title: "t"}, nil, "", "")
	line := ""
	for _, l := range strings.Split(prompt, "\n") {
		if strings.Contains(l, "creating or deleting issues") {
			line = l
		}
	}
	for _, want := range []string{"Ask the user first", "a failure main already has that no issue names"} {
		if !strings.Contains(line, want) {
			t.Errorf("the ask-first line lacks %q: %q", want, line)
		}
	}
}

// A brief's theory is a proposal: the lead's wrong cause reached a title and an
// AC (retro sweep f877, cause N4). The line names both commands a worker needs
// when the theory falls, under --project like every other command it reads.
func TestBriefingSaysABriefIsAHypothesis(t *testing.T) {
	proj := &Project{Slug: "proj"}
	issue := &Issue{Slug: "api/fix", Title: "Fix", Status: "idea", BodyRaw: "Body."}
	prompt := BuildAgentPrompt(proj, issue, nil, "", "")

	for _, want := range []string{
		"A brief's cause, cache key, fix or quoted form is a proposal",
		"measure it on the unchanged code or read the parser first",
		"record a D-n (step 4) or retitle (update --title)",
		`issue-cli --project proj decide api/fix --provisional --text "..."`,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("briefing lacks %q:\n\n%s", want, prompt)
		}
	}
}
