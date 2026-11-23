package ci

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// OneLine is where the branch stands, in one line for agents and the lead:
//
//	main green at abc1234 (12m ago)
//	main red since def5678: make validate: TestA, TestB
//	main: CI can't run: <reason> — issue-cli ci reset
//
// A live run is appended: "· running at abc1234 (3m)".
func OneLine(st *State, lock *Lock, alive bool, branch string, now time.Time) string {
	if st != nil && st.Branch != "" {
		branch = st.Branch
	}
	if branch == "" {
		branch = "branch"
	}
	var line string
	switch {
	case st == nil || st.State == "":
		line = branch + ": no CI run yet"
	case st.State == Green:
		line = fmt.Sprintf("%s green at %s (%s ago)", branch, gitdir.ShortSHA(st.GreenSha), Ago(now.Sub(st.GreenAt)))
	case st.State == Red:
		line = fmt.Sprintf("%s red since %s: %s", branch, gitdir.ShortSHA(st.RedSince), FailedText(st.Failed, st.Red))
		if f := FailureList(st.Failures); f != "" {
			line += ": " + f
		}
	default:
		line = fmt.Sprintf("%s: CI can't run: %s — issue-cli ci reset", branch, firstLine(st.Error))
	}
	if lock != nil && alive {
		line += fmt.Sprintf(" · running at %s (%s)", gitdir.ShortSHA(lock.Sha), Ago(now.Sub(lock.Started)))
	}
	return line
}

// Ago is a short duration: 40s, 12m, 3h, 2d.
func Ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

func init() {
	tracker.CIStatusLine = AgentLine
}

// AgentLine is the CI line for agents: the state of the branch they merge
// into, so a failure they see can be told apart from one they caused. ""
// without a ci: block.
func AgentLine(workDir string, wf *tracker.WorkflowConfig) string {
	if wf == nil || !wf.CI.Enabled() {
		return ""
	}
	root := RepoRoot(workDir)
	st, _ := ReadState(root)
	lock, alive := ReadLock(root)
	branch, _ := ResolveBranch(root, wf.CI)
	now := time.Now()
	line := "Local CI: " + OneLine(st, lock, alive, branch, now)
	if st.IsRed() {
		line += " — inherited from " + st.Branch + ", not caused by your branch"
	}
	if branch != "" {
		line += AheadText(branch, aheadOfBase(workDir, branch))
	}
	if st.IsRed() && st.Leftover != "" {
		line += "\nLocal CI leftover: " + st.Leftover
	}
	for _, name := range st.TierNames() {
		if ts := st.Tiers[name]; ts.State == Red {
			line += "\nLocal CI " + TierLine(name, ts, st.branchOr(branch), now)
		}
	}
	return line
}

// AheadText is the Local CI line's note that branch has moved past the
// work branch's base, so its green or red says nothing of the base the
// branch was tested on: "; main is 3 commits ahead of your branch's base:
// merge main before you debug a red test". "" when n is 0.
func AheadText(branch string, n int) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return fmt.Sprintf("; %s is 1 commit ahead of your branch's base: merge %s before you debug a red test", branch, branch)
	}
	return fmt.Sprintf("; %s is %d commits ahead of your branch's base: merge %s before you debug a red test", branch, n, branch)
}

// aheadOfBase is how many commits branch has that the checkout at dir
// (HEAD) lacks since their merge base; 0 on the branch itself or when git
// can't tell.
func aheadOfBase(dir, branch string) int {
	out, err := git(dir, "rev-list", "--count", "HEAD.."+branch)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0
	}
	return n
}

// TierLine is one non-default tier's newest run, as `ci status` and the
// agents' line print it:
//
//	tier slow: green at abc1234 (3h ago)
//	tier slow: red at abc1234 (3h ago): make ux-test: crew-all/TP5 — inherited from main, not caused by your branch
//
// A red tier is marked inherited the way a red default tier is: a worker
// whose make ux-test fails reads that main already has it.
func TierLine(name string, ts TierState, branch string, now time.Time) string {
	line := fmt.Sprintf("tier %s: %s at %s (%s ago)", name, ts.State, gitdir.ShortSHA(ts.Sha), Ago(now.Sub(ts.Finished)))
	if ts.Failed != "" {
		line += ": " + FailedText(ts.Failed, ts.Red)
	}
	if f := FailureList(ts.Failures); f != "" {
		line += ": " + f
	}
	if ts.State == Red {
		line += " — inherited from " + branch + ", not caused by your branch"
	}
	return line
}

// TierNames are the non-default tiers with a run, sorted.
func (s *State) TierNames() []string {
	if s == nil {
		return nil
	}
	names := make([]string, 0, len(s.Tiers))
	for name := range s.Tiers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *State) branchOr(branch string) string {
	if s != nil && s.Branch != "" {
		return s.Branch
	}
	if branch == "" {
		return "branch"
	}
	return branch
}

// SlowestStepLine names the slowest timed step of the newest default-tier
// run among runs (newest first), so a slow step shows in ci status:
//
//	Slowest step at abc1234: test 244s (make validate 400s)
//
// "" when that run timed no steps.
func SlowestStepLine(runs []Run) string {
	for _, r := range runs {
		if r.Tier != "" && r.Tier != defaultTier {
			continue
		}
		step, ok := r.SlowestStep()
		if !ok {
			return ""
		}
		line := fmt.Sprintf("Slowest step at %s: %s %s", gitdir.ShortSHA(r.Sha), step.Name, seconds(step.DurationMS))
		for _, c := range r.Commands {
			for _, s := range c.Steps {
				if s == step {
					return line + fmt.Sprintf(" (%s %s)", c.Cmd, seconds(c.DurationMS))
				}
			}
		}
		return line
	}
	return ""
}

func seconds(ms int64) string {
	return fmt.Sprintf("%ds", (ms+500)/1000)
}
