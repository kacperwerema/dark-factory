package validations

import (
	"strings"
	"testing"
)

func TestSectionLinksCountsDistinct(t *testing.T) {
	text := "- [Acme](https://acme.example/about): about.\n" +
		"- https://acme.example/about/ again, with a slash.\n" +
		"- <http://forum.example/t/1>, a thread.\n" +
		"- https://reviews.example/acme.\n" +
		"- `https://code.example/x` in code\n" +
		"- not a link: https:// or www.nolink.example\n"
	got := SectionLinks(text)
	want := []string{"https://acme.example/about", "http://forum.example/t/1", "https://reviews.example/acme", "https://code.example/x"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("links = %q, want %q", got, want)
	}
}

func TestSectionMinLinks(t *testing.T) {
	issue := sampleIssue()
	issue.BodyRaw = "## Research\n\n- https://a.example\n- https://b.example\n\n## Brand\n\n- https://c.example\n"
	a := Action{Rule: "section_min_links", Section: "Research", Min: 2}
	if err := SectionMinLinks(a, issue, Config{}); err != nil {
		t.Fatalf("2 links, min 2: %v", err)
	}
	a.Min = 3
	if err := SectionMinLinks(a, issue, Config{}); err == nil || !strings.Contains(err.Error(), "has 2 distinct http(s) links, need ≥3") {
		t.Fatalf("2 links, min 3: %v", err)
	}
	a.Section = "Ghost"
	if err := SectionMinLinks(a, issue, Config{}); err == nil || !strings.Contains(err.Error(), "is missing") {
		t.Fatalf("missing section: %v", err)
	}
	if err := SectionMinLinks(Action{Rule: "section_min_links", Section: "Research"}, issue, Config{}); err == nil {
		t.Fatal("no min should be an error")
	}
}
