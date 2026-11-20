package briefing

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tokens"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Limits for the memories at the status (as transition prints them).
const (
	stageMemoryLimit = 3
	stageMemoryChars = 600
)

// stagePart is one part of a stage briefing: its text, and what it folds to
// (empty: it never folds).
type stagePart struct {
	name   string
	text   string
	folded string
}

// stageInput is what BuildDispatchPrompt hands the stage briefing.
type stageInput struct {
	proj      *tracker.Project
	issue     *tracker.Issue
	wf        *tracker.WorkflowConfig
	worktree  tracker.WorktreeSpec
	workDir   string
	route     tracker.LeadRoute
	decisions tracker.DecisionBlock
	yoloOn    bool
}

// buildStageBriefing is the briefing of a fresh session at the issue's
// status, and its size against the budget.
func buildStageBriefing(in stageInput) (string, tracker.StageBriefing) {
	proj, issue, wf := in.proj, in.issue, in.wf
	inject := func(s string) string { return tracker.InjectProject(proj, s) }
	budget := wf.BriefingBudgetFor(issue.Status)

	parts := []stagePart{
		{name: "head", text: stageHead(in)},
		{name: "stage", text: inject(stageWork(in))},
		{name: "how", text: inject(stageHowTo(issue.Slug, wf, in.yoloOn))},
		{name: "worktree", text: tracker.WorktreePromptSection(proj, issue, in.worktree.Path, in.worktree.Branch)},
		{name: "handoff", text: tracker.HandoffPromptSection(wf)},
		{name: "lead", text: inject(tracker.LeadPromptSection(issue, in.route))},
		{name: "owner-words", text: inject(tracker.OwnerWordsPromptSection(issue))},
		{name: "resume", text: inject(tracker.ResumePromptSection(issue))},
		{name: "rules", text: inject(RulesPromptSection(wf))},
	}
	if text := stageMemories(proj, issue); text != "" {
		parts = append(parts, stagePart{name: tracker.StagePartMemories, text: inject(text),
			folded: inject(fmt.Sprintf("\n## Memory\n\nMemories for %s: issue-cli memory list --at %s; pinned ones: issue-cli memory list --pinned.\n", issue.Status, shell.Quote(issue.Status)))})
	} else {
		parts = append(parts, stagePart{name: "memory-pointer", text: inject("\n## Memory\n\nPinned memories: issue-cli memory list --pinned; more: issue-cli memory search <words>.\n")})
	}
	if !in.decisions.Empty() {
		hard := 0
		for _, e := range append(append([]tracker.InForceEntry(nil), in.decisions.Shown...), in.decisions.Folded...) {
			if e.Entry != nil && e.Entry.Hard {
				hard++
			}
		}
		parts = append(parts, stagePart{name: tracker.StagePartDecisions, text: inject(tracker.DecisionsPromptSection(in.decisions, issue.Slug)),
			folded: inject(fmt.Sprintf("\n\n## Decisions in force\n\n%d decisions reach this issue at %s (%d hard: cite each one your work touches): %s\n", len(in.decisions.Shown)+len(in.decisions.Folded), issue.Status, hard, tracker.InForceCommand(issue.Slug)))})
	}
	if !wf.HasRole(issue.Status, tracker.StatusRoleDesign) && !wf.HasRole(issue.Status, tracker.StatusRoleQueue) {
		parts = append(parts,
			stagePart{name: tracker.StagePartShell, text: tracker.ShellPromptSection(tracker.DetectShell(), tracker.ProjectTestsFor(in.workDir, wf)), folded: "\n## Shell on this machine\n\nSee docs/getting-started.md#shell-notes.\n"},
			stagePart{name: tracker.StagePartCI, text: inject(tracker.CIPromptSection(in.workDir, wf)), folded: inject("\n## Local CI\n\nissue-cli ci status: a failure main already has is not yours to fix.\n")},
			mainHoldsPart(in),
			stagePart{name: tracker.StagePartBuild, text: tracker.BuildPromptSection(proj, in.workDir, wf), folded: ""},
			stagePart{name: tracker.StagePartRedRounds, text: "\n\n" + inject(tracker.RedRoundsPromptSection(issue.Slug, wf)), folded: inject("\n\n## Red checks\n\nissue-cli rounds " + issue.Slug + ": each red check against its cap; stop working on one the cap turned into a question.\n")},
		)
	}
	// The build part has no command of its own: it folds to nothing but a
	// line saying so, which still names the rule.
	for i := range parts {
		if parts[i].name == tracker.StagePartBuild && parts[i].text != "" {
			parts[i].folded = "\n## Your issue-cli build\n\nBuild your branch's issue-cli once in your worktree (go build -o ./issue-cli ./cmd/issue-cli) when the installed one is behind main; never make install.\n"
		}
	}

	info := tracker.StageBriefing{Budget: budget}
	render := func() string {
		body := tracker.ShortenOwnSlug(joinStageParts(parts), issue.Slug)
		return body + stageFooter(info, tokens.Estimate(body))
	}
	text := render()
	for _, name := range tracker.StageFoldOrder {
		if tokens.Estimate(text) <= budget {
			break
		}
		for i := range parts {
			if parts[i].name == name && parts[i].text != "" && parts[i].folded != "" {
				parts[i].text = parts[i].folded
				info.Folded = append(info.Folded, name)
			}
		}
		text = render()
	}
	if tokens.Estimate(text) > budget {
		info.OverBudget = true
		text = render()
	}
	info.Tokens = tokens.Estimate(text)
	return text, info
}

