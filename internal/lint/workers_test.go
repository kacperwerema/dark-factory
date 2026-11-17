package lint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func workersFindings(t *testing.T, raw string) []string {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	os.WriteFile(filepath.Join(cfg, ".claude.json"), []byte(`{"mcpServers": {"chrome-devtools": {"command": "npx"}}}`), 0o644)
	got, err := File(filepath.Join(t.TempDir(), "workflow.yaml"), []byte(raw), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range got {
		if f.Check == "workers" {
			out = append(out, f.Message)
		}
	}
	return out
}

func TestWorkersLint(t *testing.T) {
	good := routingBase + `workers:
  mcp:
    - {server: chrome-devtools, systems: [UI], types: [feature]}
  plugins: [frontend-design@claude-plugins-official]
  skills: [stageshow]
`
	if got := workersFindings(t, good); len(got) != 0 {
		t.Fatalf("findings = %q", got)
	}
	bad := routingBase + `workers:
  setup: tiny
  mcp:
    - {server: playwrite, types: [epic]}
    - {systems: [UI]}
  plugins: [frontend-design]
`
	got := strings.Join(workersFindings(t, bad), "\n")
	for _, want := range []string{`setup: "tiny"`, `"playwrite", which the owner's Claude Code config does not define`, "item 2 names no server", `type "epic"`, `"frontend-design" is not a plugin id`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// A kept skill nothing lists, a skills: type the file does not define and
// a tool workers: tools: cannot keep are warnings.
func TestWorkersLintSkillsAndTools(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := t.TempDir()
	folder := filepath.Join(cfg, "projects", regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(root, "-"))
	os.MkdirAll(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "s.jsonl"), []byte(`{"type":"attachment","attachment":{"type":"skill_listing","content":"- dataviz: x\n- anthropic-skills:pdf: y\n"}}`+"\n"), 0o644)
	raw := routingBase + `workers:
  skills:
    - dataviz
    - {skill: "anthropic-skills:pdf", types: [feature]}
    - {skill: pdf, types: [epic]}
  tools: [Artifact, Browser]
`
	got, err := File(filepath.Join(root, "workflow.yaml"), []byte(raw), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, f := range got {
		if f.Check == "workers" {
			msgs = append(msgs, f.Message)
		}
	}
	all := strings.Join(msgs, "\n")
	for _, want := range []string{`keeps "pdf", which no skill folder holds`, `skills: pdf names type "epic"`, `tools: "Browser" is not a tool`} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, `"dataviz"`) || strings.Contains(all, `"anthropic-skills:pdf", which`) || strings.Contains(all, `"Artifact"`) {
		t.Errorf("a listed skill or a known tool warned:\n%s", all)
	}
}
