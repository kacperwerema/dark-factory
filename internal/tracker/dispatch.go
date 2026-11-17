package tracker

import "github.com/kacperwerema/dark-factory/internal/tokens"

// AgentDispatchPromptTemplate is the constant scaffolding text the issue
// viewer writes into every dispatched agent session's prompt. BuildAgentPrompt
// fills its named {{placeholders}} (slug, system, title, status, body, ...)
// from agentPromptFields; this string is the constant skeleton. Named, not
// positional: a ~20-slot Sprintf once put the system where the retrospective
// command's slug belongs.
//
// Kept in the tracker package so the workflow stats view can attribute its
// token weight without depending on the main package, and so the dispatch
// handler and the stats view share a single source of truth for the prompt
// text.
const AgentDispatchPromptTemplate = `You have been assigned this issue: {{slug}}

## Current status guidance

{{status_guidance}}

{{status_reminder}}

## How to work

Change the issue only with issue-cli, never its .md file (statuses and gates: issue-cli process workflow, process transitions).
{{gate_rules}}

1. issue-cli start {{slug}} claims it and prints the checklist and next step.
2. The brief below is the body (show re-reads it). A brief's cause, cache key, fix or quoted form is a proposal, even from your lead: measure it on the unchanged code or read the parser first; if it differs, record a D-n (step 4) or retitle (update --title).
3. issue-cli check {{slug}} <id> [<id>...] ticks boxes without moving (checklist lists ids); append [--section "Name"] --body "..." writes into the issue; show, checklist, update and append take the slug too.
{{ask_rule}}
5. Boxes done: issue-cli transition {{slug}} --to "<next-status>" --check <id> [<id>...] ticks the last boxes and moves (an owner box refuses the call); a refusal prints it with ids filled in: no --dry-run needed. Repeat 3-5 per status.

This issue is your one task: at its last status, or a gate that waits on the human, stop. Don't run next, start, claim or move another issue, or start other agents. ` + CloseRule + ` (issue-cli close <dup> --duplicate-of "$ISSUE_VIEWER_ISSUE_SLUG")
Ask the user first: a backward transition, creating or deleting issues (except a child issue your lead asks for, or a failure main already has that no issue names).
A bug in the CLI or the engine: issue-cli report-bug "..."; one in this project: issue-cli create.
On a stop (approval, manual check, clarification, blocked tool): issue-cli retrospective {{slug}} --body "Base workflow: ...\nSubsystem workflow for {{system}}: ...\nMissing system-specific instructions: ...\nTooling/workflow friction: ...", then tell the user how the workflow could improve.

## Reading code and docs

` + ReadRule + `

## Issue metadata
  Title: {{title}}
  Status: {{status}}
  Priority: {{priority}}

{{body}}`

// ReadRule points a worker at the short forms before whole files: the code
// map, a change's footprint, and a docs page's outline or one section.
const ReadRule = "Read the short form first: issue-cli codemap (where each concern's code lives) and issue-cli affected \"$ISSUE_VIEWER_ISSUE_SLUG\" (what your branch touches) before opening files; issue-cli ref <page> (a docs page's outline, e.g. ref cli) and ref <page> <heading words> (one section) instead of reading a whole docs page."

// CloseRule is who may close an issue as a duplicate: one sentence, the same
// in the worker brief, issue-cli close's help and its refusal.
const CloseRule = "Who closes: the lead and the human close any issue; a worker closes only duplicates of its own issue (--duplicate-of or --fixed-by its own slug), when its lead asks. Moving any other issue stays the lead's job."

// DispatchGateLines fills the How to work section's stop line (gate_rules)
// and its step 4 (ask_rule): today's with YOLO off, the YOLO rule's with it on (D-5 of
// workflow/yolo-one-rule-in-the-status-guidance-instead-of-clarify-with-the-human).
func DispatchGateLines(wf *WorkflowConfig, yoloOn bool) map[string]string {
	const clarify = `Questions for the owner go only in the design's clarify round; outside it, a question: comment is for a blocker only.`
	if !yoloOn {
		return map[string]string{
			"gate_rules": `At a human-approval gate, tell the user in chat what to approve and where, then end your turn and don't poll: the human messages you once it is approved.`,
			"ask_rule": `4. Stop and ask the user (in chat, or a question: comment) when you need clarification or a manual check. Your own call: issue-cli decide <slug> --provisional --text "..."; the human's: --text "..." --quote "<their words>".
   ` + clarify,
		}
	}
	return map[string]string{
		"gate_rules": `YOLO is on: gates pass without the owner (the YOLO rule above). For what it leaves to the owner, post a question: comment, tell the user in chat and end your turn; never poll for an approval.`,
		"ask_rule": `4. If you are unsure about something, decide it yourself: issue-cli decide <slug> --provisional --text "..."; what you can't, put in a question: comment and go on.
   ` + clarify,
	}
}

// AgentDispatchPromptStaticCost returns the approximate token count of the
// dispatch prompt scaffolding the bot reads on every fresh agent dispatch.
// Issue-specific substitutions (slug, title, body) are NOT included — the
// template's {{placeholders}} contribute negligible tokens. Use this as a
// per-dispatch baseline alongside per-transition costs.
func AgentDispatchPromptStaticCost() int {
	return tokens.Estimate(AgentDispatchPromptTemplate)
}
