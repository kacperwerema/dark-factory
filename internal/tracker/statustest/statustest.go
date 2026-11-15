// Package statustest builds a workflow whose role statuses have new names,
// for the tests that show a behaviour follows the role and not the name.
package statustest

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Name is the name a role's status gets in Renamed.
func Name(role string) string { return "renamed-" + role }

// Renamed is the bundled development workflow with each role status renamed
// and declaring its role, everywhere the file names it.
func Renamed() *tracker.WorkflowConfig {
	wf := tracker.DefaultWorkflow()
	names := map[string]string{}
	for _, role := range tracker.StatusRoles() {
		names[wf.RoleStatus(role)] = Name(role)
	}
	rename := func(s *string) {
		if n, ok := names[*s]; ok {
			*s = n
		}
	}
	for i := range wf.Statuses {
		st := &wf.Statuses[i]
		if role := wf.RoleOf(st.Name); role != "" {
			st.Role = role
		}
	}
	for i := range wf.Statuses {
		rename(&wf.Statuses[i].Name)
	}
	transitions := func(ts []tracker.WorkflowTransition) {
		for i := range ts {
			rename(&ts[i].From)
			rename(&ts[i].To)
			for j := range ts[i].Actions {
				rename(&ts[i].Actions[j].Status)
			}
		}
	}
	transitions(wf.Transitions)
	for name, typ := range wf.Types {
		for i := range typ.Path {
			rename(&typ.Path[i])
		}
		for i := range typ.Statuses {
			rename(&typ.Statuses[i].Name)
		}
		transitions(typ.Transitions)
		wf.Types[name] = typ
	}
	for name, sys := range wf.Systems {
		for i := range sys.Statuses {
			rename(&sys.Statuses[i].Name)
		}
		transitions(sys.Transitions)
		wf.Systems[name] = sys
	}
	for i := range wf.Board.Columns {
		rename(&wf.Board.Columns[i])
	}
	return wf
}

// Write saves Renamed to path, with text appended to the renamed verify
// status's prompt when prompt is set.
func Write(t *testing.T, path, prompt string) {
	t.Helper()
	wf := Renamed()
	if prompt != "" {
		wf.GetStatus(Name(tracker.StatusRoleVerify)).Prompt = prompt
	}
	raw, err := yaml.Marshal(wf)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "human-testing:") {
		t.Fatal("the workflow still names the old status")
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
