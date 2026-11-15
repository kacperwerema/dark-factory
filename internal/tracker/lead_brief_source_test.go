package tracker

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The duties sentence is written once. A copy anywhere else in the code, the
// bundled templates or a workflow file fails here: it would drift.
func TestLeadDutiesHaveOneSource(t *testing.T) {
	const phrase = "The human decides. You groom"
	var copies []string
	for _, root := range []string{"../../internal", "../../cmd", "../../workflows", "../../workflow.yaml"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if d.IsDir() || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "CHANGELOG.md") {
				return nil
			}
			switch filepath.Ext(path) {
			case ".go", ".yaml", ".yml", ".md":
			default:
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for range strings.Count(string(raw), phrase) {
				copies = append(copies, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(copies) != 1 || filepath.Base(copies[0]) != "lead_brief.go" {
		t.Errorf("%q is written in %v, want only lead_brief.go's LeadDuties", phrase, copies)
	}
}
