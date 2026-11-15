package validations

import (
	"fmt"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"regexp"
	"strings"
)

func init() { register("section_min_links", SectionMinLinks) }

// linkRe finds an http(s) link in markdown text: it stops at whitespace and
// at the characters that close a markdown link or an angle-bracket link.
var linkRe = regexp.MustCompile(`https?://[^\s<>()\[\]"'` + "`" + `]+`)

// SectionLinks returns the distinct http(s) links in text, in order. A
// trailing period, comma, colon or semicolon is the sentence's, not the
// link's; a trailing slash doesn't make a link new.
func SectionLinks(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range linkRe.FindAllString(text, -1) {
		m = strings.TrimRight(m, ".,;:!?")
		key := strings.ToLower(strings.TrimRight(m, "/"))
		if key == "http:" || key == "https:" || strings.HasSuffix(key, "://") || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, m)
	}
	return out
}

// SectionMinLinks passes when the named section holds at least Min distinct
// http(s) links: research that lists its sources, measured by the sources
// rather than by length.
func SectionMinLinks(action Action, issue *IssueView, _ Config) error {
	section := strings.TrimSpace(action.Section)
	if section == "" {
		return fmt.Errorf("section_min_links requires 'section'")
	}
	if action.Min <= 0 {
		return fmt.Errorf("section_min_links requires positive 'min'")
	}
	content, ok := findSectionContent(issue.BodyRaw, section)
	if !ok {
		return fail(action, issue,
			fmt.Sprintf("section \"## %s\" is missing (need ≥%d distinct links)", section, action.Min),
			fmt.Sprintf("add the sources, one link each: issue-cli append %s --section %s --body \"- <link>: what it says\"", issue.Slug, shell.Quote(section)))
	}
	if got := len(SectionLinks(content)); got < action.Min {
		return fail(action, issue,
			fmt.Sprintf("section \"## %s\" has %d distinct http(s) links, need ≥%d", section, got, action.Min),
			fmt.Sprintf("add more sources, one link each: issue-cli append %s --section %s --body \"- <link>: what it says\"", issue.Slug, shell.Quote(section)))
	}
	return nil
}
