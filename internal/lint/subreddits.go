package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// subredditsFile is the owner's list of subreddits a department's social
// channel may read and post in, under departments/<key>/ (the marketing
// template's starter, docs/departments.md "Social: Reddit").
const subredditsFile = "subreddits.md"

// subredditNameRe is a row's Subreddit cell: r/ and Reddit's name shape.
var subredditNameRe = regexp.MustCompile(`^r/[A-Za-z0-9][A-Za-z0-9_]{1,20}$`)

// subredditRow is one row of a subreddits.md table.
type subredditRow struct {
	Line int
	Name string
	Rule string
}

// parseSubreddits reads the table rows of a subreddits.md: the header row
// names the columns (Subreddit, Self-promotion rule, matched without
// case), the separator row is skipped, and so is anything inside an HTML
// comment (the template's example row). ok is false when rows exist but no
// header names both columns.
func parseSubreddits(text string) (rows []subredditRow, ok bool) {
	nameCol, ruleCol := -1, -1
	inComment, sawRow := false, false
	for i, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if inComment {
			if strings.Contains(t, "-->") {
				inComment = false
			}
			continue
		}
		if strings.HasPrefix(t, "<!--") {
			inComment = !strings.Contains(t, "-->")
			continue
		}
		if !strings.HasPrefix(t, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(t, "|"), "|")
		for k := range cells {
			cells[k] = strings.TrimSpace(cells[k])
		}
		if strings.Trim(strings.Join(cells, ""), "-: ") == "" {
			continue // the separator row
		}
		if nameCol < 0 {
			for k, c := range cells {
				switch strings.ToLower(c) {
				case "subreddit":
					nameCol = k
				case "self-promotion rule":
					ruleCol = k
				}
			}
			if nameCol >= 0 && ruleCol >= 0 {
				continue
			}
			nameCol, ruleCol = -1, -1
			sawRow = true
			continue
		}
		r := subredditRow{Line: i + 1}
		if nameCol < len(cells) {
			r.Name = cells[nameCol]
		}
		if ruleCol < len(cells) {
			r.Rule = cells[ruleCol]
		}
		rows = append(rows, r)
	}
	return rows, nameCol >= 0 || !sawRow
}

// ruleMissing: an empty rule cell, a dash, or the template's <placeholder>.
func ruleMissing(rule string) bool {
	switch strings.TrimSpace(rule) {
	case "", "-", "—", "–", "?", "n/a", "N/A":
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(rule), "<")
}

// subredditFindings are department d's subreddits.md mistakes: a row whose
// self-promotion rule is missing, a name not shaped r/<name>, one listed
// twice, a table without its header. No file is no finding: Reddit is
// optional.
func subredditFindings(p *tracker.Project, d tracker.Department) []Finding {
	rel := "departments/" + tracker.DepartmentKey(d.Name) + "/" + subredditsFile
	data, err := os.ReadFile(filepath.Join(p.Root(), filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	rows, ok := parseSubreddits(string(data))
	if !ok {
		return []Finding{{Where: rel, Message: "its table has no header naming the Subreddit and Self-promotion rule columns, so no row is read",
			Fix: "start the table with | Subreddit | Why | Self-promotion rule | Allowed | Checked |"}}
	}
	var out []Finding
	seen := map[string]bool{}
	for _, r := range rows {
		where := fmt.Sprintf("%s:%d", rel, r.Line)
		switch {
		case !subredditNameRe.MatchString(r.Name):
			out = append(out, Finding{Where: where, Message: fmt.Sprintf("subreddit %q is not shaped r/<name>", r.Name), Fix: "write it as r/<name>, e.g. r/selfhosted"})
			continue
		case seen[strings.ToLower(r.Name)]:
			out = append(out, Finding{Where: where, Message: fmt.Sprintf("%s is listed twice", r.Name), Fix: "keep one row"})
			continue
		}
		seen[strings.ToLower(r.Name)] = true
		if ruleMissing(r.Rule) {
			out = append(out, Finding{Where: where, Message: fmt.Sprintf("%s has no self-promotion rule: the social brief quotes it beside every post there", r.Name),
				Fix: fmt.Sprintf("quote the subreddit's own rule on self-promotion with its link (https://www.reddit.com/%s/about/rules)", r.Name)})
		}
	}
	return out
}
