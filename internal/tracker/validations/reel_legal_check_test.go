package validations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bundledReelLegal is the rules file department add video installs.
const bundledReelLegal = "../../../cmd/issue-cli/templates/departments/video/reel-legal.yaml"

// reelProject is a project root with the bundled rules file at
// departments/video/reel-legal.yaml and a signed consent file.
func reelProject(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	rules, err := os.ReadFile(bundledReelLegal)
	if err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("departments/video/reel-legal.yaml", string(rules))
	write("consent/host.pdf", "signed")
	return Config{ProjectRoot: root, IssuesRoot: filepath.Join(root, "issues")}
}

// cleanDecl is a reel with no face, no music and no AI content: nothing
// for the declarations to hold.
const cleanDecl = `- music: none
- ai_content: no
- face: none
`

func reelIssue(script, decl string) *IssueView {
	return &IssueView{Slug: "video/reel-1", Department: "video",
		BodyRaw: "## Brief\nA reel.\n\n## Script\n" + script + "\n\n## Legal check\n" + decl + "\n"}
}

func reelCheck(t *testing.T, cfg Config, script, decl string) error {
	t.Helper()
	return ReelLegalCheck(Action{Rule: "reel_legal_check", Section: "Script", Path: DefaultReelLegalRules}, reelIssue(script, decl), cfg)
}

func wantHold(t *testing.T, err error, rule string) {
	t.Helper()
	if err == nil {
		t.Fatalf("passed; want a %s hold", rule)
	}
	if !strings.Contains(err.Error(), "\n  - "+rule+": ") {
		t.Fatalf("hold names no %s finding:\n%v", rule, err)
	}
	if !strings.Contains(err.Error(), "issue-cli reel-legal video/reel-1") {
		t.Errorf("hold doesn't name issue-cli reel-legal:\n%v", err)
	}
}

// TestReelLegalCheckPassesACleanReel: a script with no claim, no mark and
// no testimonial, with every declaration made, passes.
func TestReelLegalCheckPassesACleanReel(t *testing.T) {
	cfg := reelProject(t)
	if err := reelCheck(t, cfg, "Plan your week's dinners in two minutes. Download the app.", cleanDecl); err != nil {
		t.Fatalf("clean reel held: %v", err)
	}
}

// TestReelLegalCheckHolds: each rule of the legal floor holds a reel (AC1,
// AC3), with the bundled phrase lists.
func TestReelLegalCheckHolds(t *testing.T) {
	cfg := reelProject(t)
	avatar := "- music: none\n- ai_content: yes\n- ai_label: yes\n- face: avatar\n- persona: presenter\n- consent: consent/host.pdf\n"
	for _, c := range []struct {
		name, rule, script, decl string
	}{
		{"health claim, Polish", "claims", "Schudnij 5 kilo z naszymi przepisami.", cleanDecl},
		{"health claim, English", "claims", "Healthy dinners every day.", cleanDecl},
		{"nutrition claim with diacritics", "claims", "Wysokobiałkowe śniadania.", cleanDecl},
		{"no music line", "music", "Plan your dinners.", "- ai_content: no\n- face: none\n"},
		{"track with no licence", "music", "Plan your dinners.", "- music: Summer Vibes\n- music_licence: unknown\n- ai_content: no\n- face: none\n"},
		{"no ai_content line", "ai-label", "Plan your dinners.", "- music: none\n- face: none\n"},
		{"AI footage with no label", "ai-label", "Plan your dinners.", "- music: none\n- ai_content: yes\n- face: none\n"},
		{"avatar with no label", "ai-label", "Plan your dinners.", "- music: none\n- ai_content: no\n- face: avatar\n- persona: presenter\n- consent: consent/host.pdf\n"},
		{"avatar gives a testimonial", "testimonial", "Schudłam 3 kilo w miesiąc dzięki tej aplikacji.", avatar},
		{"testimonial in English", "testimonial", "I lost 10 pounds in a month.", avatar},
		{"avatar presents as a customer", "persona", "Plan your dinners.", strings.Replace(avatar, "persona: presenter", "persona: a happy customer", 1)},
		{"avatar with no persona", "persona", "Plan your dinners.", strings.Replace(avatar, "- persona: presenter\n", "", 1)},
		{"face with no consent line", "consent", "Plan your dinners.", strings.Replace(avatar, "- consent: consent/host.pdf\n", "", 1)},
		{"consent file missing", "consent", "Plan your dinners.", strings.Replace(avatar, "consent/host.pdf", "consent/nobody.pdf", 1)},
		{"consent path out of the project", "consent", "Plan your dinners.", strings.Replace(avatar, "consent/host.pdf", "../outside.pdf", 1)},
		{"implied endorsement", "trademark", "Oficjalne przepisy na Thermomix.", cleanDecl},
		{"implied endorsement, English", "trademark", "Recommended by Vorwerk.", cleanDecl},
		{"mark with no disclaimer", "trademark", "Recipes for your TM6.", cleanDecl},
		{"no script", "script", "", cleanDecl},
	} {
		t.Run(c.name, func(t *testing.T) {
			wantHold(t, reelCheck(t, cfg, c.script, c.decl), c.rule)
		})
	}
}

