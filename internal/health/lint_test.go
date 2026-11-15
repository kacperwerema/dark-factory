package health

import "testing"

func TestLintScoreComponent(t *testing.T) {
	var lintComp Component
	for _, c := range DefaultComponents() {
		if c.Name == "lint" {
			lintComp = c
		}
	}
	if lintComp.Name == "" || lintComp.Weight != 10 || lintComp.Unit != UnitCount {
		t.Fatalf("lint component = %+v", lintComp)
	}
	cur := snapOf(map[string]float64{"lint.warnings": 5})
	got := scoreRows(Derive(cur, nil, []Component{lintComp}))
	if got["score.component{component=lint}"] != 75 || got["score.derived"] != 75 {
		t.Errorf("5 warnings (good 0, bad 20): rows = %v, want sub-score 75", got)
	}
}
