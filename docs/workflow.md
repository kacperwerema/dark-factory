---
title: "Workflow"
order: 3
---

## Issue Lifecycle

Every issue follows this flow from left to right on the board:

1. **Idea** — new feature or bug lands here. Just a title and rough description is enough.
2. **In Design** — flesh out the idea: requirements, approach, edge cases.
3. **Backlog** — design is done, ready to be picked up for implementation.
4. **In Progress** — actively being worked on.
5. **Testing** — implementation done, verifying it works correctly.
6. **Documentation** — update docs to reflect the change.
7. **Done** — shipped and documented.

## Optional Statuses

A status can be marked `optional: true` in `workflow.yaml`. Optional statuses remain part of the lifecycle but can be skipped on forward transitions — useful for parking states (e.g. `waiting-for-team-input`) that only apply when a specific condition fires.

```yaml
statuses:
  - name: "in progress"
  - name: "waiting-for-team-input"
    optional: true
    description: "Parked — blocked on another team (skip if not blocked)"
  - name: "testing"
```

With this config, `in progress → testing` is valid (skips the optional status). `in progress → waiting-for-team-input` is also valid (ordinary forward step). Backward transitions into an optional status still require an explicit `transitions:` edge. See [Workflow Overview](Workflow/overview.md) for full semantics.

If a transition into an optional status also has `require_human_approval`, the detail view hides the approval checkbox behind a CTA button so the default (required) next step stays unambiguous. Customise the button text with `cta_label` on the transition:

```yaml
transitions:
  - from: "in progress"
    to: "waiting-for-team-input"
    cta_label: "Block on another team — park until they respond"
    actions:
      - type: "require_human_approval"
        status: "waiting-for-team-input"
```

When `cta_label` is unset, the button falls back to `Divert to <status> — <description>`.

