package outputs

import (
	"bytes"
	"html"
	"html/template"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// Doc is a markdown output rendered for the reader.
type Doc struct {
	// Title is a leading "# " heading, taken out of HTML ("" without one).
	Title string
	HTML  string
	// Toc is the h2–h4 headings with their ids, in order.
	Toc []Heading
	// Text is the document as the page shows it, as plain text with white
	// space folded; Headings each heading id's offset in it.
	Text     string
	Headings map[string]int
}

// Heading is one heading of a Doc.
type Heading struct {
	Level    int
	ID, Text string
}

// markdown renders outputs: tables, task lists, strikethrough, heading ids;
// raw HTML is dropped and dangerous links filtered (goldmark's default),
// since the reader runs on the viewer's origin.
func markdown() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(extension.Table, extension.TaskList, extension.Strikethrough),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
}

var htmlTag = regexp.MustCompile(`<[^>]*>`)

// Render renders a markdown output.
func Render(src []byte) Doc {
	md := markdown()
	root := md.Parser().Parse(text.NewReader(src))
	var d Doc
	if first, ok := root.FirstChild().(*ast.Heading); ok && first.Level == 1 {
		d.Title = strings.TrimSpace(string(first.Text(src))) //nolint:staticcheck // the heading's plain text
		root.RemoveChild(root, first)
	}
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		h, ok := n.(*ast.Heading)
		if !entering || !ok || h.Level < 2 || h.Level > 4 {
			return ast.WalkContinue, nil
		}
		id, _ := h.AttributeString("id")
		b, _ := id.([]byte)
		d.Toc = append(d.Toc, Heading{Level: h.Level, ID: string(b), Text: strings.TrimSpace(string(h.Text(src)))}) //nolint:staticcheck
		return ast.WalkSkipChildren, nil
	})
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, root); err != nil {
		buf.Reset()
		buf.WriteString("<pre>" + template.HTMLEscapeString(string(src)) + "</pre>")
	}
	d.HTML = buf.String()
	d.Text = FoldSpace(html.UnescapeString(htmlTag.ReplaceAllString(d.HTML, "")))
	d.Headings = map[string]int{}
	from := 0
	for _, h := range d.Toc {
		at := strings.Index(d.Text[from:], FoldSpace(h.Text))
		if at < 0 {
			d.Headings[h.ID] = from
			continue
		}
		d.Headings[h.ID] = from + at
		from += at
	}
	return d
}

// HeadingPath is "Brand › Proposal": the heading id's parents and itself.
func HeadingPath(toc []Heading, id string) string {
	for i, h := range toc {
		if h.ID != id {
			continue
		}
		path := []string{h.Text}
		level := h.Level
		for j := i - 1; j >= 0 && level > 2; j-- {
			if toc[j].Level < level {
				path = append([]string{toc[j].Text}, path...)
				level = toc[j].Level
			}
		}
		return strings.Join(path, " › ")
	}
	return ""
}
