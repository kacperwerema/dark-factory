package viewer

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/kacperwerema/dark-factory/internal/ci"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// What the pages show of local CI, read from .ci/ (internal/ci): the Status
// page's "Local CI" card, its line in status.md and "Waiting on you", the
// sidebar count, and the ci-red card markers. Nothing shows without a ci:
// block.

// CIRow is the Status page's Local CI card.
type CIRow struct {
	// Line is the one-liner `issue-cli ci status` prints.
	Line   string
	State  string // green, red, error, or "" before the first run
	Branch string
	// Sha is the green commit, or the first red one.
	Sha      string
	At       time.Time
	Failed   string
	Failures string
	Error    string
	Blamed   []*tracker.Issue
	// Others counts what landed since green without a matching issue.
	Others   int
	RedIssue *tracker.Issue
	Log      string
	LogTail  []string
	Running  bool
	RunSha   string
	// Runs are the last default-tier runs, oldest first, for the row of
	// boxes on the card.
	Runs []CIRunBox
	// LastRed is the newest kept red log (.ci/red), shown green or red;
	// nil when there is none.
	LastRed *CILastRed
}

// CILastRed is the card's Last red log line.
type CILastRed struct {
	Log  string // relative to the project root
	URL  string
	Sha  string
	Tier string // "" for the default tier
	At   time.Time
	Ago  string // "40m"
}

// CIRunBox is one box in the card's last-runs row.
type CIRunBox struct {
	Sha   string
	State string // green, red, error, or running
	Title string // tooltip: sha, state, when, how long, what failed
	// LogURL is a red run's kept log, "" when it has none (or it was pruned).
	LogURL string
}

// ciLogURL is the page serving a kept red log (handleCILog).
func ciLogURL(prefix, rel string) string { return prefix + "/ci/log/" + filepath.Base(rel) }

// ciRunsShown is how many past runs the card shows.
const ciRunsShown = 12

// ciRunBoxes lists the newest default-tier runs as boxes, oldest first, and
// a running box at the end while a run is live.
func ciRunBoxes(root, prefix string, lock *ci.Lock, alive bool, now time.Time) []CIRunBox {
	var boxes []CIRunBox
	for _, r := range ci.RecentRuns(root, 0) {
		if r.Tier != tracker.CIDefaultTier {
			continue
		}
		title := fmt.Sprintf("%s · %s · %s ago · took %s", ci.Short(r.Sha), r.State, ci.Ago(now.Sub(r.Finished)), r.Finished.Sub(r.Started).Round(time.Second))
		switch {
		case r.Failed != "":
			title += " · " + r.Failed
			if f := ci.FailureList(r.Failures); f != "" {
				title += ": " + f
			}
		case r.Error != "":
			title += " · " + r.Error
		}
		if r.Subject != "" {
			title += "\n" + r.Subject
		}
		box := CIRunBox{Sha: ci.Short(r.Sha), State: r.State, Title: title}
		if r.KeptLog != "" && fileExists(filepath.Join(root, r.KeptLog)) {
			box.LogURL = ciLogURL(prefix, r.KeptLog)
			box.Title += "\nClick for the kept log"
		}
		boxes = append(boxes, box)
		if len(boxes) == ciRunsShown {
			break
		}
	}
	for i, j := 0, len(boxes)-1; i < j; i, j = i+1, j-1 {
		boxes[i], boxes[j] = boxes[j], boxes[i]
	}
	if alive && lock.Tier == tracker.CIDefaultTier {
		boxes = append(boxes, CIRunBox{Sha: ci.Short(lock.Sha), State: "running",
			Title: fmt.Sprintf("%s · running for %s", ci.Short(lock.Sha), ci.Ago(now.Sub(lock.Started)))})
	}
	return boxes
}

// Red reports whether the branch is red.
func (r *CIRow) Red() bool { return r != nil && r.State == ci.Red }

// ciState reads proj's CI state; nil when the project has no ci: block.
func ciState(proj *tracker.Project) (*ci.State, bool) {
	if !proj.LoadWorkflow().CI.Enabled() {
		return nil, false
	}
	st, _ := ci.ReadState(ciRootFor(proj))
	return st, true
}

// ciRedState is the state while local CI is red, else nil.
func ciRedState(proj *tracker.Project) *ci.State {
	if st, _ := ciState(proj); st.IsRed() {
		return st
	}
	return nil
}

// ciRow builds the Local CI card; nil without a ci: block.
func ciRow(proj *tracker.Project, prefix string, issues []*tracker.Issue, now time.Time) *CIRow {
	st, on := ciState(proj)
	if !on {
		return nil
	}
	root := ciRootFor(proj)
	lock, alive := ci.ReadLock(root)
	branch, _ := ci.ResolveBranch(root, proj.LoadWorkflow().CI)
	row := &CIRow{Line: ci.OneLine(st, lock, alive, branch, now), Branch: branch, Running: alive, Runs: ciRunBoxes(root, prefix, lock, alive, now)}
	if alive {
		row.RunSha = ci.Short(lock.Sha)
	}
	if st == nil {
		return row
	}
	row.State, row.Branch, row.Error = st.State, st.Branch, st.Error
	if rel := st.LastRedLogPath(root); rel != "" {
		row.LastRed = &CILastRed{Log: rel, URL: ciLogURL(prefix, rel), Sha: ci.Short(st.LastRedSha), At: st.LastRedAt, Ago: ci.Ago(now.Sub(st.LastRedAt))}
		if st.LastRedTier != tracker.CIDefaultTier {
			row.LastRed.Tier = st.LastRedTier
		}
	}
	switch st.State {
	case ci.Green:
		row.Sha, row.At = ci.Short(st.GreenSha), st.GreenAt
	case ci.Red:
		row.Sha, row.At, row.Failed, row.Failures = ci.Short(st.RedSince), st.Updated, st.Failed, ci.FailureList(st.Failures)
		row.Log, row.LogTail = st.Log, st.LogTail
		for _, slug := range st.Blamed {
			if issue := tracker.FindIssue(issues, slug); issue != nil {
				row.Blamed = append(row.Blamed, issue)
			}
		}
		for _, m := range st.Merged {
			if m.Slug == "" {
				row.Others++
			}
		}
		row.RedIssue = tracker.FindIssue(issues, st.RedIssue)
	default:
		row.Sha, row.At, row.Log, row.LogTail = ci.Short(st.LastSha), st.Updated, st.Log, st.LogTail
	}
	return row
}

// handleCILog serves one kept red log from .ci/red as plain text: GET
// /p/<project>/ci/log/<name>. Only a name ci.ParseRedLogName reads is
// served, so no path leaves .ci/red.
func (s *Server) handleCILog(w http.ResponseWriter, r *http.Request, proj *tracker.Project, name string) {
	if !proj.LoadWorkflow().CI.Enabled() {
		http.NotFound(w, r)
		return
	}
	if _, ok := ci.ParseRedLogName(name); !ok {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(filepath.Join(ci.RedDir(ciRootFor(proj)), name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(data)
}
