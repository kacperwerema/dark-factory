package tracker

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPlaytestForOwner(t *testing.T) {
	cases := map[string]bool{"": true, "owner": true, "agents": false, "off": false, " Agents ": false}
	for v, want := range cases {
		var wf WorkflowConfig
		if err := yaml.Unmarshal([]byte("playtest: \""+v+"\"\nsystems:\n  UI: {}\n"), &wf); err != nil {
			t.Fatal(err)
		}
		if got := wf.PlaytestForOwner(); got != want {
			t.Errorf("playtest: %q = %v, want %v", v, got, want)
		}
		// The scoped config an issue gets keeps the switch.
		if got := wf.ForIssue(&Issue{System: "UI"}).PlaytestForOwner(); got != want {
			t.Errorf("playtest: %q via ForIssue = %v, want %v", v, got, want)
		}
	}
	if !(*WorkflowConfig)(nil).PlaytestForOwner() {
		t.Error("no workflow: the owner runs playtests (today's behaviour)")
	}
}
