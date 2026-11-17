package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// roadmapRowRe matches a version table row: its first cell starts with a
// version, bold or not (`| **0.60** Decision ledger |`, `| 0.60 |`).
var roadmapRowRe = regexp.MustCompile(`^\|\s*\**v?(\d+)\.(\d+)\b`)

// milestoneRe reads a version: value as its milestone, major.minor
// (0.60.1 is milestone 0.60).
var milestoneRe = regexp.MustCompile(`^v?(\d+)\.(\d+)(?:\.\d+)?(?:-\S*)?$`)

type milestone struct{ major, minor int }

func (m milestone) String() string { return fmt.Sprintf("%d.%d", m.major, m.minor) }

func parseMilestone(s string) (milestone, bool) {
	m := milestoneRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return milestone{}, false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return milestone{major, minor}, true
}

// checkRoadmapBehind warns when docs/roadmap.md's version table stops
// before the running milestone: the highest version: an open issue
// carries. It names every milestone of that major between the table's
// highest and the running one that the table lacks, in one finding. A
// project without a roadmap, or whose table has no row of that major, is
// quiet: the check keeps an existing table current, it asks nobody to
// write one.
func checkRoadmapBehind(env *Env) []Finding {
	docs := env.Project.DocsDir
	if docs == "" {
		docs = filepath.Join(env.Project.Root(), "docs")
	}
	path := filepath.Join(docs, "roadmap.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var running milestone
	found := false
	for _, issue := range env.Issues {
		if tracker.IsClosedStatus(env.Workflow, issue.Status) {
			continue
		}
		m, ok := parseMilestone(issue.Version)
		if !ok {
			continue
		}
		if !found || m.major > running.major || (m.major == running.major && m.minor > running.minor) {
			running, found = m, true
		}
	}
	if !found {
		return nil
	}
	listed := map[int]bool{}
	highest := -1
	for _, line := range strings.Split(string(raw), "\n") {
		m := roadmapRowRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		major, _ := strconv.Atoi(m[1])
		minor, _ := strconv.Atoi(m[2])
		if major != running.major {
			continue
		}
		listed[minor] = true
		highest = max(highest, minor)
	}
	if highest < 0 || highest >= running.minor {
		return nil
	}
	var missing []string
	for n := highest + 1; n <= running.minor; n++ {
		if !listed[n] {
			missing = append(missing, milestone{running.major, n}.String())
		}
	}
	rel := "docs/roadmap.md"
	if r, err := filepath.Rel(env.Project.Root(), path); err == nil && !strings.HasPrefix(r, "..") {
		rel = filepath.ToSlash(r)
	}
	return []Finding{{
		Where: rel,
		Message: fmt.Sprintf("the version table stops at %s while open issues run %s: it lacks %s, so whoever reads it for what comes next reads an old plan",
			milestone{running.major, highest}, running, strings.Join(missing, ", ")),
		Fix: fmt.Sprintf("add a row for %s to the version table (a docs issue: issue-cli create --type docs)", strings.Join(missing, ", ")),
	}}
}
