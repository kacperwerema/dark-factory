package briefing

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/leadstate"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// LeadBriefInput is what a lead brief is rendered from.
type LeadBriefInput struct {
	Project *tracker.Project
	// Root is the project checkout (where sessions/ lives).
	Root string
	// Now dates the handoff note the lead is told to write.
	Now time.Time
	// AutoStart is the project's auto-start when the owner turned it on;
	// nil when it is off.
	AutoStart *AutoStartBrief
	// Live reports whether a lead session is running (a group's brief
	// names its departments' leads with it); nil means none is.
	Live func(name string) bool
}

// AutoStartBrief is what the lead is told of the project's auto-start.
type AutoStartBrief struct {
	Words      string  // the owner's words that turned it on
	Below      int     // memory %
	MaxLoad    float64 // the owner's max_load; 0: the core count
	MaxWorkers int
	Paused     bool
}

// today is the date expiry is judged by: Now, else the clock.
func (in LeadBriefInput) today() time.Time {
	if !in.Now.IsZero() {
		return in.Now
	}
	return tracker.DecisionsNow()
}

// LeadBrief renders the brief the project lead is spawned with: the default
// (or the workflow's leads.project, which replaces it), the workflow's
// leads.project_addendum, then a fixed footer with the lead's role, authority
// and handoff note. issue-cli commands get --project injected like dispatch
// prompts.
func LeadBrief(wf *tracker.WorkflowConfig, in LeadBriefInput) string {
	proj := in.Project
	tmpl := strings.TrimSpace(tracker.DefaultProjectLeadBrief)
	if wf != nil {
		if p := strings.TrimSpace(wf.Leads.Project); p != "" {
			tmpl = p
		}
		if a := strings.TrimSpace(wf.Leads.ProjectAddendum); a != "" {
			tmpl += "\n\n" + a
		}
	}
	projectName := ""
	if proj != nil {
		projectName = proj.Name
		if projectName == "" {
			projectName = proj.Slug
		}
	}
	lead := tracker.LeadSessionName(proj)
	brief := tracker.FillPlaceholders(tmpl, tracker.LeadValues(projectName, lead))

	var b strings.Builder
	b.WriteString(brief)
	b.WriteString("\n\n## Your role\n\n")
	fmt.Fprintf(&b, "- Role: project lead (%s). You answer to the human.\n", lead)
	b.WriteString(tracker.LeadRules)
	b.WriteString("- Authority: never Human Testing (the human approves it in the viewer); workers never tick.\n")
	fmt.Fprintf(&b, "- Messaging: agents are addressed by tmux name (yours is %s); don't ask workers to report to you. lead message is for Codex only (for Claude it prints the SendMessage call).\n", lead)
	b.WriteString("- Dispatch starts the worker like ▶ on the board and leaves the words on the issue (--agent codex; --reprompt re-sends a live worker's briefing, also with --quote); it refuses, exit 3, while the issue only waits on the human's approval. Never call the viewer over HTTP. Starting any other agent (health review --spawn) needs the human's words too.\n")
	if wf != nil && strings.TrimSpace(wf.Leads.Project) != "" {
		// The default brief's Relay questions line says it; a replaced
		// brief may not.
		b.WriteString("- Decisions: each owner decision goes on its issue in the human's words (issue-cli decide <slug> --quote).\n")
	}
	limits := autostart.Limits{}
	if a := in.AutoStart; a != nil {
		limits = autostart.Limits{Below: a.Below, MaxWorkers: a.MaxWorkers, MaxLoad: a.MaxLoad}
		fmt.Fprintf(&b, "- Auto-start is on for this project (the owner's words: %q): the one exception to dispatching only what the human names. At each check-in run issue-cli autostart next and the dispatch --autostart command it prints, one per check-in (Holds, below). Never turn auto-start on, off or pause it without the human's words.", a.Words)
		if a.Paused {
			b.WriteString(" It is paused now: start nothing until the human resumes it.")
		}
		b.WriteString("\n")
	}
	if proj != nil {
		if wf := proj.LoadWorkflow(); wf != nil && wf.Routing != nil {
			b.WriteString("- Takeover: when a worker stops at its usage limit, issue-cli queue next, autostart next and agents print a dispatch <slug> --takeover command. Run it at the check-in: the other agent takes the branch over on the issue's own words, whether auto-start is on or not. It runs once per issue; a limit after that is the human's.\n")
		}
	}
	b.WriteString(LeadTickLine)
	b.WriteString("- Queue: issue-cli queue keeps the project's ordered plan (add <slug> or --idea, move, block, list). The tick starts its first startable item; shipped items are marked done with their SHA. Queue an item with the human's words (--quote); without them it starts only while auto-start is on. When only \"think up more\" is left, refill it.\n")
	b.WriteString(HoldsLine(limits))
	n := 0
	if proj != nil {
		for _, d := range proj.Departments {
			n += len(d.Jobs)
		}
	}
	b.WriteString(tracker.JobsBrief(n, true))
	b.WriteString(RulesLeadLines(wf))
	b.WriteString(MemoryLeadLine(proj, lead, ""))
	b.WriteString(tracker.DecisionsLeadLines(tracker.LeadDecisionBlock(proj, "", wf, in.today()), ""))
	if proj != nil && len(proj.Departments) > 0 {
		b.WriteString(tracker.HandoffsCEOBrief)
		b.WriteString(tracker.HQReportCEOBrief)
	}
	if wf != nil && wf.CI.Enabled() {
		b.WriteString("- Local CI: the ci: commands run on main after every move. CI never messages you: read issue-cli ci status at each check-in and before any push; when it is red, decide with the human whether to revert, fix forward or dispatch a fix. Never push a release while it is red.\n")
	}
	if wf != nil && wf.Workbench.ReleaseCandidates {
		b.WriteString(releaseCandidateBrief)
		if wf.Privacy.Snapshots() {
			b.WriteString(snapshotsBrief)
		}
	}
	if wf != nil && wf.Privacy.Tracker() {
		b.WriteString(trackerBrief)
	}
	notesDir := filepath.Join(in.Root, "sessions")
	if note := tracker.NewestHandoffNote(notesDir, lead); note != "" {
		fmt.Fprintf(&b, "- Handoff: read %s first; it is your previous handoff note.\n", note)
	} else {
		b.WriteString("- Handoff: there is no earlier handoff note for you.\n")
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	fmt.Fprintf(&b, "- Before you exit, or before the human takes a long break, write %s with %s.", filepath.Join(notesDir, tracker.HandoffNoteName(now, lead)), HandoffNoteSections)
	b.WriteString(" issue-cli status (the Status page as text, --json for the data) shows that list and is a ready-made stand-up; a \"+K more\" line names the command for the rest.")
	b.WriteString("\n- issue-cli lead whoami shows this role again.\n")
	// The note is the last lead's own words: added after InjectProject,
	// which is for the commands above.
	b.WriteString(LeadStateLine)
	// A fresh session after a rotate starts from the state file; after a
	// flush close from the note, its chat summary and the state file;
	// otherwise the newest note and its chat summary.
	if h := leadstate.HandoverSection(lead, tracker.InjectProject(proj, "issue-cli lead history <words>"), now); h != "" {
		if leadstate.FreshHandover(lead, now).FromFlush() {
			h = tracker.LeadHandoffSection(notesDir, lead) + h
		}
		return tracker.InjectProject(proj, b.String()) + h
	}
	return tracker.InjectProject(proj, b.String()) + tracker.LeadHandoffSection(notesDir, lead) + leadstate.LiveSection(lead)
}

// HandoffNoteSections is what a lead's own handoff note holds, the
// sections tracker.HandoffHeadings checks: workers, the queue and the
// owner's asks are the state file's, which the next brief renders live.
const HandoffNoteSections = "## Next (the ordered steps), ## Rules in force and ## Released and installed; never workers, queue or asks (your state file has them)"

// HoldsLine is the lead brief's one list of what holds a start: the
// queue's holds (auto-start's, plus local CI red), with the project's
// auto-start limits when it is on.
func HoldsLine(l autostart.Limits) string {
	return "- Holds: queue next --start holds by itself while " + autostart.HoldSentence(append(autostart.HoldClauses(l), autostart.CIRedClause)) + "; autostart next holds the same, CI aside. mem-guard check stays the hard stop.\n"
}

// ReleaseDoc and PrivacyDoc are the pages the release and privacy lines
// point at (TestLeadBriefRulesMovedToDocsAreThere).
const (
	ReleaseDoc = "docs/roadmap.md#cutting-a-release-candidate-soak-publish"
	PrivacyDoc = "docs/workflow.md#private-work-and-code"
)

// releaseCandidateBrief is the lead's line for workbench.release_candidates.
const releaseCandidateBrief = "- Releases: candidate, soak, publish (" + ReleaseDoc + "): when Waiting for release says ready, release cut <version>; then release pick <slug> for each fix from main (each pick needs the next candidate). Never push a candidate tag; never publish without the human's explicit ok on a soaked candidate.\n"

// snapshotsBrief is the lead's line for privacy.code: snapshots.
const snapshotsBrief = "- Private development (code: snapshots, " + PrivacyDoc + "): publish only with issue-cli release publish <version> from the release worktree, on the human's ok; never push anything else to public.\n"

// trackerBrief is the lead's line for privacy.work_files: tracker.
const trackerBrief = "- Private tracker (work_files: tracker): before a handoff run issue-cli tracker sync; never add work files to the code repo.\n"

// LeadStateLine is every lead's line for its state file, rotate and history
// (docs/patterns/lead-session.md#the-state-file).
const LeadStateLine = "- State: issue-cli lead state is your compact state file; the tick rewrites it. Note what you'd need after a restart there, not in a long chat: issue-cli lead state note \"<text>\" [--tomorrow], an owner's request with issue-cli lead state ask --quote \"<their words>\" (done <n> when it is). When the tick says rotate, issue-cli lead rotate writes the handoff note and a digest of this chat, then starts a fresh you that reads the state first; end your turn then. Look old context up with issue-cli lead history <words> instead of carrying it.\n"

// LeadTickLine is the lead brief's tick line: the one command the lead's
// 5-minute check-in is, and how to keep it running without waking for
// nothing.
const LeadTickLine = "- Waiting: you wait in one way only, a background shell that exits on news (Bash run_in_background), started again each time it exits: issue-cli lead tick --watch --exit-on-news (add --observe [--department <d>] when you start no work), and for any other probe issue-cli lead watch --run '<cmd>' (--until-change, --until '<regex>'). Never a /loop cron, a ScheduleWakeup poll or a Monitor (it expires every 30 minutes): each wakes your whole context, news or not (measured 2026-11-15: up to 960k tokens a wake).\n- Tick: your 5-minute check-in is issue-cli lead tick. Each tick starts the queue's next item (a due job on its standing start first), closes finished worker sessions whose branch is on main, and prints a line only when you have to act: a done issue's branch to land, local CI red, a start held for a new reason, the queue at 5 open items or fewer, the owner's comment, a takeover or a stalled worker. It says a thing once and a quiet tick prints nothing: don't run the check-in by hand. Under --exit-on-news a plain start or a closed session doesn't wake you: it prints above the next news.\n"
