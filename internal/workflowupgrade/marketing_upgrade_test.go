package workflowupgrade

import (
	"regexp"
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
	stamp := regexp.MustCompile(`(?m)^template: content@v\S+$`)
	if !stamp.Match(raw) {
		t.Fatal("content template has no template: content@<version> stamp; update this test")
	}
	old := stamp.ReplaceAllString(string(raw), "template: marketing@v0.57.0")
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

// A Marketing file from before the SEO baseline (the bundled one with the
// type cut out and no features record for it) is offered seo-baseline.
func TestUpgradeProposesTheSeoBaselineToAnOlderMarketingFile(t *testing.T) {
	raw, err := ReadTemplate("marketing")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	start := strings.Index(s, "\n  # The SEO baseline (system SEO)")
	end := strings.Index(s, "\n  # More channels:")
	if start < 0 || end < start {
		t.Fatal("marketing template's seo-baseline block not found; update this test")
	}
	old := s[:start] + s[end:]
	old = strings.Replace(old, "    - seo-baseline@", "    - was-seo-baseline@", 1)
	if strings.Contains(old, "SEO baseline for one site") {
		t.Fatal("the cut left the seo-baseline type in")
	}
	plan, err := BuildPlan("workflows/marketing.yaml", []byte(old), "", false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range plan.Proposals {
		found = found || p.ID == "seo-baseline"
	}
	if !found {
		t.Errorf("upgrade should propose seo-baseline to a Marketing without it, got %v", plan.Proposals)
	}

	// And not to the bundled file, which has it.
	plan, err = BuildPlan("workflows/marketing.yaml", raw, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plan.Proposals {
		if p.ID == "seo-baseline" {
			t.Error("the bundled marketing template already has seo-baseline: upgrade must not propose it")
		}
	}
}
