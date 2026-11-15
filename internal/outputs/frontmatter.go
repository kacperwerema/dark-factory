package outputs

import (
	"bytes"
	"html/template"
	"strings"

	"gopkg.in/yaml.v3"
)

// splitFrontmatter takes a leading YAML frontmatter block ("---" on the first
// line, up to the next "---" or "..." line) off src. ok is false when src
// does not start with one: a lone "---" with no closing fence is a rule.
func splitFrontmatter(src []byte) (meta, body []byte, ok bool) {
	first, rest, found := bytes.Cut(src, []byte("\n"))
	if !found || strings.TrimRight(string(first), "\r") != "---" {
		return nil, src, false
	}
	for at := 0; at < len(rest); {
		end := bytes.IndexByte(rest[at:], '\n')
		line := rest[at:]
		next := len(rest)
		if end >= 0 {
			line = rest[at : at+end]
			next = at + end + 1
		}
		if l := strings.TrimRight(string(line), " \t\r"); l == "---" || l == "..." {
			return rest[:at], rest[next:], true
		}
		at = next
	}
	return nil, src, false
}

// maxMetaDepth caps how deep nested lists and maps render.
const maxMetaDepth = 6

// renderFrontmatter renders the frontmatter as a table of its keys, in file
// order; YAML that does not parse to a map shows as it is. title is the
// title: scalar ("" without one).
func renderFrontmatter(meta []byte) (out, title string) {
	var doc yaml.Node
	if err := yaml.Unmarshal(meta, &doc); err != nil || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		if strings.TrimSpace(string(meta)) == "" {
			return "", ""
		}
		return `<pre class="wb-odoc-fm">` + template.HTMLEscapeString(strings.TrimRight(string(meta), "\r\n")) + "</pre>\n", ""
	}
	m := doc.Content[0]
	var b strings.Builder
	b.WriteString(`<table class="wb-odoc-fm">` + "\n<tbody>\n")
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		if k.Value == "title" && v.Kind == yaml.ScalarNode {
			title = strings.TrimSpace(v.Value)
		}
		b.WriteString("<tr>\n<th>" + template.HTMLEscapeString(k.Value) + "</th>\n<td>")
		writeMetaValue(&b, v, 0)
		b.WriteString("</td>\n</tr>\n")
	}
	b.WriteString("</tbody>\n</table>\n")
	return b.String(), title
}

func writeMetaValue(b *strings.Builder, n *yaml.Node, depth int) {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	if depth >= maxMetaDepth {
		b.WriteString("…")
		return
	}
	switch n.Kind {
	case yaml.SequenceNode:
		b.WriteString("\n<ul>\n")
		for _, c := range n.Content {
			b.WriteString("<li>")
			writeMetaValue(b, c, depth+1)
			b.WriteString("</li>\n")
		}
		b.WriteString("</ul>\n")
	case yaml.MappingNode:
		b.WriteString("\n<dl>\n")
		for i := 0; i+1 < len(n.Content); i += 2 {
			b.WriteString("<dt>" + template.HTMLEscapeString(n.Content[i].Value) + "</dt>\n<dd>")
			writeMetaValue(b, n.Content[i+1], depth+1)
			b.WriteString("</dd>\n")
		}
		b.WriteString("</dl>\n")
	default:
		b.WriteString(template.HTMLEscapeString(n.Value))
	}
}
