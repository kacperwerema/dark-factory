package outputs

import (
	"regexp"
	"strings"
)

var knownHead = regexp.MustCompile(`(?i)^(?:§?\d+[.)]?\s+)?(?:known|corrections?)\b`)

// KnownItems are the top-level list items under a report's Known or
// Corrections heading (any level, a number in front or words after it:
// "## 4. Known (checked)"): the durable facts a report-only run found,
// read by issue-cli memory candidates. An item that only says none is
// left out, and so is a heading with prose and no list.
func KnownItems(markdown string) []string {
	var items []string
	level, fenced := 0, false
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if m := headingLine.FindStringSubmatch(line); m != nil {
			if knownHead.MatchString(m[2]) {
				level = len(m[1])
			} else if len(m[1]) <= level {
				level = 0
			}
			continue
		}
		if level == 0 {
			continue
		}
		if m := listItemLine.FindStringSubmatch(line); m != nil && !saysNone(m[1]) {
			items = append(items, strings.TrimSpace(m[1]))
		}
	}
	return items
}
