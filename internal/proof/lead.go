package proof

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A lead link (tracker.ProofLead) names one issue-cli command only the lead
// can run: the worker guard refuses it from the worker's shell (a guided
// check script that dispatches or spawns a lead). The worker links it with
// `prove <slug> <AC> --lead "<command>"` and stops; the criterion reads
// "waits on the lead: <command>" on prove --list, the gate, the Station and
// the lead's tick (queue next) until the lead runs `prove <slug>
// --lead-run`, which runs it and records a tracker.LeadRun.
//
// The command is written by a worker and run with the lead's rights, so it
// is never handed to a shell: ParseLeadCommand splits it into argv and
// takes only the forms below, on the issue's own slug.

// LeadWaitsPrefix starts the state of a lead link that has no passing run
// for the branch's last change.
const LeadWaitsPrefix = "waits on the lead: "

// LeadCommand is a parsed lead link command.
type LeadCommand struct {
	// Env is NAME=value words to add to the lead's environment.
	Env []string
	// Args is the issue-cli arguments, without the program name and
	// without --project (the runner injects it).
	Args []string
}

// leadForms are the commands a lead link may name: the subcommand, then
// the flags it takes (a flag ending in "=" takes a value).
var leadForms = map[string][]string{
	"check-script": {"--yes", "--cli="},
	"prove":        {"--run"},
}

var envNameRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// leadEnvDenied are env names that change how a program is found, loaded
// or run, or who the run is.
func leadEnvDenied(name string) bool {
	switch name {
	case "PATH", "BASH_ENV", "ENV", "IFS", "HOME", "SHELL", "ISSUE_CLI", "CDPATH", "PS4", "PROMPT_COMMAND":
		return true
	}
	for _, p := range []string{"LD_", "DYLD_", "TMUX", "GIT_", "ISSUE_VIEWER_", "XDG_", "GO"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// LeadAllowed says which commands a lead link may name, for refusals and
// help.
func LeadAllowed(slug string) string {
	return fmt.Sprintf("issue-cli check-script %s [--yes] [--cli <path in the worktree>], or issue-cli prove %s --run, after optional NAME=value words", slug, slug)
}

// ParseLeadCommand splits a lead link's command into env and issue-cli
// arguments, refusing anything outside the allowed forms or naming another
// issue than slug. Quotes ('…' and "…") group words; shell syntax is
// refused, never interpreted.
func ParseLeadCommand(slug, cmd string) (LeadCommand, error) {
	refuse := func(why string) (LeadCommand, error) {
		return LeadCommand{}, fmt.Errorf("lead command %q refused: %s. A lead link takes %s", cmd, why, LeadAllowed(slug))
	}
	words, err := splitWords(cmd)
	if err != nil {
		return refuse(err.Error())
	}
	var lc LeadCommand
	for len(words) > 0 {
		name, _, ok := strings.Cut(words[0], "=")
		if !ok || strings.HasPrefix(words[0], "-") {
			break
		}
		if !envNameRe.MatchString(name) {
			return refuse("env name " + name + " is not NAME=value")
		}
		if leadEnvDenied(name) {
			return refuse("env " + name + " changes how the command runs")
		}
		lc.Env = append(lc.Env, words[0])
		words = words[1:]
	}
	if len(words) > 0 && (words[0] == "issue-cli" || filepath.Base(words[0]) == "issue-cli") {
		words = words[1:]
	}
	if len(words) < 2 {
		return refuse("it names no subcommand and slug")
	}
	sub, target, flags := words[0], words[1], words[2:]
	allowed, ok := leadForms[sub]
	if !ok {
		return refuse("subcommand " + sub + " is not allowed")
	}
	if target != slug {
		return refuse("it names " + target + ", not this issue")
	}
	lc.Args = []string{sub, target}
	for i := 0; i < len(flags); i++ {
		f := flags[i]
		name, val, hasVal := strings.Cut(f, "=")
		switch {
		case containsString(allowed, name) && !hasVal:
			lc.Args = append(lc.Args, f)
		case containsString(allowed, name+"="):
			if !hasVal {
				if i+1 >= len(flags) {
					return refuse(name + " needs a value")
				}
				i++
				val = flags[i]
			}
			if err := leadFlagValue(name, val); err != nil {
				return refuse(err.Error())
			}
			lc.Args = append(lc.Args, name+"="+val)
		default:
			return refuse("flag " + f + " is not allowed for " + sub)
		}
	}
	if sub == "prove" && !containsString(lc.Args, "--run") {
		return refuse("prove takes only --run")
	}
	return lc, nil
}

// leadFlagValue checks a flag's value: --cli must be a path inside the
// issue's worktree (relative, no ..), where the command runs.
func leadFlagValue(name, val string) error {
	if name == "--cli" {
		clean := filepath.Clean(val)
		if val == "" || filepath.IsAbs(val) || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(val, "-") {
			return fmt.Errorf("--cli must be a path inside the issue's worktree, got %q", val)
		}
	}
	return nil
}

// splitWords splits s on blanks, with '…' and "…" grouping; any shell
// syntax outside quotes is an error, so nothing reads as a pipe, a
// substitution or a second command.
func splitWords(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			if quote == '"' && (r == '$' || r == '`' || r == '\\') {
				return nil, fmt.Errorf("%q inside double quotes", r)
			}
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case strings.ContainsRune(";|&`$<>()\\\n\r*?[]{}~#!", r):
			return nil, fmt.Errorf("shell syntax %q", r)
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unclosed quote")
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

// LatestLeadRun is the newest recorded run of command.
func LatestLeadRun(runs []tracker.LeadRun, command string) (tracker.LeadRun, bool) {
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Command == command {
			return runs[i], true
		}
	}
	return tracker.LeadRun{}, false
}

// checkLead is a lead link's check: its command's newest run passed and
// came after the branch's last code change (or ran at a commit holding it).
func (c *checker) checkLead(cmd string) Check {
	fix := fmt.Sprintf("issue-cli prove %s --lead-run (the lead runs it; a worker stops here, no question: comment needed)", c.slug)
	run, ok := LatestLeadRun(c.leadRuns, cmd)
	switch {
	case !ok:
		return Check{State: LeadWaitsPrefix + cmd, Fix: fix}
	case !run.OK:
		return Check{State: fmt.Sprintf("%s%s (last run failed %s, exit %d)", LeadWaitsPrefix, cmd, run.At.Local().Format("2006-01-02 15:04"), run.Exit), Fix: fix}
	case !c.lastChange.IsZero() && run.At.Before(c.lastChange) && !c.headHolds(run.Head):
		return Check{State: LeadWaitsPrefix + cmd + " (ran before the last change)", Fix: fix}
	}
	by := ""
	if run.By != "" {
		by = " by " + run.By
	}
	return Check{OK: true, State: "the lead ran it" + by + " " + run.At.Local().Format("2006-01-02 15:04") + ": passed"}
}

// headHolds reports whether a run's head contains the branch's last code
// change: it is that commit or a descendant.
func (c *checker) headHolds(head string) bool {
	if head == "" || c.lastChangeSHA == "" || strings.HasPrefix(head, "-") {
		return false
	}
	if strings.HasPrefix(c.lastChangeSHA, head) || strings.HasPrefix(head, c.lastChangeSHA) {
		return true
	}
	_, err := tracker.GitCached(c.root, "merge-base", "--is-ancestor", c.lastChangeSHA, head)
	return err == nil
}

// LeadWait is a criterion waiting on the lead's command.
type LeadWait struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	State   string `json:"state"`
}

// WaitsOnLead lists the unproven criteria with a lead link that has no
// passing run for the last change.
func (r Report) WaitsOnLead() []LeadWait {
	var out []LeadWait
	for _, c := range r.Unproven() {
		for _, l := range c.Links {
			if l.Kind == tracker.ProofLead && !l.Check.OK {
				out = append(out, LeadWait{ID: c.ID, Command: l.Ref, State: l.Check.State})
			}
		}
	}
	return out
}

// PendingLead reads a data sidecar alone (no git, no comments): each lead
// link whose command never ran, last failed, or last ran before the link
// was made. The lead's tick lists them; Collect's check, which also knows
// the branch's last change, is the one the gate reads.
func PendingLead(store tracker.DataStore) []LeadWait {
	var out []LeadWait
	for _, id := range sortedKeys(store.Proofs) {
		for _, l := range store.Proofs[id] {
			if l.Kind != tracker.ProofLead {
				continue
			}
			run, ok := LatestLeadRun(store.LeadRuns, l.Ref)
			switch {
			case !ok:
				out = append(out, LeadWait{ID: id, Command: l.Ref, State: "never ran"})
			case !run.OK:
				out = append(out, LeadWait{ID: id, Command: l.Ref, State: "last run failed " + run.At.Local().Format("2006-01-02 15:04")})
			case run.At.Before(l.At):
				out = append(out, LeadWait{ID: id, Command: l.Ref, State: "linked after its last run"})
			}
		}
	}
	return out
}

func sortedKeys(m map[string][]tracker.ProofLink) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// IssueLeadWaits is PendingLead for one issue: nothing for a closed issue
// (closed answers it) or one without a sidecar, and only unticked criteria.
// Reads the sidecar alone, so a tick over every issue stays cheap.
func IssueLeadWaits(issue *tracker.Issue, closed func(*tracker.Issue) bool) []LeadWait {
	if closed(issue) {
		return nil
	}
	if _, err := os.Stat(tracker.SidecarPath(issue.FilePath)); err != nil {
		return nil
	}
	store, err := tracker.LoadData(issue.FilePath)
	if err != nil {
		return nil
	}
	ticked := map[string]bool{}
	for _, it := range tracker.Criteria(issue.BodyRaw) {
		ticked[strings.ToUpper(it.ID)] = it.Checked
	}
	var out []LeadWait
	for _, w := range PendingLead(store) {
		if !ticked[strings.ToUpper(w.ID)] {
			out = append(out, w)
		}
	}
	return out
}
