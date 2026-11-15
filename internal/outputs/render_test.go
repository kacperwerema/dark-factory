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

// An output that starts with YAML frontmatter (an SEO article's title:,
// faq:) shows it as a metadata table, not as an <hr>, a paragraph and a list
// above the article; the first "# " after it is still the title.
func TestRenderFrontmatter(t *testing.T) {
	src := "---\ntitle: \"Jadłospis na tydzień\"\nslug: jadlospis\nfaq:\n  - q: Ile czasu?\n    a: Godzina.\n  - q: <b>Tanio?</b>\n    a: Tak.\n---\n\n# Jadłospis\n\nLead.\n\n## One\n"
	d := Render([]byte(src))
	if d.Title != "Jadłospis" {
		t.Errorf("title = %q", d.Title)
	}
	for _, bad := range []string{"<hr", "title:", "faq:", "<b>Tanio"} {
		if strings.Contains(d.HTML, bad) {
			t.Errorf("rendered %q: %s", bad, d.HTML)
		}
	}
	for _, want := range []string{`<table class="wb-odoc-fm"`, "<th>title</th>", "Jadłospis na tydzień", "<th>faq</th>", "Ile czasu?", "&lt;b&gt;Tanio?&lt;/b&gt;"} {
		if !strings.Contains(d.HTML, want) {
			t.Errorf("no %q in %s", want, d.HTML)
		}
	}
	if len(d.Toc) != 1 || d.Toc[0].ID != "one" {
		t.Errorf("toc = %v", d.Toc)
	}
	if at, ok := d.Headings["one"]; !ok || !strings.HasPrefix(d.Text[at:], "One") {
		t.Errorf("text %q headings %v", d.Text, d.Headings)
	}

	// No closing fence, or YAML that does not parse: the block is still kept
	// out of the article, shown as it is.
	d = Render([]byte("---\ntitle: [unclosed\n---\n\n# Doc\n\nBody.\n"))
	if d.Title != "Doc" || !strings.Contains(d.HTML, `<pre class="wb-odoc-fm">title: [unclosed</pre>`) {
		t.Errorf("bad yaml: title %q html %s", d.Title, d.HTML)
	}
	d = Render([]byte("---\n\nA rule above a paragraph.\n"))
	if !strings.Contains(d.HTML, "<hr") || strings.Contains(d.HTML, "wb-odoc-fm") {
		t.Errorf("a plain rule became frontmatter: %s", d.HTML)
	}
	// No "# " heading: the frontmatter's title: is the title.
	d = Render([]byte("---\ntitle: From meta\n---\nBody.\n"))
	if d.Title != "From meta" {
		t.Errorf("title from meta = %q", d.Title)
	}
}
