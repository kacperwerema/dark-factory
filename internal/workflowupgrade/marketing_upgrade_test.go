package workflowupgrade

import (
	"strings"
	"testing"
)

// A Marketing file from before the foundation (the content workflow,
// restamped marketing as docs/departments.md says) is offered the brand
// foundation in one upgrade pass.
func TestUpgradeProposesTheBrandFoundationToAnOlderMarketingFile(t *testing.T) {
	raw, err := ReadTemplate("content")
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(string(raw), "template: content@v0.58.0", "template: marketing@v0.57.0", 1)
	if old == string(raw) {
		t.Fatal("content template is no longer stamped content@v0.58.0; update this test")
	}
	plan, err := BuildPlan("workflows/marketing.yaml", []byte(old), "", false)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range plan.Proposals {
		ids = append(ids, p.ID)
	}
	got := "," + strings.Join(ids, ",") + ","
	for _, want := range []string{"brand-foundation", "measure-after-publish"} {
		if !strings.Contains(got, ","+want+",") {
			t.Errorf("upgrade should propose %s, got %v", want, ids)
		}
	}
	if strings.Contains(got, ",file-exists-gates,") {
		t.Errorf("file-exists-gates is catalog only (brand-foundation brings it), got %v", ids)
	}
}