// mainHoldsPart is the "## Already on main?" part: the commits main made to
// the tests the brief names since the issue was created. It folds to the
// summary line and the start that prints the rest.
func mainHoldsPart(in stageInput) stagePart {
	m := tracker.MainHolds(in.workDir, in.issue)
	p := stagePart{name: tracker.StagePartMainHolds, text: tracker.MainHoldsPromptSection(m)}
	if p.text != "" {
		p.folded = tracker.InjectProject(in.proj, "\n\n## Already on main?\n\n"+m.Summary()+": issue-cli start prints the commits and the go test that checks them.\n")
	}
	return p
}

func joinStageParts(parts []stagePart) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.text)
	}
	return b.String()
}

// stageFooter is the briefing's last line: the size of what is above it
// against the budget.
func stageFooter(info tracker.StageBriefing, n int) string {
	s := fmt.Sprintf("\n---\nBriefing: %d tokens of %d", n, info.Budget)
	if len(info.Folded) > 0 {
		s += " (folded: " + strings.Join(info.Folded, ", ") + ")"
	}
	if info.OverBudget {
		s += ", over budget"
	}
	return s + ".\n"
}

// stageHead opens the briefing: who it is for, that the session is fresh,
// and the paths to read first.
func stageHead(in stageInput) string {
	issue, wf := in.issue, in.wf
	var b strings.Builder
	fmt.Fprintf(&b, "You have been assigned this issue at one stage: %s, status %s.\n\n", issue.Slug, issue.Status)
	b.WriteString("## You start this stage fresh\n\n")
	b.WriteString("This session has no chat from earlier stages, on purpose: what they settled is in the files below. Read them first, then carry on from HANDOFF.md's next step.\n")
	fmt.Fprintf(&b, "- The issue: %s (read it; never edit it by hand: issue-cli does that).\n", absPath(issue.FilePath))
	if prev, sections := wf.PreviousStageSections(issue.Status); len(sections) > 0 {
		fmt.Fprintf(&b, "- What the stage before wrote (%s): %s in that file.\n", prev, sectionList(sections))
	}
	dir := in.worktree.Path
	if dir == "" {
		dir = in.workDir
	}
	fmt.Fprintf(&b, "- %s: the last session's done, next step and open questions. It may be missing or a step behind: then the issue's comments and `git log` say where the work is.\n", filepath.Join(dir, tracker.HandoffFile))
	b.WriteString("\n## Issue\n\n")
	fmt.Fprintf(&b, "  Title: %s\n  Status: %s\n", issue.Title, issue.Status)
	if wf != nil && wf.ActiveType != "" {
		fmt.Fprintf(&b, "  Type: %s (%s)\n", wf.ActiveType, wf.PathLine())
	}
	if issue.Priority != "" {
		fmt.Fprintf(&b, "  Priority: %s\n", issue.Priority)
	}
	return b.String()
}