When a CLI transition fails on a missing approval, the error includes a deep link to the corresponding approve button (`/p/<project>/issue/<slug>#approve-<status>`). See [Agent Dispatch — Approval-gate Deep Links](agent-dispatch.md#approval-gate-deep-links) for the URL contract.

## Updating Status

Edit the `status` field in the issue's frontmatter:

```yaml
status: "in progress"
```

The board view updates automatically on page refresh.

## Workflow Prompts

Workflow YAML supports two different prompt concepts:

- `statuses[].prompt`
  Baseline guidance for working in that status. This is shown when an agent starts work in the status or enters it from the previous status.
- `transitions[].actions[].type: inject_prompt`
  Additional transition-specific guidance. This is emitted only for that exact `from -> to` move.

Use `status.prompt` for persistent stage expectations, for example:

```yaml
statuses:
  - name: "in design"
    description: "Being designed and specced out"
    prompt: |
      Review the relevant docs and code before proposing changes.
      Turn requirements into explicit checklists.
      Surface assumptions, open questions, and required human input clearly.
```

Use `inject_prompt` for extra context that should only apply to one transition, for example:

```yaml
transitions:
  - from: "idea"
    to: "in design"
    actions:
      - type: inject_prompt
        prompt: |
          Focus on unresolved scope and design questions from the raw idea.
```

In practice:

- `status.prompt` defines how an agent should behave while in a status
- `inject_prompt` adds extra guidance triggered by a specific transition

For the full end-to-end agent flow from dispatch through approvals, retrospectives, and bug reporting, see [Agent Workflow Flow](agent-workflow-flow.md).

### Lead brief (`leads:`)

A top-level `leads:` block adds to the built-in brief the [project lead](patterns/lead-session.md) is spawned with by `issue-cli lead spawn`:

```yaml
leads:
  project_addendum: |
    - In this repo: designs live in docs/design/, and you review every diff.
```

- Placeholders: `{{project}}` and `{{lead}}` (the lead's tmux name). `issue-cli` commands get `--project` injected.
- A workflow without `leads:` gets the built-in brief alone, so it still has a working lead.
- `project_addendum` goes after the built-in brief, which stays the one source. An old `project` key still replaces the built-in brief; `issue-cli health lint` and `process workflow` warn, because a replaced brief misses every later change to it. Move what the project adds to `project_addendum`.
- `green_branch_minutes` (default 30): how long a branch at its issue's merge status (the last status before done that isn't optional, shipping in the development template) may wait unmerged, counted from the later of its head commit and the move into that status, before `issue-cli lead tick` names it in a `merge:` line, once an hour while it waits.
- `idle_minutes` (default 60; a negative value turns it off): how long the project may start nothing while it has room before `issue-cli lead tick` wakes the lead with an `idle:` line, again each such stretch ([The factory runs 24/7](agent-dispatch.md#the-factory-runs-247)).
- The CLI always appends a footer: role, what the lead may tick, and its handoff note under `sessions/`.
- An old `epic` key still loads but is ignored; `issue-cli process workflow` warns about it.
- `leads:` is project-wide: type, system and per-issue overlays keep it as it is.

## The clarify round

The design step asks the owner once. The bundled `development` template's `in design` prompt has the agent write every question in one `## Clarify` section, in one go:

```markdown
## Clarify

### Q1. Which printer does the label go to?
1. **The shop's thermal printer** (recommended) it is the one at the till (docs/shop.md). Cost: one driver call.
2. **Any system printer** Cost: a print dialog and a layout per printer.
**Answer:** —
```

- Each question is a `### Q<n>.` heading with 2–4 numbered options. Each option says `Cost:`. The first option is the recommendation, marked `(recommended)`, and cites the work.
- The answer line is one of three forms:
  - `—`: open.
  - `<n> · <date> · owner: "<words>" (D-k)`: the owner's answer, which is also decision D-k (how `clarify round`, the owner's words as its quote).
  - `assumed — <reason> (D-k)`: the agent took the answer alone. Without an option number it is the recommendation; `assumed 2 — …` picks option 2. D-k is the provisional decision the assumption recorded (`clarify round, provisional: Q1 <question> → <option>`), so the choice is written once and the critic finds it under `## Decisions`; the owner's later answer records its own D-n that replaces D-k.
- With nothing to ask, the section is one line: `Nothing to ask: <why>.`
- Outside the round, a `question:` comment is for a blocker only. The worker briefing says so.

The owner answers on the issue's Station: the **Clarify** card shows every question as one form, with the recommendation preselected and an optional words box per question. **Answer all** posts them (`POST …/issue/<slug>/clarify`, owner's browser only). Each answer is recorded on its own. If a question changed after the page loaded, it is refused and the owner reloads. Needs you lists one **Clarify** row per issue with questions waiting ("2 questions to answer"), counted in the one waiting-on-you count.

`issue-cli clarify <slug>` prints the round, each answer's state and the format's problems: option count, no recommendation, no `Cost:`, no answer line. `--assume [--reason]` marks the open answers assumed, each with its provisional D-n; it refuses while the round has format problems (a missing answer line aside, which it writes), and a question assumed again unchanged keeps its D-n. The YOLO pass out of design skips the same way and names the problems. `--answer Q2=1 --quote "…" [--how chat]` records an answer the owner gave in chat, relayed by the lead. When an issue leaves a status whose prompt asks for `## Clarify`, `transition` prints the round's problems, or, when the issue's path has a design status, a hint if it has no round, naming the command that writes the one-line round (`issue-cli append <slug> --section Clarify --body "Nothing to ask: <why>."`). A tweak's path has no design status, so leaving idea without a round prints nothing.

Under YOLO the gate that takes the issue out of design marks every open answer `assumed — YOLO: took the recommendation (D-k)` with a provisional D-k each, and the answers wait in that issue's entry of the [review after YOLO](Workflow/yolo.md#assumed-answers). Other sections reuse the format through `tracker.ParseQuestions(body, section)` and `tracker.AppendQuestion` (the red-round and post-mortem questions). Catalog entry `clarify-round` (factory/one-clarify-round-per-design-every-question-at-once-with-options-cost-and-the-recommendation-first-assumed-answers-marked).

## Side-Effects

Statuses support a `side_effects` field — actions that run automatically after entering a status:

- `clear_assignee` — clears the assignee field

```yaml
- name: backlog
  side_effects: [clear_assignee]
```

This is used so design agents are unassigned when an issue moves to backlog, before a different agent picks it up for implementation.

## Validation Rules

Each `transitions[].actions[]` entry of `type: validate` runs one rule against the issue. There are two flavors of rule encoding:

- **Legacy colon-string** — the rule and (optional) argument live in a single `rule:` string, e.g. `rule: "section_checkboxes_checked: Design"`. Used by the original 10 rules: `body_not_empty`, `has_checkboxes`, `section_has_checkboxes`, `has_assignee`, `all_checkboxes_checked`, `section_checkboxes_checked`, `has_test_plan`, `has_comment_prefix`, `approved_for` / `human_approval`, and by `criteria_checkboxes` and `criteria_proven`. The name ends at the first colon: `approved_for:done` is `approved_for: done`.
- **Structured action params** — the rule name in `rule:` plus companion fields on the same action (e.g. `field`, `values`, `pattern`, `section`, `min`, `max`, `command`, `ref_key`, `linked_status`, `hint`). All new validators use this form.

Both flavors can mix freely in the same workflow. New validators should use the structured form for clarity; existing legacy rules keep working unchanged.

A `hint:` on the action replaces the failure's built-in fix, templated the same way (`{{slug}}`, `{{number}}`, `{{repo}}`, `{{system}}`, `{{department}}`), on the structured rules and on the original legacy rules (`body_not_empty`, `has_checkboxes`, `section_has_checkboxes`, `has_assignee`, `all_checkboxes_checked`, `section_checkboxes_checked`, `has_test_plan`, `has_comment_prefix`) and the shelf's `has_output` and `outputs_approved`: the error keeps what is wrong (`no comment starting with "docs:"`, the open boxes) and the hint takes the place of the fix, in the transition and in `transition --dry-run`. The other rules keep their own text, and lint check `hints` warns on a hint set on one of them.

### Frontmatter validators

```yaml
- type: validate
  rule: field_in
  field: priority
  values: [low, medium, high, critical]
  hint: "set with: issue-cli set-meta {{slug}} --key priority --value high"
```

- `field_present` — frontmatter `action.field` exists, regardless of value.
- `field_not_empty` — frontmatter `action.field` exists and is non-blank.
- `field_in` — frontmatter `action.field` value is one of `action.values`.
- `field_matches` — frontmatter `action.field` value matches the Go RE2 regex `action.pattern` (no backreferences or lookarounds).
- `has_label` — issue labels contain the name in `action.field`.
- `has_any_label` — issue has at least one label.

### Linkage validators

```yaml
# Block shipping until the parent issue is done.
- type: validate
  rule: linked_issue_in_status
  ref_key: parent
  linked_status: done
```

- `has_pr_url` — frontmatter `pr` is a github pull request URL (`https://github.com/<org>/<repo>/pull/<N>`).
- `linked_issue_in_status` — the issue referenced by `action.ref_key` (a frontmatter key whose value is another issue's slug) has the status `action.linked_status`.
- `children_closed` — every child of the issue (the issues naming it in `parent:`) is closed: in `values:` when set, else by its own workflow (`tracker.IsClosedStatus`: the workflow's end, anything listed after it, and parked). An issue with no children passes. The `epic` type uses it on in progress → done.

### Body-structure validators

```yaml
- type: validate
  rule: section_min_length
  section: Design
  min: 200
- type: validate
  rule: no_todo_markers
```

- `has_section` — body contains `## <action.section>`.
- `section_min_length` — section `## <action.section>` has at least `action.min` non-whitespace chars.
- `section_min_links` — section `## <action.section>` has at least `action.min` distinct http(s) links (the same link with a trailing slash or full stop counts once). The marketing `research` edges use it with `min: 5`: research is measured by its sources, not its length.
- `section_max_length` — section `## <action.section>` has at most `action.max` non-whitespace chars (a missing section passes; pair with `has_section` if presence matters).
- `no_todo_markers` — body contains no whole-word `TODO` or `FIXME` (case-sensitive).

### Checkbox validators

- `has_checkboxes` — body contains at least one `- [ ]`/`- [x]` checkbox anywhere.
- `section_has_checkboxes: <Title>` — the `## <Title>` section exists and contains at least one checkbox. **Existence only** — the boxes need not be checked here; they are typically verified at a later transition. This is how the `Acceptance Criteria` section gates `in design → backlog`: it must be present and non-empty, but its boxes are checked during testing.
- `all_checkboxes_checked` — every checkbox in the body is checked.
- `section_checkboxes_checked: <Title>` — the `## <Title>` section must exist with at least one checkbox, and **every** checkbox in it must be checked. A missing or checkbox-less section **fails** (it does not pass vacuously) — this stops an issue imported without a templated section from transitioning with none of that section's checklist content. In the normal flow each gated section is appended by the prior transition's `append_section`, so it always exists when the gate runs.

- `criteria_checkboxes: <Title>` — every list item in `## <Title>` is a checkbox, and none still holds the text the workflow's `append_section` wrote there. Used on `Acceptance Criteria` at the design gates.
- `criteria_proven[: since YYYY-MM-DD]` — every `Acceptance Criteria` box has a proof link that passes its check. Issues created before the date, and issues without criteria, pass. See [Proof Links](Workflow/proof.md).
- `has_output[: <kind>]` — the outputs shelf holds an output for the issue (`issue-cli output add --issue <slug>`), of that kind when one is named, that isn't rejected and whose file is still there. The failure prints the `output add` command, and names a rejected or gone output. The `report` type runs `has_output: report` at `in progress → human-testing`; lint check `reports` warns on an issue past the gate whose output has since gone.
- `outputs_approved[: <kind>]` — the shelf holds an output for the issue (of that kind, when named) that the owner approved or that is published, and none still in review. `output revise` puts an output back in review, so the gate waits for the newest version's approval, not an older one's. Rejected outputs don't count. The marketing template runs it on `owner review → publish` (`outputs_approved: report` on the foundation, market-size, keyword-research and competitor-scan paths), so the issue's publish approval and the shelf's approval agree.
- `handoff_answered: <department>` — a handoff to that department filed for the issue (`issue-cli handoff <department> --for <slug>`) is done; an open, declined or parked one doesn't answer it. With `paths:` (globs) on the action it holds only when the work branch's own changes match one; a branch it can't read holds too. YOLO doesn't pass it. See [The handoff gate](departments.md#the-handoff-gate).
- `commits_reference_issue` — one of the work branch's own commits references the issue: its subject names the slug (`chore(merge): main into <slug>`) or its message has a `Refs: <slug>` line, in any paragraph. Commits merged in from main don't count; after the fast-forward the walk starts at the issue's dispatch, so it passes at `done` too. The failure shows the commit to add (a merge of main or an empty commit with `Refs:`), never a reword, since `Proves:` trailers pin their shas. This project runs it at `shipping → done`, and the shipping guidance runs that transition's `--dry-run` before the fast-forward.

A box that doesn't fit the change (no page for ux-test, a repro that is a go test) is ticked with a named N/A form, never free words: `issue-cli check <slug> <id> --na "no page changes"`. The forms and refusals are in [the CLI doc](CLI/overview.md#na-boxes-check---na); the testing prompt lists them (catalog entry `testing-na-forms`), and lint check `na-boxes` warns on a ticked N/A out of form or on the owner's box. Docs-only issues use the `docs` type, which has no Test Plan ([Types of Work](Workflow/types.md#the-docs-type)). Research and report-only issues use the `report` type: no build boxes and no commit ([The `report` type](Workflow/types.md#the-report-type)).

`process transitions` annotates these in its output: `section_checkboxes_checked` rows read "(gates this transition)" and `section_has_checkboxes` rows read "(existence only — contents verified later)".

### Shell / external

`command_succeeds` runs a shell command and passes when the exit code is 0. It is **opt-in** — set `allow_shell: true` at the top of `workflow.yaml` to enable it; otherwise every `command_succeeds` action fails with a fix-it hint.

```yaml
allow_shell: true

transitions:
  - from: shipping
    to: done
    actions:
      - type: validate
        rule: command_succeeds
        command: "gh pr view {{number}} --json state -q .state | grep -q MERGED"
        timeout_seconds: 15
        hint: "land or close PR #{{number}} before marking done"
```

- The command is run via `/bin/sh -c <command>` with `text/template` substitution of `{{slug}}`, `{{number}}`, `{{repo}}`, `{{system}}` from the issue's frontmatter.
- Working directory is the project's issue root.
- Environment is scrubbed to `PATH`, `HOME`, `GH_TOKEN` only.
- Timeout: `timeout_seconds` (default 10s). On non-zero exit or timeout, captured stdout/stderr (truncated to 400 chars) is included in the failure message.

### Files

`file_exists` passes when `action.path`, relative to the project root (`workdir:`, else the issues folder's parent), is a file with something besides whitespace in it. It runs no shell, so it needs no `allow_shell`. Use it when one piece of work needs another's result first. The marketing template uses it to keep channel briefs from moving to draft before the brand foundation is written ([Brand foundation and research mode](departments.md#brand-foundation-and-research-mode)).

```yaml
- type: validate
  rule: file_exists
  path: "departments/{{department}}/brand.md"
  hint: "the brand foundation comes first: issue-cli create --department {{department}} --type foundation --title \"Brand foundation: <topic>\""
```

- `path` is templated with `{{slug}}`, `{{number}}`, `{{repo}}`, `{{system}}` and `{{department}}`, which is the key of the department whose workflow the issue walks (`marketing`, `growth-team`). A `{{department}}` path on a Product issue fails and says the issue belongs to no department.
- A path outside the project root, either absolute or one that climbs out with `..`, is a config error.
- `issue-cli health lint` (the `departments` check) warns about a file a department's base transitions gate on that isn't written yet, and gives the rule's hint as the fix. Gates on a type's own edges aren't listed, because those check files the type's issues write themselves.

### Reel legal check

`reel_legal_check` holds a reel whose script breaks the legal floor. It reads the issue's `## <section>` (`Script` when `section:` is unset), the `## Legal check` declarations (`- key: value` lines: `music`, `music_licence`, `ai_content`, `ai_label`, `face`, `persona`, `consent`, `claim: <phrase> = <authorisation>`, `disclaimer`, `storyboard`), every string of the storyboard JSON `storyboard:` names when that file is there, and the phrase lists in `path` (`departments/{{department}}/reel-legal.yaml` when unset, relative to the project root). It runs no shell. The refusal lists each finding with its rule and fix; `issue-cli reel-legal [<slug>]` prints the same without moving. A missing or empty rules file holds the reel. The video template's reel type gates script → render and render → review on it ([Reels](departments.md#reels)).

```yaml
- type: validate
  rule: reel_legal_check
  section: Script
  path: "departments/{{department}}/reel-legal.yaml"
```

### Failure hints

Every validator returns a failure message of the form `<problem> — <hint>`, where `<hint>` is a concrete `issue-cli` command bots can run to fix the gate. Set `action.hint` on any structured validator to override the default hint with project-specific guidance (the override is templated with the same `{{slug}}/{{number}}/{{repo}}/{{system}}` vars as `command_succeeds`, plus `{{department}}`).

## Scoring

The `scoring` block in `workflow.yaml` turns on a score that ranks issues by urgency + importance + staleness. Scores are computed server-side on every page load from frontmatter — no cache, no background job — and surface in three places:

- Board cards: a small `⚡ N` badge in the corner, color-graded (green → yellow → orange → red)
- List view: a score chip on each row and a `Sort: score ↓` toggle at the top
- Detail view: a sidebar breakdown showing each component's contribution

The block is opt-in; omit it or set `enabled: false` to hide all scoring UI.

### Formula

```
score = priority_weight
      + min(overdue_cap, max(0, 30 - days_until_due) * urgency_weight)
      + age_days * staleness_weight
      + sum(label_weights for each label on the issue)
      + score_boost          # optional manual override
```

Missing fields contribute 0 — an issue with no `priority`, no `due`, and no `created` simply scores 0 (unless `score_boost` is set).

### Frontmatter fields that participate

| Field         | Role                                                                  |
|:--------------|:----------------------------------------------------------------------|
| `priority`    | Looked up in `scoring.formula.priority`. Frontmatter values are normalized to lowercase before lookup, so map keys should also be lowercase (`p0`, `critical`) — uppercase keys still match via a case-insensitive fallback but lowercase is the canonical form |
| `due`         | `YYYY-MM-DD` or RFC3339 date. Triggers urgency under a 30-day horizon |
| `created`     | Accumulates staleness at `staleness_weight` per day since that date    |
| `labels`      | Summed against `scoring.formula.labels`; unlisted labels add 0         |
| `score_boost` | Integer added as a separate component. Negative values work            |

### Example config

```yaml
scoring:
  enabled: true
  formula:
    priority:
      critical: 40
      high: 20
      medium: 10
      low: 0
    due_date:
      urgency_weight: 2     # points per day under the 30-day horizon
      overdue_cap: 60       # max from the due-date term (keeps ancient overdue from dominating)
    age:
      staleness_weight: 0.1
    labels:
      bug: 5
      blocker: 25
      enhancement: 0
  default_sort: score_desc   # initial sort on list + board; overrideable via ?sort=
```

### Sorting

With scoring enabled:

- `default_sort: score_desc` makes the list and board sort by score descending on first load.
- Append `?sort=score` to any list or board URL to force score ordering regardless of the default.
- Drop the query param (or use `/?sort=` cleared) to return to the original order (most-recently-modified first on list, insertion order per column on board).

Existing URL filters (`?status=`, `?system=`, `?label=`, etc.) compose with `?sort=`.

## Editing a station from the hall

The owner can change a station of the Product line from the viewer: the Product hall's station page has **Edit**, which edits that status's description and prompt, the actions on the base transitions into and out of it, and the approval gate on each way in. It previews the diff, writes only the edited entries (the rest of the file, comments included, stays as written) and commits the file on main with the owner's reason quoted. Overlays, wildcard edges and statuses themselves are edited by hand. See [UI: Editing a station](UI/overview.md#editing-a-station).

## YOLO Mode

For a window the owner chooses (until 08:00, 4 hours, or until turned off), `require_human_approval` gates on forward steps pass without them once nothing else is missing. Each pass leaves an `approval: "YOLO"` comment and waits in the "after the fact" queue for the owner to Agree or send the issue back. Every other validator, CI, tests, review, retros and permission prompts stay on; CI red for more than 30 minutes turns it off. A gate marked `owner_only: true` (a step that spends money or publishes outside the machine, like the marketing template's `owner review → publish`) is never passed: it waits for the owner. Turn it on from the Workshop Board's company level or with `issue-cli yolo on`. See [YOLO Mode](Workflow/yolo.md).

## Rules

Standing rules come in two layers, and every lead and worker is briefed with both. Source: factory/two-agents-and-a-short-lived-lead-routing-on-benchmarks-cross-model-critique-handover-on-limits-memory-as-the-one-source-a-nightly-lead-flush#D-8.

| Layer | Where | Who writes it | Reaches |
|---|---|---|---|
| On every project | `~/.config/ai-workflow-engine/rules.md` (`$XDG_CONFIG_HOME` honoured) | the owner, by hand; no command or agent writes it | every project's leads and workers |
| On this project | `rules:` in Product's workflow.yaml | whoever changes the workflow | that project's leads and workers, its departments' too |

The machine file is markdown. Every `- ` bullet is one rule, an indented line continues it, and a leading `[key]` names it so a project rule can point at it. Headings, comments and blank lines are skipped. Any other line reaches nobody, so lint names it.

```markdown
# Rules on this machine
- [no-install] Never run make install from a worker.
- Keep a commit to one topic.
```

A project adds its own:

```yaml
rules:
  - text: "Every release note names the issue."
  - id: release-worktree
    text: "make install runs only from the release worktree."
    narrows: no-install     # the machine rule's [key]
```

A rule only leads need on one project is a lead-scope memory instead ([Lead rules](memory.md#lead-rules)).

`rules:` is project-wide: a department's own workflow file takes it from Product's, and lint warns when one sets it. The rest of workflow.yaml (prompts, `prompt_append`) stays project text; `rules:` is the part lint can compare with the machine's.

Don't name a private project in `rules.md`: it reaches every project's workers (Settled rule "Private projects stay private").

**In a briefing.** A worker's `## Rules` section and each lead's brief show `On every project` and `On this project` apart. `## Memory` does the same with pins: `Pinned on every project:` for machine memories, `Pinned on this project:` for the rest, one budget between them. With no rules in either layer a worker gets no section; a lead still gets one line on where each layer lives. Each layer carries at most 4,000 characters, whole rules only, then `+N more: issue-cli rules`. `issue-cli start` prints one line with the counts, for a session no briefing reached.

**Narrowing.** A project rule can tighten a machine rule, or apply it to a case. It says so with `narrows: <key>`, prints as `(narrows [key])`, and the machine rule stays in the briefing beside it: nothing a project writes removes one. An agent who sees two rules disagree follows the machine's and asks.

**Lint.** `issue-cli health lint --check rules` names:

- a project rule that contradicts a machine one without `narrows:`: it takes a machine rule's key, or one of the two forbids (never, not, no, don't, avoid) while the other doesn't, with at least two content words in common covering half or more of the shorter rule. Wording is a heuristic: when it matches a real narrowing, declare it. To narrow a rule with no `[key]`, the owner gives it one first;
- `narrows:` naming no machine rule, a project rule with no text, a repeated key or id;
- a `rules.md` line that isn't a rule, a file that can't be read, and a layer over the briefing's cap.

A finding names a machine rule by key and line, never by its text. Pinned memories aren't compared.

**Reading them.** `issue-cli rules [--json]` lists both layers. It only reads.

**Private projects.** A private project reads the machine's rules like any other, and nothing of it goes the other way: there is no write command for `rules.md`, `memory add --scope machine` is refused, and so are `--updates`, `--supersedes` and `memory edit --text` on a machine memory (`s-…`).

## Decision ledger

Decisions are the `D-n` lines under an issue's `## Decisions` ([format](issue-format.md#decisions)). Each line may end with an attribute group that says whom it reaches and how strong it is; the line stays the one record, and its ref (`<slug>#D-n`) never changes.

    - **D-4** · 2026-10-31 · chat: Agents merge into local main, "only main" {scope: project; hard; paths: cmd/**}

- **Scope** (`scope:`): `issue` (the default, never written: the decision's own issue, every issue below it in the `parent:` chain, and every issue citing it), `project`, `system:<S>`, `department:<key>` (box keys: `product` or a department's key), `type:<t>`.
- **Hard** (`hard`): the owner's rule. It needs the owner's words on the line (a quote, not provisional or YOLO).
- **At** (`at:`): the statuses where it matters; empty is all.
- **Paths** (`paths:`): globs (`**` any folders) a hard decision guards. Only on hard decisions.
- **Expires** (`expires:`): its last day in force.
- **Ended** (`ended:`): the day an expired decision was let go (`decide --let-go`); written only by let go.

**Expiry.** A decision is in force through its `expires:` day and expired from the next: it leaves `decisions --in-force` and the briefings, `citations_current` refuses citing it, and `issue-cli decisions --state expired` lists it (`--in-force` adds a line counting the expired ones that would reach the issue). The state depends on the day, so the readers compute it (`DecisionEntry.StateOn`); nothing is written when the day passes.

- `issue-cli decide <slug> --renew D-n --expires YYYY-MM-DD [--quote "…"]` gives it a new last day (today or later), before or after it passed: a `renewed` event.
- `issue-cli decide <slug> --let-go D-n [--quote "…"]` closes out an expired one: `ended: <today>` in its group, an `expired` event. It stays out of force. Let go is final: renewing one refuses and points at `--replaces`; letting go of one not yet expired refuses (end it early with `--amend D-n --expires <today>` or replace it).
- On a hard decision both are the owner's acts, like every change to it: a worker is refused, the lead relays the owner's `--quote` (the `decision:` comment), the owner's own shell may; the owner's `--quote` (shell) or words (browser) go in a `decision: <ref> renewed until <date>, "<words>" — the owner` comment, so the line's quote stays as it is. `--amend --expires` on an expired decision points at renew and let go instead.
- **Waiting on you.** An expired hard decision nobody renewed or let go is one Needs you row ("Decision expired", in To answer) with **Renew** (a date, default today + 90 days) and **Let go**, both with an optional words box: `POST /p/<project>/decisions/<slug>/<D-n>/renew {expires, text}` and `…/let-go {text}`, the owner's browser only (O routes in [Security](API/security.md)). The row reads only the issue files with an `expires:` or a `(replaces …)` (`tracker.ExpiredHard`).

**In force.** `issue-cli decisions --in-force <slug>` lists what reaches an issue at its status: not replaced, deferred or expired, `at:` naming the status (or empty), and a scope that reaches it, hard first (`tracker.InForce`). The existing lines are all scope `issue`, soft.

**In front of the agent.** The same list reaches the agent where it already looks, as "Decisions in force": `== Decisions in force ==` in `issue-cli start` (at the status after start's move, before `== Memories ==`) and `issue-cli show`, `## Decisions in force` in the worker briefing (before `## Memory`, so the dispatch-prompt preview shows it too), and a "Decisions in force" item in the lead briefs: the CEO gets project-scoped decisions, a department lead project and `department:<its key>` ones, `at:` not filtered (`issue-cli decisions --lead ceo|<department>` lists them). Each line is `- <ref> · hard|soft · <scope> · <text on one line, cut at 100>` with the owner's quote when it is 60 characters or shorter. Hard decisions are never folded; soft ones fill what is left of the cap (`decisions: brief_cap:` in workflow.yaml, 12 when unset), and the rest is one line, `and N more: issue-cli decisions --in-force <slug>`. A last line says how to disagree (`decision challenge`, below). With nothing in force the block is one line naming the command, never left out. The worker briefing's block is shorter: every hard decision and the soft ones that are the issue's own (recorded on it, on an issue above it, or cited by it), grouped under the issue that records them (`- <slug>`, `- this issue` for its own) as `  - D-n · <text, cut at 76>` (`D-n (soft)` for a soft one; no scope, no quote), then `N soft ones: issue-cli decisions --in-force <slug>` for the rest; the cap doesn't apply there. The fresh critic's brief lists every decision in force by ref, strength and scope with the path of the issue file holding it, never its text (the critic reads the files).

    == Decisions in force ==
    - ops/rules#D-1 · hard · project · Agents merge into local main — "only main"
    - ops/rules#D-2 · soft · system:Workflow · Gates name the fix command
    Disagree with one? Challenge it: issue-cli decision challenge <ref> --why "…" (your issue waits for the owner's answer); never work against a hard one.

**Who changes what.** Agents record soft decisions and scope them (`decide --scope`, `decide --amend D-n --scope`), a worker on its own issue and never wider than a system or a type. Every change to a hard decision is the owner's: `--hard`, any `--amend` of a hard line, `--replaces` of a hard decision. A worker is refused; the lead relays the owner's words with `--quote`, and the write posts a `decision:` comment on the issue (in the YOLO review queue while YOLO is on); in the viewer only the owner's browser can tick Hard on Confirm or press Make hard. A decision replacing a hard one stays hard with the same scope and paths unless the owner says otherwise.

**What may change in place.** Only the attribute group (`decide --amend`), and the owner's words through `decide --confirm`. Date, how, text, quote, also-considered and replaces stay byte for byte; to change what was decided, record a new decision with `--replaces`.

**Citing decisions in design.** Two rules check a design against the ledger, on the edges out of `in design` (the `decision-ledger` catalog entry; both take `since <YYYY-MM-DD>`, and an issue created before it passes):

```yaml
- type: "validate"
  rule: "decisions_cited: since 2026-11-01"
- type: "validate"
  rule: "citations_current: since 2026-11-01"
```

- `decisions_cited`: `## Design` names every **hard** decision in force for the issue at its current status (the Decisions in force block, `decisions --in-force <slug>`), other than the issue's own: by its ref (`Follows sim/rules#D-4: …`), or on a line `n/a <ref>[, <ref>…]: <why>` (a list item or a plain line; several refs share the one reason, and after the first a bare `#D-n` takes the slug of the ref before it: `n/a sim/rules#D-4, #D-5, sim/epic#D-1: docs only`). Several n/a sentences may share a line, each with its own reason: `n/a sim/rules#D-4: docs only. n/a sim/epic#D-1: no schema.` (a `.` or `;` ends one). A bare `n/a <ref>` refuses, and so does a line that starts with `n/a` but doesn't parse (`n/a sim/rules#D-4 and #D-5: …`): it sets nothing aside and cites nothing, and the refusal quotes it. In `## Design` a shorthand `#D-n` on the same line as a full ref is that issue's D-n (`sim/rules#D-4 and #D-5`; with two full refs on the line, the nearest one before it); alone on its line it names nothing and the decision stays uncited. A comma list after a ref needs no `#` on the later ones: `sim/rules#D-4,D-5,D-6` and `sim/rules#D-4, #D-5` each cite every ref in the list, in a cited line or an n/a one. A refusal lists these forms. `decisions_respected` reads it the same way; `citations_current` checks full refs only, so a replaced decision is caught only when written in full. Soft decisions never hold it. The Design checklist's "Decisions in force cited" box is the step.
- `citations_current`: no ref cited in the body outside its own `## Decisions` is replaced or expired. The refusal names the replacer and its text; cite it instead, or write the history as `(replaces <ref>)`, which is no citation.
- Each refusal lists the refs with their scope and text, the `issue-cli append … --section Design` lines that fix it, and how to challenge a decision (`decision challenge`, below).
- Under YOLO the holds pass and wait in the review after the fact as `yolo-hold:` comments ([YOLO Mode](Workflow/yolo.md)).
- `issue-cli health lint` (check `workflow`) warns on a validate rule the build doesn't know and on a bad `since` date. The rules build the project's decision index only when they run, so a workflow without them is unchanged.
- `issue-cli health lint --check decisions` re-checks the lines already on the board: a group that names a decision key but doesn't parse or a scope naming no system, department or type; `hard` without the owner's words; `paths:` matching no file; `at:` naming no status ([the lint family](workbench-files.md)).
- The Settled rules' sources (docs/departments.md) are scoped records: `internal/tracker/testdata/settled-rules.tsv` gives each its scope and strength, `TestSettledRulesScoped` checks the board against it, and the lead writes a new row to the board with `decide --amend` (scope, and `--hard --quote` with the quote already on the line). The first 44 went on with the guided check script `settled-rules-scopes` of factory/scope-the-settled-rules-as-hard-decisions-and-lint-check-decisions, which `issue-cli check-script` still finds in history.

**Challenges.** An agent that thinks a decision is wrong says so on the record, and only its own issue waits:

    issue-cli decision challenge sim/rules#D-2 --why "PRs give the owner a review step" [--propose "merge via PRs"] [--for <slug>]

- The ref must exist and not be replaced (challenge the replacer); `--why` is required; a second open challenge on the same ref from the same issue is refused. A worker challenges for its own issue (`--for` defaults to it and may name no other); a lead or the owner's shell names `--for`.
- It writes a `challenged` event (ref, the challenger's `issue`, `why`, `propose`) and a `challenge: <ref> — <why> · propose: … (for <issue>, by <who>)` comment on the challenger's issue and on the decision's. The log is the state: a challenge is open while its `challenged` event has no later `resolved` one for the same ref and issue. Comments are the trail; typing one opens or closes nothing.
- The lead sees each new challenge once, after its next issue-cli call (`== Decision challenges ==`, with the two answers to relay); `issue-cli decision challenges [--json]` lists the open ones. The owner sees one Needs you row per open challenge (To answer): the decision's text, why, the proposal, and "Also on it, not held": the other open issues that cite it and those whose `work/<slug>` branch's merge-base diff touches its `paths:`.
- The owner answers. **Keep:** `issue-cli decision resolve <ref> --keep [--for <slug>] [--quote "…"]`, the row's **Keep it** (`POST …/decisions/challenge/resolve`, the owner's signed-in browser only), or confirming a challenged provisional decision (`decide --confirm`, the Decisions log's Confirm). **Change:** `decide <slug> --replaces <ref> --text "…"`. Each closes every open challenge on the ref (`resolved` events, `new: kept|replaced`, `by_ref`) and posts `challenge-resolved: <ref> kept|replaced by <ref>[, "<words>"] — <who>` on each challenger's issue. Answering is the owner's act, soft or hard: a worker is refused (also for `--replaces` and `--confirm` of a challenged decision), a lead relays the owner's `--quote` (a `decision:` comment on the decision's issue, in the YOLO review while YOLO is on).
- The rule `decision_unchallenged[: since <date>]` holds the issue while a challenge it made is open, at the gates that have it (the `development` template: out of `in design` and `in progress → testing`). It holds only the challenger: an issue that cites the decision, or whose branch touches its paths, goes on (the diff audit judges those by citation). The refusal names the ref, why and the answer commands. A challenge never clears a gate. Under YOLO the hold passes as a `yolo-hold: decision_unchallenged <ref> from <status>: challenged by this issue: <why>` comment, listed by `issue-cli yolo queue`, and the challenge stays open.

**The diff audit.** `decisions_respected[: since <YYYY-MM-DD>]` checks the code, at `in progress → testing` and every edge into `shipping` (the `development` template's type overlays too). It reads the work branch's own changes: `git diff --name-only <from>...<head>`, where the head is `work/<slug>` (else the sidecar's last recorded branch head) and `<from>` is the merge-base of the head and the current main branch, so a `chore(merge)` of main adds nothing and main moving on changes nothing (once the head is in main, the issue's recorded base). For every hard decision in the project that has `paths:` and is not replaced, deferred or expired, **whatever its scope or `at:`** (scope decides who is briefed, not which branches the guard skips), a changed file its globs match makes the issue cite the ref somewhere in its body outside its own `## Decisions` (an `n/a <ref>` line doesn't count; the issue's own decisions do), with no open challenge of its own on it (a `challenged` event for the ref and this issue in the audit log with no later `resolved`; another issue's challenge never holds it). Soft decisions never hold it. A replaced decision is judged through its replacement, which inherits `paths:`.

    decisions_respected: the branch changes files 1 hard decision guards:
      ops/rules#D-4: Agents merge into local main
        files: cmd/issue-cli/main.go  (paths: cmd/**)
        not cited (an n/a line doesn't count for a decision the branch touches)
    Cite each, saying how the change follows it:
      issue-cli append <slug> --section Design --body "Follows ops/rules#D-4: …"
    or, if the change goes against it, undo that part, or challenge it and wait for the owner.

- A citation is the agent's statement that the change follows the decision; the gate only counts paths (no model reads the diff), and the fresh critic's brief lists the touched decisions ("Hard decisions this branch touches": ref, the file holding it, the matching files) so a reader checks the claim.
- `issue-cli decisions --touched <slug> [--json]` prints the same audit: the files, each touched decision and cited / NOT cited / challenged. It only reads.
- No recorded base (the issue wasn't cut by a dispatch) or no branch: the rule passes with a `⚠ decisions_respected: … the diff audit didn't run` line on the transition and its dry run. A git error refuses, naming it.
- Under YOLO a hold passes and leaves `yolo-hold: decisions_respected <ref> from <status>: touched (<files>), not cited` for the review after the fact.

**The audit log.** `<issues>/.decisions/log.jsonl`, one JSON event per line (`recorded`, `amended`, `confirmed`, `replaced`, `challenged`, `resolved`, `renewed`, `expired`: let go), appended under a lock and never rewritten. Each write logs `pending` while it holds the issue's lock, then `done`, or `failed` when the write did not land; readers count only `done`. An event names the ref, who (`by`, and `role`: `owner-shell`, `owner-browser`, `lead-relay`, `agent`), the old and new attribute group and the relay comment's id; never a decision's text or quote. A real briefing that carried at least one decision appends one `injected` event (op `done`): `briefing` (`start`, `dispatch` when the agent was launched, `reprompt` when the paste arrived, `lead`, `department-lead`), `issue` ("" for a lead) and `refs`, every ref shown or folded. Reads (`decisions`, `--in-force`, `--lead`, `show`, `dispatch-prompt`, `critique --brief`) never write it.

**Holds.** A real `transition` that a ledger rule (`decisions_cited`, `citations_current`) refuses, or that YOLO lets through over one, appends one `held` event per rule and ref (op `done`): `rule`, `issue`, `from`, `to`, `passed` (true under YOLO). A dry run never writes it. Challenges are `challenged` and `resolved` events (ref, the challenger's `issue`).

**Reading the audit log.** `issue-cli decisions log [--ref <slug>#D-n] [--issue <slug>] [--since YYYY-MM-DD] [--json]` prints the `done` events in time order, one line each (`2026-10-31 14:02 · amended · econ/gold#D-2 · {…} → {…} · lead-x (lead-relay)`). `--ref` keeps that decision's events and the briefings that carried it; `--issue` the issue's own decisions and the briefings, holds and challenges naming it; `--since` is inclusive, local time. `decide --amend` and a refused transition print the command for their ref. The Stats page's **Decisions** card counts the same ledger (in force by scope, hard and soft, provisional, open challenges, gate holds by decision, the briefings that carried the block, in the page's range), and the `decisions` health family snapshots it ([Workbench Files](workbench-files.md#the-decisions-family)). "Provisional" there is a ledger state; the one waiting-on-you count stays `needsYouRows`.

**The drift sweep.** The gates catch drift only when an issue crosses one. `issue-cli decisions drift [--record] [--since <commit>] [--json]` lists what moved away from the decisions between gates, as markdown (a `## <kind> (n)` heading per kind, each finding with its fix; "No drift." when clean):

- **Citations:** an open issue whose body cites a replaced or expired ref (the `citations_current` rules: its own `## Decisions` and `(replaces …)` don't count), naming the replacer or the expiry day.
- **Paths:** a non-merge commit on main since the last sweep whose files match an active hard decision's `paths:`, made on or after the decision's day, whose issue doesn't cite it. A commit's issue is the slug a `Refs: <slug>` line names, else the `work/<slug>` branch that made it: on that branch's first-parent line (a commit merged in from main never is) and after the base its dispatch recorded (else after the issue's start). No issue is drift too ("no issue": a direct commit on main). Paths only, as the diff audit.
- **Docs:** a line under a "Settled rules" heading in any docs file that names a replaced or expired ref, as `docs/departments.md:23`. A bare `#D-2` after `slug#D-1` on the line is `slug#D-2`.
- **Provisional:** an active decision marked YOLO or provisional, more than 14 calendar days old (its date against today's local date, never hours: one exactly 14 days old is not listed), whose scope is wider than `issue` or that has `paths:`; the rest are one count line (`decisions --state provisional` lists them).

It only reads. `--record` also writes `<issues>/.decisions/drift.json` (`at`, main's sha, who) under a lock, so the next sweep reads main after it; without a mark, or with one no longer in main's history, it reads main from local midnight 14 calendar days back and says so. `--since <commit>` reads a window you choose for one run. The `decision-drift` job runs it on a schedule (`brief: builtin:decision-drift` on the Product entry's or a department's `jobs:`, [Recurring jobs](departments.md#recurring-jobs)): each run sweeps with `--record` and, when there is drift, files one issue "Decision drift (<period>)" with the report for the owner and the lead; it changes no decision, doc or other issue. Its standing start is off until the owner turns it on with their words (Settled rule "Nothing goes out without the owner's stamp", factory/department-recurring-jobs#D-3). No gate and no Needs you row: the filed issue is the place. Source: factory/decision-drift-sweep-decisions-drift-and-the-decision-drift-job.

## Owner steps

Some steps only the owner can do by hand: reinstall a phone build whose free signing lasts 7 days, request indexing in Search Console (its API is read-only), submit to a directory that answers scripts with 403, upload a logo. An agent records each one as an **owner step** instead of leaving it in a handoff note or a report's "Owner actions", so the owner's Waiting on you list carries the exact text and the date. Source: factory/a-dated-owner-step-an-agent-records-a-hand-step-with-a-due-date-and-the-exact-text-and-waiting-on-you-shows-it-until-the-owner-ticks-it.

```bash
issue-cli owner-step add --title "Reinstall the iPhone build" --due +7d --repeat 7d \
  --body "Xcode → Product → Run on the phone (free signing lasts 7 days)"
issue-cli owner-step add --title "Request indexing of the new articles" --due tomorrow --body-file urls.txt
issue-cli owner-step list            # open steps, overdue first; --all, --json
issue-cli owner-step done st-3fa9c1 --quote "reinstalled it"   # a lead relaying the owner
issue-cli owner-step drop st-3fa9c1 --why "the paid account signs for a year"
```

- **Where it shows.** One row per open step in Needs you's **Your steps** section, so it counts in every "waiting on you" number (Settled rule "One waiting-on-you count"), and on the Status page with the other waiting rows. The row has the title, the due line (`due 2026-11-22`, `due today`, or red `overdue: was due 2026-11-22 (3 days ago)`), the issue it is for, who added it and the body as written, and **Done**.
- **Days.** `--due` takes `YYYY-MM-DD`, `today`, `tomorrow`, `+Nd` or `+Nw`, never a day before today. A step is overdue from the day after its due day, in calendar days of local time.
- **Repeat.** `--repeat 7d` (or `2w`): the tick closes the step and it opens again on the tick's day plus the interval, due that day. Counted from the tick, so a late reinstall moves the next one. Nothing runs on a timer: the list is read from the log.
- **The body** is kept byte for byte, 4000 bytes at most, and refused when it looks like it holds a secret (the memory check): steps are on the board. Name where the owner finds a secret instead.
- **Done is the owner's act** (Settled rule "Owner acts are the owner's"): in the viewer the row's Done, the owner's signed-in browser only (`POST …/owner-steps/<id>/done`, docs/API/security.md); in issue-cli a worker is refused, a lead relays the owner's words with `--quote`, the owner's shell may.
- **Drop** a step no longer needed: a lead or the owner; a worker only a step it added that the owner hasn't ticked.
- **Whose.** A worker's step is on its own issue (`--issue` names another); a lead's is on the project. A step outlives its issue: closing the issue doesn't tick it.
- **Handoff.** The lead's state file and the rotate's handoff note list open steps under the owner's section, and `lead flush pack` prints them for the nightly note.
- **The log** is `<issues>/.owner-steps/log.jsonl` (add, done, drop events), with the work files (Settled rule "Business data sits with the work files").

## Post-mortem

When an issue closes, a fresh agent that did none of the work reads the run and writes a post-mortem: a retro whose lessons each go to one place, and only once the owner agrees. Source: factory/post-mortem-actions-each-lesson-to-one-place-only-if-a-fresh-agent-would-act-differently-applied-after-the-owner-agrees.

```yaml
postmortem:
  at: done        # the status whose transition starts it (default: the last status)
  start: ask      # ask (default): print the start command · auto: the transition starts the agent
  auto_types: [feature, epic]  # optional, with start: auto: only these types start by themselves
  agent: claude   # optional; else routing: picks it as for a worker
```

- **Which closes start one.** With `start: auto` and no `auto_types:`, every close starts the agent but a chore's (a night's housekeeping: nothing to learn). With `auto_types:`, only an issue whose type is listed starts it, matched as written or as `default_type` resolves an untyped issue; any other type prints `Not started by itself: postmortem: auto_types: feature, epic starts it, and this is a tweak.` and the start command, as `start: ask` does, so the lead or the owner can still start one on purpose. A chore stays silent unless it is listed. With `start: ask` the list changes nothing. This repo lists `[feature, epic]`: tweaks, bugfixes and docs make most closes and repeat the same lessons (owner, 2026-11-06, "Yes I think that auto post-mortems should go on a high-er level.").

- **The agent.** `postmortem-<slug>`, headless, in the project checkout (no worktree). Its brief (`issue-cli postmortem brief <slug>`) has **paths only**: the issue file (body, decisions, comments), its data and stats sidecars, the worker's `.agent-logs/agent-<slug>/` (clilog, prompt), the branch and the base it was cut from (the range read with `git log --first-parent`, so a `chore(merge)` of main doesn't bring in other issues' commits), and the worker's own retros. A branch is named only while `work/<slug>` exists: once it is merged and deleted the brief points at `git log --grep <slug> main` instead. An epic's brief adds a **Children** part: each child's issue file, `work/<slug>` branch (or its commits on main by slug when the branch is gone), retros and post-mortems, so it reads the epic's whole run at the higher level (what repeats across children). None of the run's text is pasted. It only reads. A post-mortem that exists already, a live session or `mem-guard check` saying no holds the start, and the output prints the command for later. A session whose pane is only a shell (its agent exited or never started) counts as dead: the start removes it and goes on. So does a live session whose post-mortem is written (the trail's last `postmortem:` comment is `saved`): an agent idling at its prompt is finished, not running. The agent is `--agent`, else `agent:`, else routing's pick; a launch whose agent is gone within a few seconds is a failed start, as for any agent ([A launch that dies](agent-dispatch.md#a-launch-that-dies)): the session is removed and no `postmortem: started` comment is posted. By hand: `issue-cli postmortem start <slug> --quote "<the owner's words>"` (the lead or the owner, workers can't; `--again` writes a new one).
- **The retro.** `issue-cli postmortem write <slug> --file <path>` saves it under `retros/` with the header `Kind: postmortem` and six sections: `## Steps`, `## Worked`, `## Did not work`, `## Decisions`, `## Known`, `## Proposed actions`. The retro sorting room, `experiment candidates` and `memory candidates` read it like any retro. Run from inside its own session (`postmortem-<slug>`, or that window in a shared session), `write` closes that session a second after printing its result (a detached `tmux kill-session -t =<name>:`, never `kill-server`), so no post-mortem agent outlives its post-mortem.
- **An action** is `- [ ] A<n>: <action>` with `Differently:` (the answer to "would a fresh agent do something differently next session because of it?"), `Place:` and `Text:` (`Under: <heading>` optional; `At: <status>` optional on a memory action):

  | Place | Applied as |
  |---|---|
  | `memory <kind>` (fact, convention, pitfall, howto, who) | a project memory, sourced to the issue and the retro (a near-duplicate is updated, not doubled); with `At: <status>` it is tied to that status as `memory add --at` ties it, so `transition` shows it to whoever enters the status (an updated near-duplicate is tied too). The round checks the status again: one the workflow dropped since leaves the action open with the statuses |
  | `file <path.md>` (CLAUDE.md, AGENTS.md, docs/…) | the text spliced in under `Under:` (else at the end), one-file commit on main: `docs(postmortem): …` with `Owner: agreed …` and `Refs: <slug>` |
  | `skill <name>` | the same, in `.claude/skills/<name>/SKILL.md` |
  | `workflow` | an experiment proposal: an idea issue labelled `experiment` whose source is the retro (the [experiment loop](workflow-stats.md) measures it; nothing edits `workflow.yaml` directly) |
  | `none <doc path#anchor>` | nothing: the docs already say it, and the doc is named |

- **Refused, nothing saved.** `write` refuses a missing section, an action with no `Differently:` or a non-answer there ("n/a", too short, the action repeated), no or two places, an unknown place or memory kind, no `Text:`, a file path that is absolute, leaves the repo, isn't markdown or sits under `issues/`, `retros/`, `sessions/`, `.git/`, `.worktrees/` or `.agent-logs/`, a `none` naming a missing doc, `At:` naming no status of the project's workflows or on a place other than memory, a secret in `Text:`, and a duplicate id. Each refusal prints the shape that passes.
- **The owner's one round.** `/p/<project>/postmortem/<slug>` folds the five sections and lists every action with Agree / Refuse and a note, then **one** Submit (POST `…/postmortem/<slug>/decide`, the owner's browser only: an O route in [Security](API/security.md)). The round must answer every open action. Agreed ones are applied in order. A failed one (a heading gone, the checkout not on main) stays open with its error while the others apply. Once nothing is open the form is read-only and another POST gets 409. YOLO never answers it.
- **Across post-mortems.** `/p/<project>/postmortems` (linked from Needs you and each post-mortem page) lists every open action of the post-mortems Needs you counts, by place, biggest place first. Within a place, actions of different issues whose words overlap at least 0.3 (title, `Differently:`, `Text:`; `postmortem.SameThreshold`, no model) are copies, and a `file` or `skill` action is a copy only of one that edits the same file. A group of copies is **one row**: its newest title, a count, and every post-mortem it came from; the rest sit as "alone". Every copy starts ticked, so **one answer covers the group**, after a second click and with one note: **Agree** applies the first ticked copy once, as the post-mortem page's round would (memory, one-file commit, experiment, none), and ticks every other copy `Applied: as <file>#<id>: <what it made> · agreed <date> (owner)[: note]`, so a group makes one experiment, not one per copy; when that apply fails no copy is marked. **Refuse** writes the same `Refused: <date> (owner)[: note]` on each, and **Overtaken** takes a commit on main and writes `Overtaken: <sha> · <date> (owner)[: note]`. All three are the owner's, from the browser (POST `…/postmortems/mark`, an O route); each issue's trail gets one line, and the post-mortem page's round then answers only what is still open. On 2026-11-16 this board had 641 open actions in 227 post-mortems; one answer per group cut them to 497 answers.
- **Fixed already.** An action an issue or a commit has fixed needs no answer. `issue-cli postmortem fixed <retro file>#A<n> --by <slug|sha> [--copies]` (the lead, or any agent that knows) writes `Fixed: <slug|sha>` under it, after checking the slug is an issue or the sha a commit on main; `--copies` writes it on every copy of its group, and the issue's trail gets a line. The action is **fixed** once its issue is at the workflow's last status, or at once for a commit on main; a memory action is fixed too when an active memory has its very words (`memory.Hash`), where agreeing would add nothing. A near memory is not enough: within memory's 0.5 bar every such action on this board was an update of that memory. Fixed is read on every load and never written as an outcome, so a reopened issue makes the action wait again. Fixed actions leave the queue's rows, its count, Needs you and `postmortem list`'s counts, and show under **Fixed already** ("fixed by <slug>", "known as m-…"); a linked issue still open shows on its row as "fix underway: <slug> (<status>)". `issue-cli postmortem list --grouped [--json]` prints the page's rows for the lead, from the same grouping and fixed rule, ending with "N open action(s) of P post-mortem(s) need M answer(s) …; K fixed already need none."
- **Undoing a mark.** The queue page's **Marked lately** card lists the owner's refused and overtaken actions of the last 7 calendar days (`postmortem.MarkedDays`), newest first, from either page, even when the queue is empty. Undo reopens the ticked rows after a second click (POST `…/postmortems/undo`, the owner's browser only, no CLI verb): the line becomes `Reopened: <time> (owner), was Refused: <the old line>`, so the action is open again everywhere and the file keeps each step. Each issue's trail gets one line. An `Applied:` action can't be reopened, since what it made stays. Older marks: delete the line by hand in the retro.
- **Traceable.** Each action's line in the retro is ticked with `Applied: memory m-… | commit <sha> (<path>) | experiment <slug> | none (…)`, or gets `Refused: <date> (owner): <note>`. The issue gets one `postmortem: decided — N applied, M refused` comment. `issue-cli postmortem show <slug>` and `postmortem list` print them.
- **Waiting on you.** An issue's newest post-mortem with open actions is one Needs you row, wherever the file sits: `retros/` or `retros/archive/` (one waiting-on-you count, docs/departments.md Settled rules).
- **Lint.** The workflow check warns on an `at:` that names no status, a `start:` other than auto or ask, and an `auto_types:` name the workflow's `types:` doesn't define (or any name, when it has no `types:`). Catalog entry `postmortem`.

## Local CI

Agents run `make validate` on their own branch before they fast-forward main. Two branches that are green on their own can still break main together, and nobody runs the slow checks. Local CI re-checks the integration branch after every move and tells the system when it goes red. It is opt-in per project: without a `ci:` block nothing runs and nothing shows.

### Config

```yaml
ci:
  branch: main          # default: the branch checked out in the main checkout
  timeout: 20m          # for the whole run; default 10m
  nice: 10              # default 10, so CI doesn't slow the agents; 0 = off
  on_red: mark          # mark (default) or file_issue
  run:                  # in order; the first failure stops the run
    - make validate                    # a plain string = a default-tier command
    - cmd: make ux-test
      tier: slow                       # never on merge: issue-cli ci run --tier slow
      failures: '--- FAIL: (\S+)'      # optional: group 1 names a failing test
      results:                         # optional: test results the command writes
        - {kind: e2e, format: playtest, file: e2e.json}
  nightly:              # optional: run one tier once a night
    tier: slow
    at: "03:00"         # local time it may start from; default 03:00
    timeout: 3h         # for any run of this tier; default ci.timeout
```

- `ci:` is project-wide: system and type overlays can't change it.
- `issue-cli process schema` documents every key, and `process workflow` lints the block. It flags an empty `run`, a bad timeout, an unknown `on_red`, a `failures` regex without a group, and a block with only slow commands.
- Commands run with `sh -c` in their own session: their own process group, so a timeout kills everything they started, and no controlling terminal. The detached runner lives in a tmux window, so without that a command that starts an interactive shell (`bash -i`, `$SHELL -lic`) would stop on the window's tty until the timeout; with it the shell says "no job control" and carries on, as in any CI. They get `CI=true` and `CI_RESULTS_DIR` (see [Test results](#test-results-results)). `ISSUE_VIEWER_*`, `AGENT_NAME` and `TMUX` are removed, so issue-cli inside a test never thinks it is an agent and never reaches the real tmux server.
- The block does not need `allow_shell`: it is its own opt-in, and its commands are the project's own.
- This repo runs `make validate` on every move of main, with `make ux-test ALL=1` (every playtest session) as the slow tier, run every night. Both declare their test results.
- **Nightly:** with `nightly:` the viewer's CI watcher (and `issue-cli ci watch`) starts `issue-cli ci run --tier <tier> --detach` once a night: after `at`, or at the next look when yesterday's slot was missed, when no run is live, the tier has not run since the slot and `mem-guard check` allows it (a machine without mem-guard is allowed). One start per night, even if the run dies. The result is a slow-tier run like any other: `ci status --tier slow`, its failures named by the command's `failures:` regex, its red log kept in `.ci/red/` (the Status card's Last red line names the tier); it never turns main red. While it runs, merges wait for the lock: the default tier tests the newest head when it ends. `process workflow` flags a nightly tier with no command, a bad `at` or a bad `timeout`.

### Test results (`results:`)

A command can name the machine-readable test results it writes, so the [health tests family](workbench-files.md#the-tests-family) can count tests and time them without running anything again:

- **Where:** each command gets `CI_RESULTS_DIR`, an absolute folder `.ci/results/<sha>[.<tier>]/`, emptied when the run starts. The command writes its files there, and the entries name them.
- **Entry:** `{kind, format, file}`.
  - `kind`: `unit` or `e2e`.
  - `format`:
    - `go-test-json` (`go test -json`)
    - `trx` (`dotnet test --logger trx`)
    - `junit` (pytest, jest and most runners)
    - `playtest` (the e2e results `ux-test -results` writes)
  - `file`: a plain file name in the folder. `process workflow` lints all three.
- **After the command,** red or green, local CI parses each file:
  - The run record (`.ci/runs/<sha>.json`) gets a per-kind summary under `results`: tests, subtests, passed, failed and skipped, wall and summed seconds, mean/p50/p90 per test, the slowest parts.
  - The log gets a line such as `results: unit 1184 tests, 0 failed, 31.1 s wall (unit.json)`.
  - Every test with its time goes to `<kind>.tests.json` next to the file.
- **Problems never change the verdict.** A missing or broken file becomes the summary's `error`, for example `not written (the command must write $CI_RESULTS_DIR/unit.json)`.
- **Retention:** results folders are pruned with their run records.

Per project, as Makefile lines (only under local CI, so a plain `make test` is unchanged):

```make
# Go: issue-cli ci gotest prints what plain go test prints and saves the stream
test:
	go test ./... -count=1 $(if $(CI_RESULTS_DIR),-json | issue-cli ci gotest --out $(CI_RESULTS_DIR)/unit.json)
# .NET
test:
	dotnet test MyTests $(if $(CI_RESULTS_DIR),--logger "trx;LogFileName=$(CI_RESULTS_DIR)/unit.trx")
```

This repo uses `go run ./cmd/issue-cli ci gotest`, so CI tests the commit's own helper. The playtest runner writes `$CI_RESULTS_DIR/e2e.json` by itself when the variable is set.

### How a run happens

1. The viewer polls each configured project's branch head every 5 s. When the head was not tested yet and no run is live, it starts `issue-cli ci run --detach --if-untested`. A commit from a fast-forward, a `--no-ff` merge, a `git pull` or a hotfix on main counts the same, because the viewer watches the ref, not the merge command.
2. The run lives in tmux session `ci-<project>`, or window `ci-<project>` in the project's shared session. The owner can watch it live, and it stays open with the result until the next run. Restarting the viewer never kills a run, and on startup the viewer catches up on any untested head. Without tmux the run is a background process logging to `.ci/runner.log`.
3. The run takes `.ci/lock`, so there is one run per repository at a time. A lock whose process died is replaced.
4. The run checks out the head in the CI worktree `.worktrees/.ci`. The worktree is created once and reused (`checkout --force` plus `clean -fd`), so ignored build output survives and builds stay incremental. `issue-cli ci reset` rebuilds it from scratch.
   Before each command the run reads its text (ci.PrepareInstall). In a tree with `node_modules` links at any depth (a worker's worktree shares the main checkout's, root and workspace packages), a whole-tree install is skipped while every linked folder's lock file and `package.json` are the main checkout's, and any other install first turns the links into the tree's own copies (a clone on APFS), so no install writes through a link (D-8; docs/agent-dispatch.md#per-issue-worktrees). `rm -r node_modules/` while `node_modules` is a link into another checkout is not run: the run records an error naming the link, its target and the fix (`rm <link>`, then run it again). `ship`, `prove --run` and `worktree_setup:` do the same.
   A command that is one whole-tree install (`npm ci`, `npm install`, `pnpm install`, `yarn`, `bun install`, with flags but no package names and no shell around it) is skipped while the lock file, `package.json`, the command and `node --version` are as at its last green run: that run left their hash in `node_modules/.df-install-stamp`. The log says `skipped: <lock file> … unchanged …`, the command counts green, and the run record marks it `skipped`. An `npm ci` rewrote ~29k files on every merge (docs/fs-churn.md). A wiped `node_modules` takes its stamp with it; `issue-cli ci run --reinstall` forgets the stamps for one run, and `ci reset` rebuilds the worktree. go.sum and Cargo.lock need nothing of the kind: go and cargo keep modules in a cache outside the checkout.
5. When the run ends it re-reads the branch. If merges landed meanwhile it tests the newest head, so three merges during a run give one more run, not three.

Without the viewer, `issue-cli ci watch` does the polling; `--once` checks once.

### Files (`.ci/` in the main checkout)

`.ci/` writes its own `.gitignore`, so no repo has to ignore it.

| File | What |
|:--|:--|
| `.ci/state.json` | Where the branch stands: last tested sha, `green`/`red`/`error`, last green sha, red since, failing command and tests, what landed since green, the issues among it, the log tail. The viewer reads only this file and the lock |
| `.ci/runs/<sha>.json` | One run: each command's exit code and duration, the failing command and tests, the commit range from the last green run, the merged branches and issues, the log tail. A non-default tier writes `<sha>.<tier>.json`. The newest 50 runs of each tier are kept (with their logs and `.ci/results` folders), so merges never push out the nightly slow run health's `e2e_wait` reads |
| `.ci/runs/<sha>.log` | The full output of the run. A re-run of the same head writes it again |
| `.ci/red/<time>-<sha>[.<tier>][.first].log` | A red run's log, copied when the run ends so it outlives the next green run (and the 50-run prune). The run record's `kept_log` and the state's `last_red_log` (with `last_red_sha`, `last_red_tier`, `last_red_at`, kept while green) point at it; `.first` marks the first red after a green. The newest 10 are kept, none older than 30 days; the newest `.first` log stays past the count, not past the age |
| `.ci/results/<sha>/` | The run's `CI_RESULTS_DIR`: the result files the commands wrote, and a `<kind>.tests.json` list per parsed file |
| `.ci/lock` | The run in progress: pid, sha, tier, start |

### When it goes red

- **Blame:** the run lists what landed between the last green commit and this one. That covers local branches whose tip is in the range (matched to issues by `work/<slug>`), branches named by merge commits, and commits made straight on the branch. A rewound branch counts from the merge-base, and the first run ever blames nothing.
- **Status page:** the Local CI card shows the state chip, the one-line state and a Last runs row (the last 12 runs as green/red/amber boxes, oldest first, with a dashed box for a live run; each box's tooltip has the commit, result, age, duration and what failed). While red it also shows the failing command and tests, the issues that landed since green and the log tail. Green or red, a **Last red log** line links the newest kept red log, and the red boxes link to theirs (`/p/<project>/ci/log/<name>`, plain text). Red CI is the first item in the Needs you tile (Waiting on you in `status.md`) and counts in the sidebar's Status number. `status.md` carries the same line.
- **Card markers:** each issue merged since the last green run carries a red `CI red · <command>` chip while main is red, on the workboard (done cards too) and the system pages.
- **`ci:` comments:** each of those issues gets a comment with the failing command, its tests, what else landed and the log path.
- **The lead:** CI never messages it (nothing on a timer prompts a session). The lead reads `issue-cli ci status` when the owner asks, at check-ins and before a push, and decides with the owner whether to revert, fix forward or dispatch a fix. CI never reverts anything.
- **`on_red: file_issue`:** the first red run after green also files one `bugfix` issue (`Fix red CI on <branch> at <sha>: <command>`) with the range, the merged issues and the log tail in a `ci:` comment. Later red runs comment on that same issue, and the `ci:` comments link it. When main is green again the issue gets a comment and stays open for whoever ships the fix.
- **Quiet while still red:** later red runs comment and message again only if the failing command changes. The lead hears once more when main is green again, and the chips go.
- **CI can't run** (git or the worktree failed): the state is `error`, not red. Nobody is blamed, the card says why, and the lead hears once.
- **Health snapshot:** after each default-tier run, red or green, `ci run` takes a [health snapshot](workbench-files.md#health-snapshots) at the tested commit and prints `health: snapshot <path>`. A failed snapshot prints one line and never changes the run's result.
- **Slow tiers:** `issue-cli ci run --tier slow` runs the default commands, then the slow ones, once. Its result is shown separately (a `tier slow:` line under `ci status`'s first line) and never turns main red, blames issues or messages anyone. While the newest slow run is red, the agents' Local CI line (start, the dispatch briefing) adds it with its sha and age, marked inherited from main, so a worker whose `make ux-test` fails does not debug main's red as its own.

### What agents and the lead see

- `issue-cli start` and the dispatch prompt print the one-line state, for example `Local CI: main red since def5678: make validate FAIL (test: internal/ci): TestFoo — inherited from main, not caused by your branch`. An agent can tell a failure it inherited from one it caused.
- After `issue-cli transition <slug> --to done`, the agent reads that CI tests main by itself and that it must not wait.
- The lead brief adds a line when `ci:` is set: check `issue-cli ci status` before any push, and never push a release while main is red.
- `issue-cli ci status` prints the one-liner first, then what landed and the recent runs; `--json` dumps the state and runs. A command that times its steps (make validate prints `validate: <step> <seconds>s` per step) keeps them in its run record as `steps: [{name, duration_ms}]`, and `ci status` names the newest run's slowest step. See [CLI Overview](CLI/overview.md#ci).

## Deploy

A project's own deploy (back up the database, run the install script, deploy the landing, probe the live URLs) is a list in its `workflow.yaml` that `issue-cli deploy` walks the same way every time: it checks each step's needs, runs the step, runs its probe, stops at the first red or refused step, and keeps a record the Report shows. It is opt-in: without a `deploy:` list the command prints a snippet. It is not DF's own release flow (`issue-cli release`, [Roadmap: Cutting a release](roadmap.md#cutting-a-release-candidate-soak-publish)).

```yaml
deploy:
  - name: backup
    run: ./scripts/backup-db.sh
    needs: no-live-workers
  - name: app
    run: ./install.sh
    needs: [main-green, owner-ok]
    probe: curl -fsS https://example.com/health
    timeout: 15m
  - name: landing
    run: npm run deploy -w landing
    needs: owner-ok
    probe: curl -fsS https://example.com/ -o /dev/null
```

- `name` is unique; `run` and `probe` run with `sh -c` from the project's main checkout. A non-zero exit of either makes the step red. `timeout` (a Go duration, default 30m) covers both; a step past it is stopped with what it started.
- `needs:` is a word or a list:
  - `main-green`: local CI's newest run is green on main's head ([Local CI](#local-ci)). Red, an error, CI off, or green on an older commit (a merge CI has not run yet) refuses with `ci status`'s one line.
  - `no-live-workers`: no worker session of the project is live; the refusal names them.
  - `owner-ok`: the owner's words, passed with `--quote`. A deploy goes out of the machine, so a step that publishes should say it ([Departments: Settled rules](departments.md#settled-rules), "Nothing goes out without the owner's stamp"); a dry run with no `owner-ok` step says so. YOLO never passes it.
- Who runs it: the lead, relaying the owner's words for an `owner-ok` step, or the owner's shell. A worker is refused a real run (the worker guard; never passed on a sandbox) and may run `--dry-run`.
- `issue-cli deploy --dry-run` prints the plan in order and each need's verdict now; it runs and writes nothing. `--step <name>` runs one step; after a red step is fixed, `--from <name>` goes on from it (the refusal prints that command). One deploy runs at a time (`.deploys/lock`).
- Each run is recorded in the issues dir's `.deploys/<YYYYMMDD-HHMMSS>.json` (who, the owner's words, main's head, each step's state `green | red | refused | not run`, exit, probe exit, start, seconds, why) with every command's output in `<id>.log`. The records sit with the work files, so a private tracker keeps them private. `issue-cli deploy --last` prints the newest; the Report's Last deploy card shows it (and `status.md` a `Last deploy:` line).
- `workflow.yaml`'s `deploy:` is project-wide: a department's file takes Product's. Its fields are in `issue-cli process schema` (`deploy[]`). See [CLI Overview](CLI/overview.md#deploy).

## Red-round cap

A check that stays red round after round becomes a question for the owner, and the worker stops looping on it. The owner chose four rounds (factory/stop-after-four-red-rounds-…, from the spec-flow epic's D-4).

```yaml
max_red_rounds: 4   # top level, project-wide like ci:; 4 when unset
```

The briefing and `process schema` say what counts in one phrase (`tracker.RedRoundsWhatCounts`):

> a gate's command, the proof check, a local CI command or the critic's held runs; one round per commit, each held critic run its own round; only a failure that comes back counts, and an owner comment starts the count over

The rest of this section is the detail behind it.

- **What counts.** Checks that run and can go red without the worker touching them: a gate's `command_succeeds` command (`gate:command_succeeds:<command>`), the proof check (`gate:criteria_proven`), and a local CI command on a run that merged the issue (`ci:<command>`). Checkbox and section gates don't count, because the worker fills those in. `issue-cli transition` records each check it ran, red or green, in the issue's stats sidecar (`rounds:` in `<issue>.stats.json`). CI runs are read from `.ci/runs`. The fresh critic's runs are read from `<issues>/.critique/runs.jsonl`. Each gated run with a blocks finding is a red round of `critique <from> → <to>` (a run by hand, from a status with no gate, holds nothing and is not a round), and the fourth that counts posts the question. While it is open, `issue-cli critique` refuses as well ([The Fresh Critic](Workflow/critique.md#the-gate)).
- **A round** is one fix attempt. Critique runs are the exception: each held run is its own round. Otherwise, reds at the same work-branch head fold into one round, so retrying without a new commit doesn't add to the count. With no branch, each red is a round. A green ends the check's streak.
- **Only a repeated failure counts.** Each round keeps what failed: the critic's blocks findings (matched by path and text, with case, spacing and punctuation folded and the line left out), the failing tests (`--- FAIL:` lines, or local CI's failure names), the unproven criteria of the proof check (by id), or else the failure text with numbers and shas masked. A round counts when one of its failures was a failure of an earlier round since the last green. A round whose failures are all new starts the count over at 1: a design whose critic finds new, fewer problems each run (3, 2, 1, 1) stays at "red 1 of 4". The same finding four times still reaches the cap, and so does a finding that comes back after a run without it. Records from before v0.60 have no failures kept: their failure line is compared instead.
- **An owner comment starts the count over.** Any comment by the owner on the issue (author `owner`, from the viewer) makes every round before it stop counting, for every check. A cap question that is already open stays open: only resolving it answers it.
- **At the cap.** The transition that records the fourth counted red posts `question: red rounds: <check> — red 4 of 4 …` with the recommendation first and the options after it, then refuses. While that question is open, every `transition` that runs that check refuses *before* running anything, naming the question. A question on a gate or critique check the transition doesn't run (the critic gate was taken out of workflow.yaml, or it sits on another edge) stays open for the owner but doesn't hold the transition; a question on a local CI check or a name given with `--cannot-fix` holds every forward transition. A transition to a side status (`parked`, or any optional status every status can move to) is never held: it passes and counts no rounds, and `close --duplicate-of/--fixed-by` parks the same way. `transition --dry-run` lists the same hold (kind `hold` in `--json`): the open question that would refuse the transition, or a check at the cap it would post as a question first; it posts nothing. The check is not retried automatically. YOLO doesn't pass it: it's a question, not a gate approval. The question is a `question:` comment, so it shows on Needs you like any other.
- **The owner's answer** is resolving the question (the comment toggle on the issue page). Rounds before it no longer count, and the next transition runs the check again.
- **Can't fix it.** `issue-cli rounds <slug> --cannot-fix "<check>" --why "…"` raises the question at once, for a listed check or a new name such as `test:TestFoo`.
- **Where it shows.** A red check's refusal says `red 2 of 4 (max_red_rounds)`. `issue-cli rounds <slug> [--json]` lists every red check and each of its rounds since the last green: `round 3 · count 2 · same finding: design.md — … as round 1`, or `all failures new: the count starts over` (`history` in the JSON). `show` and `start` print a `Red rounds:` line. The worker briefing has a `## Red checks` section. The Station's Tests and proofs panel lists *red checks* with "red 3 of 4".
- **Lint.** `workflow` warns on a value below 1. `max_red_rounds` is project-wide, so a department's own file setting it gets the `department` warning. Catalog entry `red-round-cap`.

## Fresh session per stage

A status can say that its work starts in a new session instead of the one that did the stage before. The epic behind it measures first and adopts only what wins (factory/fresh-agent-per-stage-measured-tracing-a-golden-set-evals-per-stage-and-the-fresh-vs-continue-experiment#D-1), so dark-factory's own workflow.yaml sets it nowhere yet: the fresh-vs-continue experiment decides.

```yaml
statuses:
  - name: "documentation"
    session: fresh            # continue (default) | fresh
    briefing_budget: 3000     # the stage briefing's cap, in estimated tokens
```

- **Unset or `continue`** is the single session from design to shipping, with today's briefing.
- **Entering a fresh status.** When the issue has a live worker (`agent-<slug>`), `transition` posts `stage: fresh session at <status>` and tells the worker to write `HANDOFF.md` (what's done, the next step, open questions) and end its turn. It starts and kills nothing: a worker never starts an agent.
- **Who starts the new session.** The lead, at its check-in. `issue-cli queue next`, `autostart next` and `agents` print `issue-cli dispatch <slug> --fresh` when it may run, or the hold: the old pane still in a turn, no `dispatch:` words to quote, the owner's gate outside YOLO. `dispatch --fresh` quotes the issue's newest `dispatch:` words, as a [takeover](agent-dispatch.md#takeover-at-a-usage-limit) does, checks mem-guard and the machine-wide start gap under the same lock, closes the old session and starts `agent-<slug>` in the same worktree. It leaves `dispatch: "<words>" (fresh session at <status>, <date>)`, which answers the mark; the takeover's once-per-issue count doesn't see it.
- **The stage briefing.** Any dispatch at a fresh status (▶, `--quote`, `--fresh`, `dispatch-prompt`, the benchmark's sandbox) gets it instead of the full briefing. It is paths, not the issue's body or earlier chat: the issue file, the sections the stage before wrote (those the transition into it appended, walking back over a status that appends none, such as backlog), and `<worktree>/HANDOFF.md`. Then the stage's guidance (with the YOLO rule when on), its output (the sections the transition into this status appended) and the gate out (the next transition's requirements), the stop lines, a short command list, the worktree rules, the handoff, your lead, your rules, the memories `--at` this status and the decisions in force at it. Shell notes, local CI, the build note and the red-round rules come only at statuses whose role isn't design or queue.
- **The budget.** The briefing ends with `Briefing: N tokens of B`. Over `briefing_budget:` (3000 when unset), parts fold to the command that prints them, in this order: memories, the decisions list, shell, local CI, build, red rounds. Still over, the line adds `over budget`. The owner's rules, the YOLO rule, the stop lines and the worktree rules never fold. `dispatch-prompt --json` carries the size as `stage: {budget, tokens, folded, over_budget}`.
- **HANDOFF.md on every issue.** The worker briefing's `## Handoff` and `transition`'s reminder no longer need a `routing:` block, and `agentrun.Start` keeps the file out of git in every worktree. A type that builds nothing (report, chore, epic) keeps its handoff in `progress:` comments instead ([the note](agent-dispatch.md#takeover-at-a-usage-limit)).
- **Lint.** `workflow` warns on a `session:` other than `fresh` or `continue`, a `briefing_budget:` under 500, and a budget on a status that isn't fresh. `process workflow` marks each fresh status with its budget. Catalog entry `stage-sessions` (not proposed until the experiment says so).

## Private work and code

A project's work files tell how the work was done: issue bodies with the owner's words, session notes, retros. In the code repo, they go wherever the code goes. The opt-in `privacy:` block records the owner's choice of where they live, and nothing is decided silently: `issue-cli init` asks on a terminal, `workflow upgrade` proposes the `privacy` entry with its question and setup steps, and the Updates card lists them.

```yaml
privacy:
  work_files: tracker        # tracker | in_repo (keep them in the code repo, by choice)
  work_paths: [issues, sessions, retros, bugs, .health/snapshots]   # the default
  code: snapshots            # snapshots | public (develop in the public repo, by choice)
  public_remote: public      # code: snapshots only (default public)
  public_branch: main        # (default main)
  decided: "2026-10-24, owner: \"…their words…\""
```

Without the block the project has not decided. `process workflow` warns about an unknown value, a work path outside the project, and an empty `decided:`.

### The tracker repo (`work_files: tracker`)

The work files live in a private tracker repo whose git folder sits **outside the checkout**: `<checkout's parent>/.trackers/<slug>.git` by default, or `tracker_git_dir:` on the project in projects.yaml. Its `core.worktree` is the checkout and `status.showUntrackedFiles` is `no`, so:

- every path stays where it is: the viewer, issue-cli and the lead's handoff path work unchanged;
- the code repo ignores the work paths (`.gitignore`) and sees nothing;
- `git clean -fdx` in the checkout can't take the tracker's history.

Setting it up (the `privacy` entry's setup steps):

1. The owner answers, and the answer goes in `privacy:` with their words.
2. The owner creates a private repo, e.g. `<name>-tracker`. Agents never create repos.
3. `issue-cli tracker init --remote <url>`, with the URL the owner gives (init refuses without it), creates the git folder (or adopts an existing one, as it is), adds the remote, ignores the work paths in the code repo, installs the pre-push guard and pushes the first backup. When the code repo still tracks work files it prints the `git rm -r --cached` to run with the owner; it never runs it. Their old history stays in the code repo until a one-time clean from a mirror (`git filter-repo --path <work path> --invert-paths` on a fresh mirror clone, then a force push, with the owner present).
4. `issue-cli privacy check` shows every line green, or each fix.

**Sync.** The viewer syncs each tracker every 10 minutes (tracker_watch.go): it commits every change under the work paths (`tracker: N files`), pulls with rebase and pushes. It prompts no session. A conflict aborts the rebase, keeps the local commit and pushes nothing; the error goes to `<git folder>/issue-cli-sync.json` and Status shows **Backup failing**. `issue-cli tracker sync` does the same on demand.

**Another machine or a cloud session** clones the tracker too, with the same layout (a git folder outside the checkout, `core.worktree` the checkout), and runs `issue-cli tracker sync` before work (pull) and after (push). A session that writes an issue into the code repo instead is how a work file goes public: PR #62 on the public repo was a cloud session's.

### Private development, public releases (`code: snapshots`)

A public dev repo publishes how the work was done, not just the result: commit messages with owner quotes, D-n text and `Proves:` trailers, branch names (issue slugs), PRs and their refs, which GitHub never lets anyone rewrite. With `code: snapshots`:

- a **private dev repo** (e.g. `<name>-dev`) is `origin` and holds everything: main, branches, tags, PRs;
- the **public repo** is the remote `public` (its tags are never fetched, so they can't clash with the dev tags) and gets **one commit per release**: the release branch tip's tree, subject `vX.Y.Z` and the release's changelog section as the body, built with `git commit-tree`, so no dev commit, message, branch name or quote travels with it. It goes straight onto the public main: no release PR (a squash merge made on GitHub would not carry the `vX.Y.Z` subject the guard and the next publish look for). `release publish <version> --dry-run` shows the parent, the message and what the commit changes, and writes nothing. Between releases it does not change; released code stays readable and `go install`-able;
- the snapshot is **signed** with the owner's `user.signingkey` (`commit-tree -S`), so GitHub shows it Verified; a key that fails to sign stops the publish, and the Code badge turns amber (**Release unsigned**) while the public tip has no signature;
- releases follow the branch flow in [Roadmap: Cutting a release](roadmap.md#cutting-a-release-candidate-soak-publish): `issue-cli release cut` (the branch `release/vX.Y` and its rc tags), `release pick` (fixes from main), `release publish` (the snapshot). `release/vX.Y` stays in the dev repo for good.

**The first switch of an existing public repo** (the `privacy` entry's setup steps):

1. The owner creates the private dev repo. Agents never create repos.
2. `issue-cli release setup --dev <url>`: the old `origin` becomes `public`, the dev repo becomes `origin` and gets every branch and every non-candidate tag, the public branch's tip is recorded as the base the first snapshot goes on, and the pre-push guard is installed. Nothing is pushed to `public`.
3. The public repo still holds the old dev history. Leaving it is fine: the first snapshot goes on top of the recorded base, and from then on each release is one commit (dark-factory kept v0.55.0 and v0.56.0 this way, `factory/release-bundling-one-public-commit-or-squash-merged-pr-per-release#D-2`). To remove it instead, the owner cleans it **once, from a mirror**, never in the working checkout (so no worker waits): `git clone --mirror <public url>`, `git filter-repo --path issues --path sessions … --invert-paths`. Commit messages (quotes, D-n, trailers) and branch names stay public after a filter alone, so issue-viewer went further (2026-10-24): one squashed, signed commit per old tag (`git commit-tree -S`, the tag's filtered tree, message `vX.Y.Z`, the previous one as parent), the tags moved onto them, the old branches deleted, then `git push --force` of main and the tags. Back up the untouched mirror first (a tarball and a private repo). PR refs GitHub keeps can't be removed this way. Then record the cleaned tip as the base, so the Code badge doesn't read the cleaned history as dev history: `git fetch public && git config issue-cli.publicbase $(git rev-parse public/main)`.
4. In the release workflow, skip private repos (`if: ${{ !github.event.repository.private }}`) so the dev repo builds no releases, and take the release notes from the CHANGELOG's version section (a snapshot is one commit, so commit subjects say nothing). This repo's `release-marker.yml` does both, writing the notes with `scripts/release-notes.sh`: GitHub refuses a release body over 125,000 characters, so a section over 120,000 bytes goes out as each entry's bold title (an entry without one keeps its first sentence) and a link to the changelog at the tag, and `issue-cli process changes` shows those titles for that release.
5. The next release is the first snapshot: see it first with `issue-cli release publish <version> --dry-run` from the release worktree. If the public tip is no longer the recorded base (the mirror clean rewrote it), `git fetch public` and pass `--first` once.

**Cloud sessions and other machines** clone the dev repo (and the tracker), never the public one, and push there. A cloud session that opens a PR on the public repo publishes it: that is how PR #62 went public.

**Commit messages.** With `code: snapshots` they keep the owner's quotes and D-n text as today: they never leave the dev repo. With `code: public` the guard refuses a push whose commit message contains a recorded decision's quote (16 characters or more, matched against every issue's `## Decisions`).

### The guard

`tracker init` and `release setup` install a pre-push hook in the repository's common git folder, so every worktree shares it. An existing pre-push hook is kept as `pre-push.local` and runs first. The hook runs `issue-cli privacy pre-push`, which refuses:

- in every project, with or without a `privacy:` block, a push whose commits add a key or an env file (`secret guard: this push: …`, [the secret scan](#the-secret-scan));
- with `work_files: tracker`, a push whose commits add a work path to the code repo (`privacy guard: <sha> adds work files to the code repo (issues/…)`);
- with `code: snapshots`, any commit or tag to the public remote (by name or URL) that is not a release snapshot (`privacy guard: public gets release snapshots only, and <sha> ("…") is a dev commit`);
- with `code: public`, a commit message that quotes a decision.

`issue-cli privacy hook` reinstalls it. A clone without the hook (another machine, a cloud session) is the gap it can't close, which is why everyone works against the dev repo. For a PR opened on GitHub, `.github/workflows/privacy-guard.yml` reads the same `privacy:` block: a PR touching a work path fails (`work_files: tracker`), and with `code: snapshots` every PR on the public repo fails, saying PRs are not merged there and asking for an issue instead (the public repo's `ci.yml` only builds and tests each snapshot pushed to main). Copy it into a project that turns the feature on.

### The leak scan

Tracked files must not carry this machine's private data. The scan (`internal/privacy/leaks.go`) looks at every tracked text file for:

- a user home path: `/Users/<name>/`, `/home/<name>/`, `C:\Users\<name>\`, and Claude Code's encoded folder names (`-Users-<name>-`);
- a Claude Code scratchpad path (`/claude-<uid>/`);
- a name from the owner's private-names list, `$XDG_CONFIG_HOME/ai-workflow-engine/private-names.txt` (default `~/.config/ai-workflow-engine/private-names.txt`): one name per line, `#` for comments, matched as a whole word without case. The list stays outside the repo, so the names never reach it. With no list, only the path patterns run.

`TestTrackedFilesCarryNoLeaks` (internal/privacy) runs it over the repo, so `make validate` fails on a hit and names the file and line (`make leak-check` runs it alone, before the test suite). Earlier still, the commit-msg hook (`make hooks`, `issue-cli workflow commit-msg`) runs the same line matchers over the lines the staged diff adds and over the commit message, in a dark-factory checkout only, and prints `warning: <file>:<line>: <what>` with the stand-in to use (describe another project by its layout, or a placeholder such as `Orchard`). It only warns: the commit goes through, and `git commit --amend` fixes it before anything pins the sha. The warning names the list's line, never the name; a merge in progress is skipped. `issue-cli release publish` runs it over the release branch's tree and refuses, the `--dry-run` too, before anything goes public.

Earlier yet, `issue-cli create` (and `handoff`, which writes through it) keeps a name from the list out of a new issue's slug, in a dark-factory checkout only: an issue's slug goes into its branch (`work/<slug>`) and every `Refs:` line, so a sweep or report named after another project would fail the scan at validate. A hit in the title or the slug drops the name's words from the slug (`issue` when nothing is left) and pins it with `slug:`; the title keeps the name, since `issues/` is no tracked file. Create prints a warning naming the list's line, never the name (`TestCreateKeepsPrivateNamesOutOfTheSlug`; source: cli/create-warns-when-a-sweep-or-report-title-names-a-private-project-so-its-slug-and-branch-dont-carry-the-name#D-1).

Beside it, `TestTrackedFilesNameUpstreamOnlyInLicense` (`internal/privacy/upstream.go`) guards the upstream author's name: in any case, it may appear in LICENSE.md only (its Required Notice lines), and a hit in any other tracked file fails `make validate` with its file and line. `issue-cli release publish` (and its `--dry-run`) runs the same check over the release branch's tree and refuses a hit, even on a machine with no private-names list. Fixtures and docs use placeholder owners such as `example-org/example-repo`; this repo is `github.com/kacperwerema/dark-factory`. Source: factory/public-repo-cleanup-v0-60-0-module-path-layout-leftovers-naming-licence-and-attribution#D-4.

A test input or doc that needs a home path uses a placeholder user: `someone`, `owner`, `me`, `u`, `a` and the others in `placeholderUsers` in leaks.go. Add a made-up name there when a test needs another one, never a real one. For a sample project, use a placeholder such as `Orchard` / `orchard`.

### The secret scan

A key never goes in a file in a repo ([where each kind lives](departments.md#secrets)). The secret scan (`internal/privacy/secrets.go`) refuses:

- a line holding a key: ElevenLabs (`sk_` and 32+ letters or digits), Stripe live (`sk_live_`, `rk_live_`), GitHub (`ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_`, `github_pat_`), AWS (`AKIA`/`ASIA` and 16), Slack (`xoxb-<digits>-…`), OpenAI and Anthropic (`sk-proj-`, `sk-ant-`, `sk-svcacct-`), a PEM `-----BEGIN … PRIVATE KEY-----` line, a JWT (`eyJ….eyJ….…`);
- a file named `.env` or `.env.*`. `.env.example`, `.env.sample` and `.env.template` pass; their lines are checked like any file's, so a key in one is still refused.

A value that looks like a placeholder passes: fewer than 8 distinct characters after the prefix (`sk_xxxx…`, `ghp_0000…`), or `EXAMPLE` in it (AWS's documented `AKIAIOSFODNN7EXAMPLE`); `sk_…` in a doc is too short to match. Each hit names the file, the line and the kind (`config.go:3: a GitHub token (ghp_…)`), never the value or any part of it, then says what to do: keep the key in Keychain or the vendor CLI's own file, take it out of the commit, add `.env` to `.gitignore`, and rotate a key that was ever pushed. A test that needs a key-shaped value builds it at run time (`"gh" + "p_" + …`), so its source line holds none.

It runs in four places:

- `make leak-check` (`TestTrackedFilesCarryNoSecrets`), over every tracked file, so `make validate` fails on a hit;
- the commit-msg hook (`issue-cli workflow commit-msg`), over the lines the staged diff adds and the files it adds or changes: it refuses the commit (a merge in progress is skipped);
- the pre-push guard (`issue-cli privacy pre-push`), over the commits the push sends (their added lines and added or changed files, a key added then removed included), in **every** project that has the guard, with or without a `privacy:` block. A file the push doesn't touch is not read, so an old one never blocks a push. The commits it reads stop at what the remote has: the ref's old tip and the remote's tracking refs, or, for a push by URL or to a remote never fetched, the tips `git ls-remote` lists. When the remote shares no commit with the repo (empty, unreadable or unrelated: a first push), it scans the pushed tip's tree the way `make leak-check` does instead of every commit since the root, so a stand-in an old commit held and the tree no longer does is not refused; a key in the tree still is. Another project gets it with `issue-cli --project <slug> privacy hook`; `issue-cli privacy check` shows a `secret` line, amber with that fix while the guard is missing;
- `issue-cli release publish`, `--dry-run` too, over the release branch's tree, before anything goes public.

Beside it, memories refuse secrets of their own (`internal/memory/secret.go`, which also knows `password=` forms that would be too noisy over a whole repo).

### What Status shows

A **Work** and a **Code** badge after the word Status, their lines at the foot of the Health card (or a Privacy card without one), and markers on the projects list and the `/stats` overview. All local: `git ls-files`, the tracker's git folder and its sync file; no network.

| State | Badge | When |
|:--|:--|:--|
| Green | Work private | a tracker with a remote; the line names its repo and the last backup |
| Amber | No backup | ignored by git and backed up nowhere, or a tracker without a git folder or a remote |
| Amber | Backup failing | the last sync failed |
| Red | Work in code repo | the code repo tracks a work path; shown even before the feature is on, because it is a fact from git |
| Grey | Work public | `work_files: in_repo`, the owner's choice; never a warning |
| Grey | Not set up | no choice and no work files |

| State | Code badge | When |
|:--|:--|:--|
| Green | Code private | `code: snapshots`, a public remote, the guard installed, the public tip a snapshot; the line names the dev repo and the last published version |
| Amber | Not switched | `code: snapshots` but no public remote yet (`issue-cli release setup`) |
| Amber | Guard off | this clone has no pre-push guard (`issue-cli privacy hook`) |
| Red | Dev history public | the public branch's tip (as of the last fetch) is not a release snapshot and not the recorded base |
| Grey | Code public | `code: public`, the owner's choice |
| Grey | Not set up | no choice |

The badge's title and aria-label carry the full sentence; an amber or red line adds the fix. `issue-cli privacy check` prints the same lines (exit 1 on red) plus the guard.

## System Overlays

Each system (API, CLI, UI, Workflow) can override status prompts and add transition actions. Overlays are defined under `systems:` in `workflow.yaml` and merge with the base workflow:

```yaml
systems:
  API:
    statuses:
      - name: "in design"
        prompt: |
          Extra API-specific design guidance.
    transitions: []
```

`prompt:` replaces the status prompt for that system's issues, including a prompt their type set. To add to it
instead, use `prompt_append:` on the overlay status: its lines follow the base or type prompt, so tweaks and bugfixes
get the system's guidance too ([Workflow overview: System Overlays](Workflow/overview.md#system-overlays)).

System-specific documentation lives in `docs/<System>/` and agents should reference it during the documentation status. See:

- [API Overview](API/overview.md)
- [CLI Overview](CLI/overview.md)
- [UI Overview](UI/overview.md)
- [Workflow Overview](Workflow/overview.md)

## Department workflows

A project can split its issues into departments (`departments:` on the project in `projects.yaml`). An issue belongs to a department when it has `department: <name>` or sits in one of the department's `systems:`. Everything else is Product. A department can run on its own workflow file:

```yaml
projects:
  - name: "Acme"
    workflow: "workflow.yaml"           # Product's file
    departments:
      - name: "Marketing"
        systems: ["SEO"]
        workflow: "workflows/marketing.yaml"   # relative to the project root
```

- **Without `workflow:`** the department runs on Product's file, as before. A project without departments behaves exactly as it did.
- **Every read is per issue.** transition, start, check, show, approve, done, prove, the dispatch prompt, the issue page and the station read the issue's own file. System overlays and `types:` work inside a department file as they do in Product's.
- **Only the lifecycle lives in a department file:** statuses, transitions, prompts, types, systems, board. The project-wide keys (`workbench`, `ci`, `health`, `privacy`, `leads`, `project_actions`, `allow_shell`) are always taken from Product's file. `health lint` warns when one shows up in a department file. `worktree:` is the exception: a department file may set its own (content work may not need a code worktree), else Product's applies.
- **No fallback.** A missing or broken department file is an error that names the file. Commands on that department's issues stop, and its room in the viewer shows an error chip. Product's issues are not affected.
- **Statuses are keyed by (department, status).** `done` can be in both files. The Product board and hall show only Product's issues. Each department room draws its own line, and the Board's **Workflow** switch (`?department=<name>`) shows one file's columns at a time. Columns are never merged across departments.
- **Epics across departments.** A child counts as done when it reaches the last status of its own file's path.
- **Moving an issue** to another department (`set-meta --key department|system`, or the issue page) is refused when its status isn't in the target file. The error lists the target's statuses and the retry: `issue-cli set-meta <slug> --key department --value Marketing --status brief`.

Commands that take `--department <name>`: `create` (and `--status` is checked against that file), `list`, `process workflow|transitions` (without it, on a project with departments, they print Product's lifecycle and list the department files), `workflow upgrade|scan`. `next` lists each department's work under its own heading. `show` and `start` print `Department:` and the file under `Workflow file:`.

### Setting one up

From a bundled department template, in one step (workflow file, role prompt, goals and the projects.yaml entry; see [Departments: templates](departments.md#templates)):

```bash
issue-cli department add marketing
```

By hand:

```bash
issue-cli workflow init --template content --out workflows/marketing.yaml
# add workflow: workflows/marketing.yaml to the department in projects.yaml
issue-cli health lint --check department
```

The lint lists the department's issues whose status isn't in the new file, each with the `set-meta … --status` fix.

The bundled `content` template is for recurring outputs such as posts and SEO pages: `brief → draft → owner review → publish → done`. `owner review → publish` needs the owner's `publish` approval in the viewer, so nothing goes out without the stamp. `owner review → draft` needs a `revise:` comment. `publish` adds `- [ ] Published: link to what went out`, and `publish → done` checks it.

**Known limits.** The canvas, Stats, the timeline aggregate and the Status page order statuses by Product's file: a department's statuses sort after Product's. `next` keeps Product's backlog / in progress / testing buckets for Product's issues.

## Keeping workflow.yaml up to date

Projects edit their `workflow.yaml` heavily, so a newer issue-cli never overwrites it. Instead:

- `workflow.yaml` carries a `template: <name>@<release>` stamp and a `features:` record (installed `<id>@<version>`, declined `<id>`). `issue-cli workflow init` writes both.
- `issue-cli workflow scan` detects which catalog features the file already has and writes the record. Use `--dry-run` to preview.
- `issue-cli workflow upgrade` lists the features the project could adopt, each with what it does, why, a snippet and a question for you. An agent proposes them one at a time and copies in only the ones you pick. Declined ones stay declined until you re-open them with `issue-cli workflow scan --forget <id>`.
- You see what there is to adopt on the viewer's **Updates** tab (`/p/<project>/updates`, and `/updates` for every project): decline a feature, re-open it, or start an agent that installs it with you. No `issue-cli` command mentions upgrades unless asked, so workers and leads are never nudged.
- With a `features:` record, the record decides what is offered: the stamp hides nothing undecided. Bump the stamp only once every proposal is installed or declined.
- `upgrade` calls a current stamp `up_to_date` only when the record decides everything: `undecided` while proposals wait on you, `stale_record` when the file has features the record misses. `process workflow` and `health lint` warn about a stale record and name `issue-cli workflow scan`.

Details: [Workflow Overview](Workflow/overview.md) (stamp, record, catalog, keeping it complete) and [CLI Overview](CLI/overview.md) (`workflow upgrade`, `workflow scan`, `workflow catalog-check`).
