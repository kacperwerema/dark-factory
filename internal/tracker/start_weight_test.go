package tracker

import (
	"os"
	"path/filepath"
	"testing"
)

// start_weight: is read from projects.yaml; unset, zero or negative is 1.
func TestStartWeight(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "projects.yaml")
	os.MkdirAll(filepath.Join(dir, "issues"), 0o755)
	os.WriteFile(cfg, []byte("projects:\n  - name: Atlas\n    issues: ./issues\n    start_weight: 3\n  - name: DF\n    issues: ./issues\n  - name: Neg\n    issues: ./issues\n    start_weight: -2\n"), 0o644)
	ps, err := LoadProjects(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{3, 1, 1} {
		if got := ps[i].Weight(); got != want {
			t.Errorf("%s weight = %d, want %d", ps[i].Name, got, want)
		}
	}
	var nilProj *Project
	if nilProj.Weight() != 1 {
		t.Error("nil project weight")
	}
}
