package agentrun

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/briefing"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"gopkg.in/yaml.v3"
)

// listedTranscript writes a transcript of a session in dir under the fake
// Claude Code config folder whose skill_listing names skills.
func listedTranscript(t *testing.T, cfg, dir string, skills ...string) {
	t.Helper()
	folder := filepath.Join(cfg, "projects", regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(dir, "-"))
	os.MkdirAll(folder, 0o755)
	var listing strings.Builder
	listing.WriteString("The following skills are available for use with the Skill tool:\n\n")
	for _, s := range skills {
		listing.WriteString("- " + s + ": does " + s + " things.\n")
	}
	rec, _ := json.Marshal(map[string]any{"type": "attachment", "attachment": map[string]any{"type": "skill_listing", "content": listing.String()}})
	body := `{"type":"mode","mode":"normal"}` + "\n" + string(rec) + "\n"
	if err := os.WriteFile(filepath.Join(folder, "s.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

type workerSettingsDoc struct {
	SkillOverrides       map[string]string `json:"skillOverrides"`
	DisableBundledSkills bool              `json:"disableBundledSkills"`
}

// A lean worker starts without every skill its type does not keep: the
// personal ones on disk, the bundled and claude.ai-synced ones Claude Code
// listed in the project's transcripts, and Claude Code's Artifact tool. A
// type keeps a skill by workers: skills: {skill, types}; the briefing names
// none of the dropped ones.
func TestWorkerStartKeepsOnlyTheTypesSkills(t *testing.T) {
	cfg := ownerClaude(t) // personal skills dxverify, humanizer, stageshow
	proj := connectorProject(t)
	for slug, typ := range map[string]string{"quarterly": "report", "button": "feature"} {
		os.WriteFile(filepath.Join(proj.IssueDir, slug+".md"), []byte("---\ntitle: \""+slug+"\"\nstatus: \"backlog\"\nsystem: \"Engine\"\ntype: \""+typ+"\"\n---\nWrite it.\n"), 0o644)
	}
	// The lead's transcript in the root lists everything it has; a
	// worktree's adds one more.
	listedTranscript(t, cfg, proj.WorkDir, "dataviz", "claude-in-chrome", "anthropic-skills:pdf", "anthropic-skills:docx", "stageshow")
	listedTranscript(t, cfg, filepath.Join(proj.WorkDir, ".worktrees", "x"), "anthropic-skills:xlsx")
	// Another project's transcript is not read.
	listedTranscript(t, cfg, proj.WorkDir+"-other", "elsewhere")

	var wf tracker.WorkflowConfig
	if err := yaml.Unmarshal([]byte(`
workers:
  skills:
    - stageshow
    - {skill: "anthropic-skills:pdf", types: [report]}
`), &wf); err != nil {
		t.Fatal(err)
	}

	logs := t.TempDir()
	start := func(slug string) (workerSettingsDoc, WorkerSetup, []Step) {
		t.Helper()
		dir := filepath.Join(logs, "agent-"+slug)
		os.MkdirAll(dir, 0o755)
		var steps []Step
		ws := WorkerSetupFlags(proj, Spec{IssueSlug: slug, AgentType: "claude", Workflow: &wf}, proj.WorkDir, dir, nil, &steps)
		var doc workerSettingsDoc
		b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
		if err != nil || json.Unmarshal(b, &doc) != nil {
			t.Fatalf("%s: settings.json %v\n%s", slug, err, b)
		}
		return doc, ws, steps
	}

	dropped := []string{"dxverify", "humanizer", "dataviz", "claude-in-chrome", "anthropic-skills:docx", "anthropic-skills:xlsx"}
	for _, c := range []struct {
		slug       string
		pdfOff     bool
		bundledOff bool
	}{
		{"button", true, true},
		{"quarterly", false, false}, // keeps a skill not on disk: it may be bundled
	} {
		doc, ws, steps := start(c.slug)
		for _, name := range dropped {
			if doc.SkillOverrides[name] != "off" {
				t.Errorf("%s: %s = %q, want off (%v)", c.slug, name, doc.SkillOverrides[name], doc.SkillOverrides)
			}
		}
		if _, on := doc.SkillOverrides["stageshow"]; on {
			t.Errorf("%s: stageshow is kept by every worker: %v", c.slug, doc.SkillOverrides)
		}
		if _, ok := doc.SkillOverrides["elsewhere"]; ok {
			t.Errorf("%s: another project's transcript was read", c.slug)
		}
		if got := doc.SkillOverrides["anthropic-skills:pdf"] == "off"; got != c.pdfOff {
			t.Errorf("%s: anthropic-skills:pdf off = %v, want %v", c.slug, got, c.pdfOff)
		}
		if doc.DisableBundledSkills != c.bundledOff {
			t.Errorf("%s: disableBundledSkills = %v", c.slug, doc.DisableBundledSkills)
		}
		if !slices.Contains(ws.Env, "CLAUDE_CODE_DISABLE_ARTIFACT=1") {
			t.Errorf("%s: env %v", c.slug, ws.Env)
		}
		if names := stepNames(steps); !strings.Contains(names, "listed by Claude Code") || !strings.Contains(names, "Artifact off") {
			t.Errorf("%s: steps %s", c.slug, names)
		}

		issues, _ := proj.LoadIssues()
		prompt := briefing.BuildDispatchPrompt(proj, tracker.FindIssue(issues, c.slug), &wf, proj.WorkDir, nil).Prompt
		if prompt == "" {
			t.Fatalf("%s: empty briefing", c.slug)
		}
		for name := range doc.SkillOverrides {
			if strings.Contains(prompt, name) {
				t.Errorf("%s: the briefing names dropped skill %s", c.slug, name)
			}
		}
	}

	// workers: tools: [Artifact] keeps the Artifact tool.
	wf.Workers.Tools = []string{"Artifact"}
	if _, ws, _ := start("button"); slices.Contains(ws.Env, "CLAUDE_CODE_DISABLE_ARTIFACT=1") {
		t.Errorf("tools: [Artifact]: env %v", ws.Env)
	}

	// Once the transcripts list nothing more (every new one is a lean
	// worker's), the names earlier workers turned off still go off.
	os.RemoveAll(filepath.Join(cfg, "projects"))
	if doc, _, _ := start("button"); doc.SkillOverrides["anthropic-skills:docx"] != "off" {
		t.Errorf("no transcripts: an earlier worker's off skills are forgotten: %v", doc.SkillOverrides)
	}
}
