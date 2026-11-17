package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const roadmapTo060 = `# Roadmap

## Versions

| Version | Focus | Issues | Released when |
|:--|:--|:--|:--|
| **0.59** Spec flow | x | y | z |
| **0.60** Decision ledger | x | y | z |
| **1.0** Stable | x | y | z |
`

// roadmapEnv is a lint project with docs/roadmap.md holding roadmap and
// one issue per version (the last one done).
func roadmapEnv(t *testing.T, roadmap string, versions ...string) *Env {
	t.Helper()
	issues := map[string]string{}
	for i, v := range versions {
		issues["CLI/"+string(rune('a'+i))+".md"] = "---\ntitle: \"I" + v + "\"\nstatus: idea\nversion: \"" + v + "\"\ncreated: \"2026-01-02\"\n---\n"
	}
	env := lintProject(t, minimalWorkflow, issues)
	if roadmap != "" {
		docs := filepath.Join(env.Project.Root(), "docs")
		os.MkdirAll(docs, 0755)
		if err := os.WriteFile(filepath.Join(docs, "roadmap.md"), []byte(roadmap), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return env
}

func TestLintRoadmapBehind(t *testing.T) {
	env := roadmapEnv(t, roadmapTo060, "0.60.1", "0.62", "0.61", "later")
	fs := run(t, env, "roadmap-behind")
	if len(fs) != 1 {
		t.Fatalf("findings = %+v, want one", fs)
	}
	if !has(fs, "docs/roadmap.md", "stops at 0.60", "run 0.62", "lacks 0.61, 0.62", "issue-cli create --type docs") {
		t.Errorf("finding = %+v, want 0.61 and 0.62 named", fs[0])
	}

	// A closed issue at a later milestone is no running milestone.
	env = roadmapEnv(t, roadmapTo060, "0.62")
	env.Issues[0].Status = "done"
	if fs := run(t, env, "roadmap-behind"); len(fs) != 0 {
		t.Errorf("done issue at 0.62: findings = %+v, want none", fs)
	}

	// A row in the middle is not named again.
	gap := strings.Replace(roadmapTo060, "| **1.0**", "| **0.62** Channels | x | y | z |\n| **1.0**", 1)
	env = roadmapEnv(t, gap, "0.63")
	if fs := run(t, env, "roadmap-behind"); len(fs) != 1 || !has(fs, "stops at 0.62", "lacks 0.63") || has(fs, "0.61") {
		t.Errorf("findings = %+v, want only 0.63 missing", fs)
	}
}

func TestLintRoadmapCurrentIsQuiet(t *testing.T) {
	current := strings.Replace(roadmapTo060, "| **1.0**", "| **0.61** One source | x | y | z |\n| 0.62 Channels live | x | y | z |\n| **1.0**", 1)
	for name, env := range map[string]*Env{
		"current":     roadmapEnv(t, current, "0.62", "0.61"),
		"ahead":       roadmapEnv(t, current, "0.60"),
		"no roadmap":  roadmapEnv(t, "", "0.62"),
		"no table":    roadmapEnv(t, "# Roadmap\n\nPlans.\n", "0.62"),
		"no versions": roadmapEnv(t, roadmapTo060, "later"),
		"other major": roadmapEnv(t, roadmapTo060, "2.3"),
	} {
		if fs := run(t, env, "roadmap-behind"); len(fs) != 0 {
			t.Errorf("%s: findings = %+v, want none", name, fs)
		}
	}
}
