package lint

import (
	"strings"
	"testing"
	"time"
)

func TestRolesLintNamesARename(t *testing.T) {
	raw := `statuses:
  - name: "idea"
  - name: "in design"
  - name: "ready"
  - name: "in progress"
  - name: "testing"
  - name: "human-testing"
  - name: "done"
`
	got, err := File("/nowhere/workflow.yaml", []byte(raw), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var roles []Finding
	for _, f := range got {
		if f.Check == "roles" {
			roles = append(roles, f)
		}
	}
	if len(roles) != 1 || !strings.Contains(roles[0].Message, "no status has role queue") || !strings.Contains(roles[0].Fix, "role: queue") {
		t.Fatalf("roles findings = %+v", roles)
	}
}

func TestRolesLintLeavesDepartmentFilesAlone(t *testing.T) {
	raw := "statuses:\n  - name: \"brief\"\n  - name: \"draft\"\n  - name: \"owner review\"\n  - name: \"publish\"\n  - name: \"done\"\n"
	got, err := File("/nowhere/workflow.yaml", []byte(raw), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range got {
		if f.Check == "roles" {
			t.Fatalf("a department's file: %+v", f)
		}
	}
}
