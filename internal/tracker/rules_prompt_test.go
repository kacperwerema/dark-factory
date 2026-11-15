package tracker

import (
	"testing"
)

func TestDepartmentFileTakesProductsRules(t *testing.T) {
	product := &WorkflowConfig{Rules: []ProjectRule{{Text: "from product"}}}
	own := &WorkflowConfig{Rules: []ProjectRule{{Text: "from the department file"}}}
	if got := departmentWorkflow(own, product).Rules; len(got) != 1 || got[0].Text != "from product" {
		t.Errorf("rules: is project-wide: %+v", got)
	}
}