// stageWork is the stage's guidance, its output and the gate out.
func stageWork(in stageInput) string {
	issue, wf := in.issue, in.wf
	guidance := wf.StatusPrompt(issue.Status)
	if strings.TrimSpace(guidance) == "" {
		guidance = "Use issue-cli to inspect the current workflow requirements for this status before making changes."
	}
	if in.yoloOn {
		guidance, _ = tracker.YOLOGuidance(guidance)
		guidance = strings.TrimSpace(guidance + "\n" + tracker.YOLORule(wf, issue.Slug))
	}
	var b strings.Builder
	b.WriteString("\n## This stage\n\n")
	b.WriteString(strings.ReplaceAll(strings.TrimSpace(guidance), "<slug>", issue.Slug) + "\n")
	b.WriteString("\nYour output is this stage's artifact")
	if sections := wf.EntrySections(issue.Status); len(sections) > 0 {
		b.WriteString(": " + sectionList(sections) + " in the issue, with their boxes")
	}
	b.WriteString(". The gate out checks it")
	if next, lines := wf.GateOut(issue.Status); next != "" {
		fmt.Fprintf(&b, " (into %s):\n", next)
		for _, l := range lines {
			b.WriteString("- " + l + "\n")
		}
		fmt.Fprintf(&b, "Run issue-cli transition %s --to %s --check <id> [<id>...] once the work is done: a refusal lists every unmet requirement with its box ids and the one call that moves, so no --dry-run first.\n", issue.Slug, shell.Quote(next))
	} else {
		b.WriteString(".\n")
	}
	gates := tracker.DispatchGateLines(wf, in.yoloOn)
	b.WriteString("\n" + strings.ReplaceAll(gates["gate_rules"], "<slug>", issue.Slug) + "\n")
	b.WriteString("This issue is your one task: when it reaches a gate that waits on the human, or its last status, stop there. Don't start, claim or move another issue, and don't start other agents.\n")
	return b.String()
}

// stageHowTo is the short list of commands a stage needs.
func stageHowTo(slug string, wf *tracker.WorkflowConfig, yoloOn bool) string {
	gates := tracker.DispatchGateLines(wf, yoloOn)
	var b strings.Builder
	b.WriteString("\n## How to work\n\n")
	fmt.Fprintf(&b, "  issue-cli start %s        # claims it again and prints the checklist, the next step and the memories matching the issue\n", slug)
	fmt.Fprintf(&b, "  issue-cli checklist %s    # each box's id\n", slug)
	fmt.Fprintf(&b, "  issue-cli check %s <id> [<id>...]\n", slug)
	fmt.Fprintf(&b, "  issue-cli transition %s --to \"<next>\" [--check <id>...]   # a refusal lists everything still unmet, with box ids\n", slug)
	b.WriteString(strings.ReplaceAll(gates["ask_rule"], "<slug>", slug) + "\n")
	b.WriteString("Never modify issue .md files by hand; a bug in issue-cli goes to issue-cli report-bug \"...\".\n")
	b.WriteString(tracker.ReadRule + "\n")
	return b.String()
}

// stageMemories is the memories that matter at the issue's status, "" when
// none does.
func stageMemories(proj *tracker.Project, issue *tracker.Issue) string {
	v := memory.Viewer{Agent: tracker.AgentSessionName(issue.Slug)}
	if proj != nil {
		if d := proj.DepartmentOf(issue); d != "" {
			v.Department = tracker.DepartmentKey(d)
		}
	}
	at, _ := memory.ForStage(briefingMemories(proj, v), issue.Status, stageScores(proj), memoryNow(), stageMemoryLimit, stageMemoryChars)
	if len(at.Shown) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n## Memory\n\nWhat earlier agents learned about %s (pinned ones: issue-cli memory list --pinned):\n", issue.Status)
	for _, m := range at.Shown {
		fmt.Fprintf(&b, "- %s · %s: %s\n", m.ID, m.Kind, m.Text)
	}
	return b.String()
}

// stageScores is the memories' usefulness by status from the project's and
// the machine's stores (memory as a cache): ForStage puts the most useful
// at the status first and leaves out those measured unhelpful there.
func stageScores(proj *tracker.Project) memory.Scores {
	out := memory.Scores{}
	if proj == nil || proj.IssueDir == "" {
		return out
	}
	for _, st := range []memory.Store{memory.ProjectStore(proj.IssueDir), memory.MachineStore()} {
		sc, err := st.Scores(proj.IssueDir)
		if err != nil {
			continue
		}
		for id, u := range sc {
			out[id] = u
		}
	}
	return out
}

// StageMemoryIDs are the memories a stage briefing at the issue's status
// shows, for the briefing record.
func StageMemoryIDs(proj *tracker.Project, issue *tracker.Issue) []string {
	v := memory.Viewer{Agent: tracker.AgentSessionName(issue.Slug)}
	at, _ := memory.ForStage(briefingMemories(proj, v), issue.Status, stageScores(proj), memoryNow(), stageMemoryLimit, stageMemoryChars)
	var ids []string
	for _, m := range at.Shown {
		ids = append(ids, m.ID)
	}
	return ids
}

func sectionList(sections []string) string {
	out := make([]string, len(sections))
	for i, s := range sections {
		out[i] = "## " + s
	}
	return strings.Join(out, ", ")
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}
