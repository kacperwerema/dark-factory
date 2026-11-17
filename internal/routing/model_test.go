package routing

import (
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestModelForRuleMatch(t *testing.T) {
	yes := true
	cfg := &tracker.RoutingConfig{Rules: []tracker.RoutingRule{
		{Lead: &yes, Agent: "claude", Model: "claude-opus-5-5"},
		{Type: "tweak", Agent: "claude", Model: "claude-haiku-5-5"},
		{Type: "report", Agent: "claude", Model: tracker.ModelDefault},
		{Type: "docs", Agent: "claude"},
		{Agent: "claude", Model: "claude-catch-all"},
	}}
	cases := []struct {
		name  string
		s     Subject
		agent string
		want  string
	}{
		{"rule names a model", Subject{Type: "tweak"}, "claude", "claude-haiku-5-5"},
		{"model: default switches the default off", Subject{Type: "report"}, "claude", ""},
		{"first match without model keeps the built-in default", Subject{Type: "docs"}, "claude", "claude-sonnet-5-5"},
		{"catch-all", Subject{Type: "feature"}, "claude", "claude-catch-all"},
		{"lead rule", Subject{Lead: true}, "claude", "claude-opus-5-5"},
		{"another agent's rule doesn't reach codex", Subject{Type: "tweak"}, "codex", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, _ := ModelFor(cfg, c.s, c.agent); got != c.want {
				t.Errorf("ModelFor(%+v, %s) = %q, want %q", c.s, c.agent, got, c.want)
			}
		})
	}
}

func TestModelForDefaultsWithNoRouting(t *testing.T) {
	for typ, want := range map[string]string{"docs": "claude-sonnet-5-5", "report": "claude-sonnet-5-5", "feature": "", "bugfix": "", "tweak": "", "": ""} {
		if got, _ := ModelFor(nil, Subject{Type: typ}, "claude"); got != want {
			t.Errorf("type %q on claude: %q, want %q", typ, got, want)
		}
		if got, _ := ModelFor(nil, Subject{Type: typ}, "codex"); got != "" {
			t.Errorf("type %q on codex: %q, want no model", typ, got)
		}
	}
	if got, _ := ModelFor(nil, Subject{Type: "docs", Lead: true}, "claude"); got != "" {
		t.Errorf("a lead got the docs default: %q", got)
	}
}

// A docs issue routed to codex that falls back to claude gets claude's
// docs default; one routed to claude that falls back to codex gets no
// Claude model.
func TestModelFollowsTheFallbackAgent(t *testing.T) {
	old := Installed
	Installed = func(string) bool { return true }
	defer func() { Installed = old }()
	cfg := &tracker.RoutingConfig{Fallback: []string{"claude", "codex"}, Rules: []tracker.RoutingRule{
		{Type: "docs", Agent: "claude", Model: "claude-sonnet-5-5"},
	}}
	st := &State{Rests: map[string]Rest{"claude": {Agent: "claude", Until: now.Add(time.Hour), KnownReset: true}}}
	c := Pick(cfg, Subject{Type: "docs"}, "", st, now)
	if c.Agent != "codex" || c.Model != "" {
		t.Fatalf("fallback: agent %s model %q, want codex with no model", c.Agent, c.Model)
	}
	c = Pick(cfg, Subject{Type: "docs"}, "", &State{}, now)
	if c.Agent != "claude" || c.Model != "claude-sonnet-5-5" || c.ModelWhy != "model for type docs" {
		t.Fatalf("routed: %+v", c)
	}
	// The owner's --agent picks the CLI, not the model.
	c = Pick(cfg, Subject{Type: "docs"}, "claude", &State{}, now)
	if c.Model != "claude-sonnet-5-5" {
		t.Fatalf("--agent claude on docs: model %q", c.Model)
	}
}
