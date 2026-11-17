package docref

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const cliPage = "---\ntitle: \"CLI Overview\"\norder: 1\n---\n\nThe CLI system covers issue-cli. More words here.\n\n## Commands\n\n### `start`\n\n`issue-cli start <slug>` is the single entry point. It claims.\n\n```\n## not a heading\n```\n\n#### In a dispatched session\n\nIn a dispatched session it reads the prompt.\n\n### `transition`\n\n| table | row |\n\n`issue-cli transition` runs the engine. Same path as the board.\n\n## Exit codes\n\nZero is success, and more follows here.\n"

func docsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range map[string]string{
		"CLI/overview.md":   cliPage,
		"cli-legacy.md":     "# Legacy cli\n\nOld.\n",
		"Workflow/proof.md": "---\ntitle: \"Proof Links\"\n---\n\n## Proof that waits on the lead\n\nSome proof needs the lead.\n",
		"departments.md":    "# Departments\n\n## Settled rules\n\nRules.\n",
	} {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestResolveByKeyPathAndTitle(t *testing.T) {
	dir := docsDir(t)
	for topic, want := range map[string]string{
		"cli":                    "CLI/overview.md",
		"docs/Workflow/proof.md": "Workflow/proof.md",
		"workflow/proof":         "Workflow/proof.md",
		"proof links":            "Workflow/proof.md",
		"departments":            "departments.md",
	} {
		p, err := Resolve(dir, topic)
		if err != nil || p.Rel != want {
			t.Errorf("Resolve(%q) = %v, %v; want %s", topic, p, err, want)
		}
	}
	if _, err := Resolve(dir, "nothing-like-it"); err == nil || !strings.Contains(err.Error(), "issue-cli ref lists them") {
		t.Errorf("unknown page: %v", err)
	}
}

// The outline holds each heading with its first sentence and line, skips
// fenced code and tables, and is much smaller than the page.
func TestOutlineHeadingsAndFirstSentences(t *testing.T) {
	p, err := Resolve(docsDir(t), "cli")
	if err != nil {
		t.Fatal(err)
	}
	out := p.Outline("issue-cli ref cli <heading words>")
	for _, want := range []string{
		"CLI Overview (docs/CLI/overview.md,",
		"The CLI system covers issue-cli.",
		"## Commands  [L8]",
		"  ### `start`  [L10] — `issue-cli start <slug>` is the single entry point.",
		"    #### In a dispatched session",
		"### `transition`",
		"— `issue-cli transition` runs the engine.",
		"## Exit codes",
		"One section in full: issue-cli ref cli <heading words>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("outline misses %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "not a heading") || strings.Contains(out, "table") {
		t.Errorf("outline has fenced code or a table:\n%s", out)
	}
}

func TestSectionInFull(t *testing.T) {
	p, _ := Resolve(docsDir(t), "cli")
	out, err := p.Section([]string{"start"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "docs/CLI/overview.md lines 10-21:") || !strings.Contains(out, "In a dispatched session it reads") || strings.Contains(out, "transition") {
		t.Fatalf("section:\n%s", out)
	}
	if _, err := p.Section([]string{"zzz"}); err == nil || !strings.Contains(err.Error(), "Commands · Exit codes") {
		t.Fatalf("no match: %v", err)
	}
}

// Measure counts the sessions that read each docs page (Read or a shell
// read), and each session's first-hour tokens once per message id.
func TestMeasureReadsAndFirstHour(t *testing.T) {
	projects := t.TempDir()
	folder := filepath.Join(projects, "-repo--worktrees-a")
	other := filepath.Join(projects, "-elsewhere")
	for _, d := range []string{folder, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	turn := func(ts, id string, in int, tool string) string {
		return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"id":%q,"usage":{"input_tokens":%d,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":0},"content":[%s]}}`, ts, id, in, tool)
	}
	read := `{"type":"tool_use","name":"Read","input":{"file_path":"/repo/.worktrees/a/docs/CLI/overview.md"}}`
	cat := `{"type":"tool_use","name":"Bash","input":{"command":"sed -n 1,40p docs/workflow.md"}}`
	edit := `{"type":"tool_use","name":"Bash","input":{"command":"git add docs/departments.md"}}`
	s1 := strings.Join([]string{
		turn("2026-11-15T09:00:00Z", "m1", 100, read),
		turn("2026-11-15T09:00:00Z", "m1", 100, ""),
		turn("2026-11-15T09:30:00Z", "m2", 50, cat+","+edit),
		turn("2026-11-15T10:30:00Z", "m3", 1000, read),
	}, "\n")
	s2 := turn("2026-11-15T09:00:00Z", "n1", 10, read)
	_ = os.WriteFile(filepath.Join(folder, "s1.jsonl"), []byte(s1), 0o644)
	_ = os.WriteFile(filepath.Join(folder, "s2.jsonl"), []byte(s2), 0o644)
	_ = os.WriteFile(filepath.Join(other, "s3.jsonl"), []byte(s2), 0o644)
	rep := Measure(projects, "-repo--worktrees-", time.Now().Add(-time.Hour))
	if rep.Sessions != 2 {
		t.Fatalf("sessions = %d", rep.Sessions)
	}
	if rep.Median() != 80 { // 150 and 10
		t.Fatalf("median = %d (%v)", rep.Median(), rep.FirstHour)
	}
	if len(rep.Pages) != 2 || rep.Pages[0].Rel != "CLI/overview.md" || rep.Pages[0].Sessions != 2 || rep.Pages[0].Reads != 3 || rep.Pages[1].Rel != "workflow.md" {
		t.Fatalf("pages = %+v", rep.Pages)
	}
}
