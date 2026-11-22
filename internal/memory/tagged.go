package memory

import (
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// PathTag ties a folder of the repository to a memory tag: an issue that
// changes a file under Prefix gets the memories tagged Tag in its
// in-progress briefing, whatever its title says.
type PathTag struct {
	Prefix, Tag string
}

// PathTags are the folders a briefing knows a tag for. The bundled
// templates carry rules a test only names at make validate (the earlier
// hash of a department template's file, the shared snippets), so a change
// there gets them before the first edit.
var PathTags = []PathTag{
	{Prefix: "cmd/issue-cli/templates/", Tag: "template"},
}

// TagsForFiles is the PathTags tags of files (slash paths from the
// repository root), each once, in PathTags order.
func TagsForFiles(files []string) []string {
	var out []string
	for _, pt := range PathTags {
		for _, f := range files {
			if strings.HasPrefix(strings.TrimPrefix(f, "./"), pt.Prefix) {
				out = appendUnique(out, pt.Tag)
				break
			}
		}
	}
	return out
}

// HasTag reports whether m carries tag (case aside).
func (m Memory) HasTag(tag string) bool {
	for _, t := range m.Tags {
		if strings.EqualFold(strings.TrimSpace(t), tag) {
			return true
		}
	}
	return false
}

// ForTags is the valid memories in mems carrying one of tags, strongest
// first, within limit and chars; skip holds ids the briefing already shows
// (pins among them), which are left out. More counts those that did not
// fit.
func ForTags(mems []Memory, tags []string, skip map[string]bool, now time.Time, limit, chars int) Pins {
	var p Pins
	if len(tags) == 0 {
		return p
	}
	var hits []Memory
	for _, m := range mems {
		if skip[m.ID] || m.Pinned || !m.Valid(now) {
			continue
		}
		for _, t := range tags {
			if m.HasTag(t) {
				hits = append(hits, m)
				break
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return Strength(hits[i], now) > Strength(hits[j], now) })
	used := 0
	for _, m := range hits {
		n := utf8.RuneCountInString(m.Text)
		if len(p.Shown) >= limit || used+n > chars {
			p.More++
			continue
		}
		used += n
		p.Shown = append(p.Shown, m)
	}
	return p
}
