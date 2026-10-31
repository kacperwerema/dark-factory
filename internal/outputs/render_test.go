package outputs

import (
	"strings"
	"testing"
)

func TestRenderTitleContentsAndNoScript(t *testing.T) {
	d := Render([]byte("# Brand\n\nLead.\n\n## One\n\n### Two\n\n#### Three\n\n##### Five\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n<script>alert(1)</script>\n\n[x](javascript:alert(1)) <img src=x onerror=alert(1)>\n"))
	if d.Title != "Brand" || strings.Contains(d.HTML, "<h1") {
		t.Errorf("title %q, html %s", d.Title, d.HTML)
	}
	var got []string
	for _, h := range d.Toc {
		got = append(got, strings.Repeat("#", h.Level)+h.ID)
	}
	if strings.Join(got, " ") != "##one ###two ####three" {
		t.Errorf("toc = %v", got)
	}
	if !strings.Contains(d.HTML, "<table>") {
		t.Error("no table")
	}
	for _, bad := range []string{"<script", "javascript:", "onerror"} {
		if strings.Contains(d.HTML, bad) {
			t.Errorf("rendered %q: %s", bad, d.HTML)
		}
	}
	if !strings.HasPrefix(d.Text, "Lead. One Two Three") || d.Headings["two"] <= d.Headings["one"] {
		t.Errorf("text %q headings %v", d.Text, d.Headings)
	}
	if HeadingPath(d.Toc, "three") != "One › Two › Three" {
		t.Errorf("path = %q", HeadingPath(d.Toc, "three"))
	}
}