// TestReelLegalCheckClears: the declarations that clear a hold do (AC3:
// the same reel with the claim authorised, the licence and the label).
func TestReelLegalCheckClears(t *testing.T) {
	cfg := reelProject(t)
	script := "High protein breakfasts. Recipes for your TM6."
	held := "- music: Summer Vibes\n- ai_content: yes\n- face: none\n"
	err := reelCheck(t, cfg, script, held)
	for _, rule := range []string{"claims", "music", "ai-label", "trademark"} {
		wantHold(t, err, rule)
	}
	cleared := held + "- music_licence: Epidemic Sound, commercial social licence\n- ai_label: yes\n" +
		"- claim: high protein = Annex to 1924/2006: at least 20% of energy from protein\n" +
		"- disclaimer: Not affiliated with Vorwerk. Thermomix is Vorwerk's trademark.\n"
	if err := reelCheck(t, cfg, script, cleared); err != nil {
		t.Fatalf("cleared reel still held: %v", err)
	}
	// A placeholder isn't a declaration: the appended section as it comes.
	if err := reelCheck(t, cfg, "Plan your dinners.", "- music: <none, or the track>\n- ai_content: <yes or no>\n- face: <none, avatar or real>\n"); err == nil {
		t.Fatal("placeholders passed as declarations")
	}
}

// TestReelLegalCheckReadsTheStoryboard: a claim only in the storyboard's
// captions holds the reel once ## Legal check names the storyboard; file
// names in it don't read as claims.
func TestReelLegalCheckReadsTheStoryboard(t *testing.T) {
	cfg := reelProject(t)
	sb := `{"scenes":[{"narration":{"text":"Plan your dinners."},"clip":{"src":"clips/healthy-bowl.mp4"},"captions":[{"text":"Spal tłuszcz w tydzień"}]}],"cta":{"text":"Download"}}`
	if err := os.WriteFile(filepath.Join(cfg.ProjectRoot, "reel.json"), []byte(sb), 0o644); err != nil {
		t.Fatal(err)
	}
	wantHold(t, reelCheck(t, cfg, "Plan your dinners.", cleanDecl+"- storyboard: reel.json\n"), "claims")
	if err := os.WriteFile(filepath.Join(cfg.ProjectRoot, "reel.json"), []byte(strings.Replace(sb, "Spal tłuszcz w tydzień", "Dinner in 20 minutes", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := reelCheck(t, cfg, "Plan your dinners.", cleanDecl+"- storyboard: reel.json\n"); err != nil {
		t.Fatalf("a file name in the storyboard read as a claim: %v", err)
	}
}

// TestReelLegalCheckRulesFile: the rules file is the project's own (an
// edited copy changes what holds), and a missing one holds (AC5).
func TestReelLegalCheckRulesFile(t *testing.T) {
	cfg := reelProject(t)
	rules := filepath.Join(cfg.ProjectRoot, "departments", "video", "reel-legal.yaml")
	if err := reelCheck(t, cfg, "Taste the crunch.", cleanDecl); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rules, []byte("claims: [crunch]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantHold(t, reelCheck(t, cfg, "Taste the crunch.", cleanDecl), "claims")
	if err := os.Remove(rules); err != nil {
		t.Fatal(err)
	}
	err := reelCheck(t, cfg, "Taste the crunch.", cleanDecl)
	if err == nil || !strings.Contains(err.Error(), "can't read the rules file departments/video/reel-legal.yaml") ||
		!strings.Contains(err.Error(), "issue-cli department update video") {
		t.Fatalf("missing rules file: %v", err)
	}
	if err := os.WriteFile(rules, []byte("# emptied\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := reelCheck(t, cfg, "Taste the crunch.", cleanDecl); err == nil {
		t.Fatal("an empty rules file passed")
	}
}
