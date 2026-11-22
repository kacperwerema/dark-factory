---
title: "Departments: Role, Goals, Outputs and Jobs"
order: 9
---

# Departments: role, goals, outputs and jobs

A project's **departments** live in `projects.yaml`, under the project. Every project has two: **HQ**, the CEO's room with the business goals, and **Product**, the software factory that runs `workflow.yaml`. `departments:` adds more (Marketing, Sales, …). A department's work is the issues with `department: <name>` in their frontmatter, or in one of its `systems:`; the rest is Product's. A system named after a department doesn't put an issue in it: `system: Marketing` without `department:`, when Marketing's `systems:` don't list it, is Product's. `create` warns on that, and the `departments` lint check warns on each open issue like it.

Each department, HQ and Product can have:

- a **mission**: what it is for, in a sentence;
- a **role prompt**: a markdown file a lead or agent of the department starts with;
- **goals** (KPIs): numbers with a target, read by hand or from a file. Department goals **roll up** into HQ's business goals.

Nothing shows unless it is configured. A project without `company:` and goals looks as before: the company header shows the two open epics furthest along.

## Settled rules

Rules the owner and the lead have already settled for department and Factory work. Don't derive them again from a lead message: follow them, and cite the decision when a design leans on one. A rule changes only by a new decision on the issue that changes it, and then this list changes with it.

Each source D-n is also a scoped record on the board, so the rule reaches the briefings by itself (`issue-cli decisions --in-force <slug>`): `system:Factory` for the engine's department rules, `department:marketing` where only Marketing acts on it, `project` for the rules every design meets, and `hard` only where the owner's words are on the line. `internal/tracker/testdata/settled-rules.tsv` lists each rule's sources with that scope; `TestSettledRulesTableMatchesDoc` keeps it in step with this list, so a new rule or source gets a row there (the lead applies it with `decide --amend`).

- **Secrets are references.** A connector names `env:<VAR>` or `keychain:<service>`, never a value; the owner creates the value, the engine never prints or copies it, and pages show only set / missing; the writers are the owner's [Connect socials](#connecting-socials-from-the-factory) page, which hands a value the owner types to Keychain, and `issue-cli cms up`, run in the owner's shell, which hands Keychain the Ghost password and key its run generates, never printing them ([Stand up the CMS](stand-up-the-cms.md); provisional, factory/stand-up-a-projects-cms-ghost-in-one-owner-command-and-the-go-live-walk-through clarify Q1). A connector reaches only its department's lead and workers, never the CEO or Product (the owner uses `--as-owner`). Tests use fakes in a sandbox project, never real credentials. Sources: factory/department-connectors#D-1, #D-2, #D-3, #D-4, #D-5, #D-6, #D-7; see [Connectors](#connectors).
- **Private projects stay private.** Content from the owner's private projects (their names, client rosters, client sites, Keychain service names) stays in that project's repo or tracker. This repo, its tests, fixtures, docs and prompts don't name such a project: they use placeholders (`Orchard`, `orchard`, `client-site`). The names sit in a list outside the repo that the leak scan reads ([Private work and code](workflow.md#private-work-and-code)). Sources: factory/factory-benchmark-and-evals#D-10; the pilot's runbook below; factory/release-snapshot-leaks-a-fixture-with-a-user-path-and-brand-fixtures-naming-private-projects.
- **Budgets are shown, never enforced.** A `budget:` is "x of y" beside the spend; nothing refuses, pauses or waits on it until the owner decides otherwise. Spend is read from this machine's Claude Code transcripts, plus Codex's own session logs for the split by subscription (rollout files only, never its login), numbers only, and the engine bundles no prices. Source: factory/department-budgets-what-each-department-spends-in-tokens-and-money-shown-in-its-room#D-2, #D-3 (N-18); Codex logs: factory/route-work-between-claude-and-codex-subscriptions-ccr-pick-the-agent-per-task-fall-back-when-one-hits-its-limit#D-4 (provisional).
- **Business data sits with the work files.** Goal values, outputs and connector checks are logs under the issues dir (`.goals/`, `.outputs/`, `.connectors/`), so a private tracker keeps them private; the viewer holds no business state of its own. Device state is the exception: phone-notification subscriptions, VAPID keys and their settings live in the state dir's `push/` (0600), because an endpoint is a capability that must not travel in a tracker repo (factory/phone-notifications-web-push-from-the-viewer-when-something-new-waits-on-the-owner-installable-as-an-app-keys-generated-locally#D-1, provisional). Sources: factory/department-role-and-goals (values log), factory/department-outputs-shelf#D-1.
- **One waiting-on-you count.** Every "waiting on you" number is the length of `needsYouRows` (`needs_you.go`). Anything department-level that waits on the owner (outputs to review, due jobs, a failing connector) adds rows there; no page counts its own waits on top. Sources: factory/needs-you-list-every-waiting-item-in-one-place-with-approve-all, factory/department-outputs-shelf#D-3.
- **Box keys.** The boxes on the company page and in connections are `hq`, `product`, a department's key and a group's key (`tracker.DepartmentKey`: the name slugified). Use these keys in `requested_by:`, `data-box` and JSON, not display names. Source: factory/connections-between-boxes-requests-outputs-and-dependencies-drawn-between-departments-on-the-floor-map#D-3.
- **Only real data, honest empty states.** No mock or sample data on a page; a box with nothing in it says so and names the command that would fill it. Sources: factory/global-screens (rules for all Workshop Board screens), the connections issue above.
- **Bootstrap mode reads projects.yaml.** Department config lives in `projects.yaml`, so anything issue-cli reads about departments must also work with no `--project`, run from a project root. Test that path. Source: factory/department-role-and-goals (the playtest found the bug; fixed in `cmd/issue-cli/helpers.go`).
- **Writing projects.yaml keeps it byte for byte.** A feature that edits `projects.yaml` splices only its own lines (comments and order kept), reads the result back before an atomic write, and never overwrites a file. The viewer reads `projects.yaml` at start, so a viewer write reloads the project (`reloadProject` in `dept_add.go`). Sources: factory/department-templates-add-a-department-to-any-project#D-2, ui/the-company-pages-new-department-tile-adds-a-department-from-a-template.
- **Department lint: `departments`, plus `department` for workflow files.** New warnings about department config in `projects.yaml` (prompts, goals, groups, outputs, connectors, files a gate waits on) go in the `departments` check. `department` covers only a department's own workflow file: it doesn't load, it has a project-wide key, or an issue's status isn't in it. Two checks exist because they read different files, and `--check department` is already in use; don't add a third for department config. (`owner-only`, which also reads `sends:` connectors, is about YOLO gates in any workflow, not department config.) Source: factory/departments-the-settled-rules-written-down-once#D-1.
- **New departments come from templates.** No empty custom department; groups, templates and channel types are generic engine pieces every project can add. Anything a department needs that the pieces don't give is fixed in the engine for every department, not as code for that one. Sources: factory/factory-departments-floor-map-and-capabilities#D-9, #D-10, factory/marketing-pilot-seo-as-the-first-department#D-5.
- **Groups nest up to 3 deep.** A group is a `departments:` entry; children name it with `parent:`, and a group may sit in a group, at most 3 groups above any department (`tracker.MaxGroupDepth`); a loop or a deeper chain is a lint finding and that department counts as in no group. Its lead is the lead of leads; routing walks department lead → each group's lead up the chain → CEO, first live. Sources: factory/factory-departments-floor-map-and-capabilities#D-5, #D-6, factory/department-groups-a-parent-with-a-lead-of-leads-gtm-over-marketing-and-sales#D-1, #D-2, factory/nested-groups-a-group-inside-a-group-with-goals-rolling-up-and-the-map-drawing-it#D-1, #D-2.
- **Channels are types, paired with a system of the same name.** SEO, PPC, social and cold outreach are types in Marketing's workflow file, not departments. Each channel type has a system of the same name (`seo-pass` ↔ `SEO`); `create --type seo-pass` doesn't set `--system` for you, so pass both. Sales is a department of its own, from the `sales-b2c` template ([Sales (B2C)](#sales-b2c)). Sources: factory/factory-departments-floor-map-and-capabilities#D-7, #D-8, factory/marketing-channels-as-types-seo-first-room-for-ppc-social-and-cold-outreach#D-1.
- **A template's workflow change reaches tests outside the template.** Changing a bundled department template's `workflow:` or statuses also breaks tests and playtest sessions that expect it (`dept_add_test.go`, `cmd/issue-cli/cmd_department_test.go`, `playtest/sessions/department-templates.yaml`, the scenarios): grep for the old name and run `make validate` and `make playtest-check`. Source: retro of factory/marketing-channels-as-types-seo-first-room-for-ppc-social-and-cold-outreach.
- **Engine requests go via the CEO.** A department that needs the engine changed files the request (`requested_by:` filled); the CEO forwards it to Product. Declared as `{from: <dept>, to: product, via: hq}` under `connections: requests:`, lint warns on a direct route. Sources: factory/marketing-brand-foundation-brand-analyst-icp-and-tamsamsom-before-channel-work#D-8, factory/declared-connections-who-may-ask-whom-and-what-feeds-what-with-routing-rules#D-6.
- **Nothing goes out without the owner's stamp.** Outputs start in review; only the owner approves; an agent publishes only an approved one. A step that publishes, sends or spends sits behind an `owner_only` gate, which YOLO never passes ([YOLO Mode](Workflow/yolo.md#gates-yolo-never-passes)). A job's standing start is off until the owner turns it on with their words. Sources: factory/department-outputs-shelf#D-2, factory/department-connectors#D-6, factory/department-recurring-jobs#D-3, factory/marketing-pilot-seo-as-the-first-department#D-4, workflow/an-owner-gate-yolo-never-passes-spending-money-and-publishing-outside.
- **Owner acts are the owner's: workers refused, the lead relays `--quote`, the viewer checks `fromOwnerBrowser`.** Stamping a gate (`issue-cli approve`), reviewing an output, ticking an owner box or an owner step (`owner-step done`), overriding the critic, answering a clarify round or a challenge, and every change to a hard decision are owner acts. In issue-cli a worker is refused (the worker guard's identity); a lead may run the command only with the owner's words in `--quote`, which the tool records in a comment it can't check; the owner's own shell may. In the viewer only the owner's signed-in browser may (`fromOwnerBrowser`, the `iv_owner` session, the O routes in docs/API/security.md); any other caller gets a 403 that names the CLI. A new owner act follows the same three paths; don't invent a fourth. Sources: `cmd/issue-cli/cmd_approve.go`; factory/department-outputs-shelf#D-2; cli/agents-can-tick-owner-boxes-through-issue-cli-check-and-prove-run#D-1; api/owner-only-routes-need-an-owner-credential-on-loopback#D-1, #D-2.
- **A human starts the work.** Every worker, lead and job run starts from the owner's ▶ or the owner's words quoted (`dispatch --quote`), or from a standing start the owner turned on (auto-start, a job's `auto on`); a worker never starts an agent or a tmux session ([A human starts the work](agent-dispatch.md#a-human-starts-the-work)). The one exception is an agent that is part of a run the human already started and can change nothing by itself: the fresh critic, a one-shot headless read-only subprocess inside the worker's session (factory/a-fresh-critic-that-never-wrote-the-work-a-read-only-agent-given-only-paths-findings-blocksshouldnote-with-fileline-blocks-hold-the-gate#D-4), and the post-mortem at close, started by the transition into `done` with `postmortem: start: auto` and changing nothing until the owner agrees (factory/post-mortem-actions-each-lesson-to-one-place-only-if-a-fresh-agent-would-act-differently-applied-after-the-owner-agrees#D-1, #D-10, #D-11). A design that needs another exception asks the owner. All provisional.
- **Lint checks that read issues: `undated`, `na-boxes`, `owner-marker`, `after-install`, `show-footer`, `criteria`, `types`, `reports`, `decisions`** (and `department` for an issue's status). They run once per board (`boardChecks` in `internal/lint/lint.go`); the rest read workflow files and `projects.yaml`. A new check about issue bodies or boxes goes beside them and in `boardChecks`. Source: retro sweep of 2026-10-31.
- **A pilot is rehearsed on a sandbox; go-live comes later.** A department pilot is first run end to end on a throwaway project with placeholder sites and stand-in connectors (a guided check script); going live on the real project follows from a runbook once the owner gives the repo and connectors. Source: factory/marketing-pilot-seo-as-the-first-department#D-2.
- **A red newcomer walkthrough blocks shipping.** `TestNewcomerWalkthrough` (`newcomer_test.go`) builds both binaries and walks a clean HOME and PATH from `project init` to a first dispatch, `department add` and `lead spawn` on a private tmux server. `make validate` runs it (`make test` is plain `go test ./...`; it skips only under `-short`, without tmux or git, off Linux/macOS, or with `i3-msg` on PATH). When it fails, the branch doesn't ship: fix the step a newcomer would hit, don't skip the test. Onboarding work walks the guarded steps (dispatch, department add, lead spawn) in that test. The briefing's rule: "The worker guard refuses issue-cli dispatch, department add and lead spawn from your shell, even with --config on a sandbox projects.yaml (it knows you by environment, tmux name and worktree): walk those steps in a Go test with a clean env and a private tmux server, as TestNewcomerWalkthrough (newcomer_test.go) does. Never strip the guard's environment." Source: retro of factory/a-friend-forks-it-a-fresh-clone-to-a-first-issue-on-a-clean-machine-and-every-step-that-breaks-fixed; factory/the-newcomer-walkthrough-test-guards-main-factorys-settled-rules-call-it-a-blocker-and-onboarding-briefs-say-which-steps-the-worker-guard-keeps-in-a-go-test.
- **Research watches memory.** Research steps carry the [heavy-tools line](#running-partners-on-this-machine); tests use fixtures, never the live web. Source: factory/marketing-brand-foundation-brand-analyst-icp-and-tamsamsom-before-channel-work#D-2, #D-7.

## Config

```yaml
projects:
  - name: Orchard
    issues: ./issues
    company:                              # HQ
      mission: "Grow the clients' local search traffic"
      prompt: departments/hq/role.md      # the CEO's role prompt
      goals:
        - id: clients
          name: Paying clients
          target: 20
          period: quarter
    departments:
      - name: Marketing
        color: "#E2559B"
        role: SEO and content for the client roster   # one line, on the room
        mission: "Every client site refreshed every week"
        prompt: departments/marketing/role.md
        systems: [SEO]
        goals:
          - id: pages
            name: Client pages refreshed
            target: 40
            period: week
            source: file:docs/seo/run.json#pages_refreshed
            rolls_up: clients
      - name: Product                     # optional: Product's mission, prompt, goals
        prompt: departments/product/role.md
        goals:
          - {id: bugs, name: Open bugs, target: 5, direction: down}
```

- **`company:`**: HQ's `mission`, `prompt` and `goals`.
- **`departments:`**: each takes `name`, `color`, `role`, `systems` as before, plus `mission`, `prompt` and `goals`.
- **The `Product` entry** gives Product a mission, prompt and goals. It does not change which issues are Product's, and its `systems:` are ignored (lint warns).
- **`prompt:`** is a path relative to the project root (`workdir:`, else the issues folder's parent).

### A goal

| Field | Meaning |
|---|---|
| `id` | Required. With the owner it makes the goal's key: `hq/clients`, `product/bugs`, `marketing/pages` (the department's name in lower case, spaces as `-`) |
| `name` | Shown instead of the id |
| `target` | The number to reach. Without one the value shows with no bar |
| `unit` | Shown with the value; `pct` shows as `%`, `count` as a plain number, anything else after the number (`400 EUR`) |
| `period` | `week` (from Monday), `month`, `quarter` or `none` (the default). The value shown is the newest; one from before the current period is **stale** |
| `direction` | `up` (the default: more is better) or `down` (fewer is better) |
| `source` | `manual` (the default) or `file:<path>#<metric id>`; a path starting with `<issues>/` is read from the issues folder ([a job's metrics file](#goals-a-job-fills-from-a-connector)) |
| `rolls_up` | On a department or Product goal: the HQ goal id it counts towards |

## Where the numbers come from

- **`manual`**: `issue-cli goal set <key> <value> [--note "…"] [--date YYYY-MM-DD]` adds a line to `<issues>/.goals/values.jsonl`:
  ```json
  {"goal":"marketing/pages","value":38,"date":"2026-10-29","note":"two sites left"}
  ```
  The log sits with the work files, so a private tracker (`privacy.work_files: tracker`) keeps business numbers out of the code repo along with the issues. The newest entry by date wins.
- **`file:<path>#<id>`**: reads metric `<id>` from a JSON file in the [metric run format](workbench-files.md#metric-run-metricsprobe_results) the Metrics view already reads. A script or a recurring job writes one file, and both views understand it. The date is `run.date` (or a top-level `date`):
  ```json
  {"run": {"date": "2026-10-28"}, "metrics": [{"id": "pages_refreshed", "value": 30}]}
  ```
  A file not written yet reads "no value yet: <path> is not written yet" (a job that writes it hasn't run), and lint's `departments` check gives one finding per missing file, naming every goal that reads it. A missing id or a value that is not a number shows on the goal as an error. The page and the commands still work.
- **`file:<issues>/.goals/<dept>/<job id>.json#<id>`**: a recurring job's metrics file, the next section.

### Goals a job fills from a connector

The pattern for goals that come from outside numbers (analytics, payments): a [recurring job](#recurring-jobs) reads them through a [connector](workflow-stats.md) and writes one metrics file, and the department's goals read it. The sales-b2c template's weekly funnel review is the first.

1. **The job reads the numbers**, each from a connector (`issue-cli connector exec <dept>/<name> -- …`, the secret only in the child) or from the owner's numbers on the run's issue. Never an estimate: a number it can't read is left out.
2. **The job writes them** with `issue-cli goal write <dept>/<job id> <id>=<value>… [--merge] [--date YYYY-MM-DD]`. It replaces `<issues>/.goals/<dept>/<job id>.json` whole, in the metric run format, atomically (a temp file beside it, then a rename: a reader never sees half a file), and prints which goal reads each id, warning about an id no goal reads. It refuses an unknown job, a value that is not a number, a bad date and no metrics. A job run works in its own worktree and a report run never commits, so a file written in the checkout would never reach the goals; `goal write` finds the issues folder from anywhere, and the numbers stay with the work files (a private tracker keeps them private).
   ```json
   {"date": "2026-11-02", "written": "2026-11-02", "job": "sales/weekly-funnel", "metrics": [{"id": "visits", "value": 512, "date": "2026-11-02"}, {"id": "signups", "value": 31, "date": "2026-11-02"}]}
   ```
   Each metric carries the day it is for (`--date`, default today), and a goal dates its value by it before the file's `run.date` or `date`. **Two scripts, one file:** `--merge` sets only the named metrics and keeps the file's others with their own dates (`kept: visits (2026-11-02)`), under a lock on the folder so two merges at once don't drop each other's ids; a merge into a file it can't parse refuses and says to rerun without `--merge`. A whole write names the ids it dropped (`dropped: signups`). `written` is the day `goal write` ran, not `--date`: the run stamp a gate can compare with today.
3. **The goals read it**: `source: file:<issues>/.goals/sales/weekly-funnel.json#visits`. The file's name ties it to its job (department `sales`, job `weekly-funnel`).
4. **A stale value says so.** When a metric's date (or the file's, for a metric without one) is before the day the run *before* the latest due one was due (a whole run missed: for a Monday job, on Thursday, a file from before last week's Monday), the goal shows **"no value since <date>"** instead of the number, has no progress and drops out of the roll-up's mean; `issue-cli goals` adds "job <key> missed a run since <date>", the company page mutes it and its tooltip names the job. A run that is only late inside the current period keeps the number, marked stale by `period:` as any goal. Before the first run the goal reads "no value yet: <path> is not written yet (job <key> writes it …; next run <when>)". A `goal set` on such a goal is refused and points at `goal write`.
5. **Lint (`departments`)** names a job-file path whose department has no such job ("names no job sales/stripe-daily"), a path that climbs out of the issues folder, and a file not written yet, with the job that writes it on its first run.

A job's workflow can hold the run until the file is there with a `file_exists` gate on the same path: `path: "<issues>/.goals/{{department}}/weekly-funnel.json"` (`<issues>/` is read from the issues folder there too).

**An entry installed before this** (the sales-b2c goals reading `departments/sales/funnel.json`): change each goal's `source:` in projects.yaml to `file:<issues>/.goals/sales/weekly-funnel.json#<id>` and, in `workflows/sales.yaml`, the `funnel-review` gate's `path:` to `"<issues>/.goals/{{department}}/weekly-funnel.json"`; `department update sales` brings the role and the brief along (a gate path is not wording, so it leaves the workflow file to you).

## Progress and the roll-up

- **Progress** is `value / target` for `up`. For `down` it is 100% at or below the target, else `target / value`. It is clamped to 0–100%.
- **The roll-up:** an HQ goal with a value of its own shows that value. Without one (manual and nothing recorded), its progress is the **mean progress** of the department goals whose `rolls_up:` names it, counting only those that have a progress. Units are never added together: a count of pages and a sum of euros only meet as percentages.
- **Through a group:** a department under a [group](#groups) may roll a goal up into one of the group's goals (`rolls_up: pipeline`) or straight into an HQ goal. The group goal follows the same rule (its own value, else the mean of its departments' goals), and counts in HQ's mean like any department goal.

## Work behind a goal

A goal nobody is working on stays where it is. An issue names the goal it serves in a `goal:` field: the goal key (`hq/clients`, `marketing/pages`) or a bare id when only one owner has it. `issue-cli create --title "…" --goal <key>` writes it (`set-meta --key goal` changes it), and an idea in the lead's queue takes it too: `issue-cli queue add --idea "…" --goal <key>`. An unknown or ambiguous key is refused with the keys the project has.

- **Covered** is a goal with an open, unparked issue whose `goal:` names it; an active queue item whose `goal:`, or whose issue's, names it; a job that fills it (its `source:` is the job's metrics file, [above](#goals-a-job-fills-from-a-connector), whether its standing start is on or not); or, for an HQ or group goal, a covered goal rolling up into it. An open epic is covered by an open, unparked child or an active queue item for it or a child. Closed and parked issues and done or dropped items are no work.
- **Lint** (`goal-without-work`, [the lint family](workbench-files.md#the-lint-family)) warns once per goal or epic with nothing behind it, with the command that adds work. A project with no goals at all gets one note (no warning: a new project starts clean), fixed by the owner's `department update hq --goal "<id>=<name>/<target>/<period>"`. Choosing goals stays the owner's.
- **The out-of-work line** names them as the places to look first: `queue list` under its `∞` refill line (`goals_without_work` in `--json`), `queue next` when only the refill line is left, the lead briefing's `Queue:` line and the lead tick's `queue: nothing open left` line add `look first at: hq/clients (Paying clients), epic <slug>`, five at most, then `+N more: issue-cli health lint --check goal-without-work`; a project with no goals adds `no goals set`.

Source: factory/lint-and-the-lead-tick-name-a-project-goal-with-no-open-issue-queued-item-or-job-behind-it (D-1, D-2, provisional).

## Where it shows

- **Company page** (`/p/<project>/company`, the floor: one fixed map with an inspector rail, [UI](UI/overview.md#the-company-floor); the page described in this document's other entries stays at `?classic=1`):
  - **The header** shows HQ's first two goals, value / target with a bar in the project's colour.
  - **On the floor** a tile shows its goals when the floor fits at more than 1.3x (a wide screen, a small company); a room's inspector (click its name) lists every goal, job, output, connector and handoff the tile has no room for.
  - **HQ** shows its mission, then each HQ goal with the department goals rolling into it, in their department's colour.
  - **Each department room and the Product line** show the mission and the first three goals.
  - A stale or unreadable goal is muted, and its tooltip says why.
- **Project map** (`/`, the lot of each project): the bar under the lot is HQ's first goal, else the first goal of Product or a department (`Goal: Marketing · Leads`), with value / target. A goal with no value yet reads "no value yet" and its tooltip gives the `goal set` command. A project without goals keeps the open epic furthest along.
- **`issue-cli goals [<dept>] [--json]`**: the roll-up as text. Each HQ goal is followed by the goals under it (`↳ Marketing: …`), then each department's goals, with value, target, progress, period, date (`stale, from …`), source, and the error or the `goal set` command for a goal with no value yet. With nothing configured it prints the config snippet.
- **`issue-cli department show <name> [--json]`**: the mission, role, the role prompt's path and content, the goals, and its [spend](#spend-and-budgets) this week. `<name>` is `hq`, `product` or a department. A department with its own workflow file also gets `Workflow: workflows/marketing.yaml · its hall (stations, every type's path, Edit): <viewer>/p/<project>/hall?department=marketing` (`workflow`, `hall_url` in JSON).
- **Its hall** (`/p/<project>/hall?department=<name>`, [UI](UI/overview.md#a-departments-hall)): a department with a workflow file gets the same hall as Product: its stations and gates, every type's path with its gates and owner_only marks, and the station Edit form. A file git does not track is written in place and never committed.
- **`issue-cli start` / `show`** print one line for an issue whose department has a role prompt or goals:
  ```
  Department: Marketing (Every client site refreshed every week) — role: departments/marketing/role.md · goals: issue-cli goals marketing
  ```
- `issue-cli` run from a project root without `--project` takes the departments, `company:` and the Product entry from the matching `projects.yaml` entry, so agents there see the same goals.
- **`issue-cli health lint`**, check `departments`: warns about a prompt file that does not exist, `rolls_up:` naming no HQ goal (for a department under a group: no group goal and no HQ goal; or set on an HQ goal), a `parent:` that names no department, the department itself, HQ or Product, a `parent:` chain that loops or has more than 3 groups ([Nested groups](#nested-groups)), `parent:` on the Product entry, a duplicate goal id or a goal with none, an unknown `period:`, `direction:` or `source:`, a file source whose file is missing, `systems:` on the Product entry, and a `budget:` that doesn't parse ([Spend and budgets](#spend-and-budgets)).

## Outputs shelf

Every department, Product included, has a **shelf**: what it made, such as a post, a design, a refreshed page, a report or an invoice, with the issue or job that made it and the owner's review. Nothing needs configuring. The shelf shows once something is on it.

### Recording an output

An agent records what it made:

```
issue-cli output add --issue seo/acme-weekly --title "Acme: FAQ refresh" --kind page --url https://acme.example/faq
issue-cli output add --department marketing --job weekly-pass --title "Hero banner" --kind design --file out/hero.png
issue-cli output add --title "October report" --kind report --path docs/reports/2026-10.md
```

- **Where it points**: exactly one of `--url` (an http(s) link), `--file` (copied onto the shelf, under `<issues>/.outputs/files/<dept>/`, up to 25 MB) or `--path` (a file in the repo, relative to the project root; it must exist). A worker's `--path` that the project's checkout doesn't have yet but its issue's worktree does (the file is on a branch not merged yet) is read in that worktree, and the output records the branch (`branch`, `worktree` in the log; the shelf row says `(branch <name>)`); once the branch is merged the file is read from the checkout again. A path on the checkout behaves as before. `issue-cli output list` prints the shelf as `issue-cli outputs` does. A file or path that later disappears shows as **missing**.
- **Which shelf**: the department of `--issue`, else `--department`, else Product. A `--department` that disagrees with the issue's is refused.
- **A second version**: `output add` on an issue that already has a file output with the same title (any case) still adds, and hints `output revise <id>` with the older output's id (on stderr with `--json`). A published or non-file output gets no hint: revise would refuse it.
- **`--kind`** is any word. `post`, `design`, `site`, `page`, `report`, `invoice` and `video` get an icon; anything else shows 📦.
- **Videos**: `--kind video --file <x.mp4|x.webm>` puts a video on the shelf, up to 200 MB (every other file stays at 25 MB; over it, the refusal names the file's size, the limit and an ffmpeg command for a smaller cut, or `--url`). Other formats are refused with the ffmpeg command that converts them, and `--kind video --path` is refused (only shelf files are served). When `ffmpeg` is on PATH, output add and output revise write a poster next to the file (`<file>.poster.jpg`, the frame at 1 s, 640 px wide); without ffmpeg the add goes through and prints why there is none. The shelf row shows the poster (or 🎬) and opens the reader, which plays the video where a document's sheet would be: Approve / Reject, the raw file, `Previous: v<n>` for a revised video, and comments on the whole video (the owner writes the time). The issue's Station plays it under the output's row. Any shelf file ending in .mp4 or .webm gets the player, whatever its kind. The viewer never runs ffmpeg; `…/shelf/file/` serves the video with its media type and Range support (206) under the same sandbox headers as every shelf file.
- **`--job`** names the job that made it when no issue did. A [recurring job](#recurring-jobs)'s run is an issue, so its outputs use `--issue`. **`--note`** adds a line.

Each output is one line in `<issues>/.outputs/outputs.jsonl`, an append-only log next to the goals log, so a private tracker keeps outputs private too. Reviews and publishes are further lines with the same id, and the shelf folds them:

```json
{"op":"add","id":"o-20261030-a1b2","date":"2026-10-30T07:12:00Z","department":"marketing","title":"Acme: FAQ refresh","kind":"page","url":"https://acme.example/faq","issue":"seo/acme-weekly","by":"agent-seo-acme-weekly"}
{"op":"review","id":"o-20261030-a1b2","date":"2026-10-30T08:01:00Z","state":"approved","quote":"approved on the shelf","how":"on the shelf","by":"owner"}
{"op":"publish","id":"o-20261030-a1b2","date":"2026-10-30T09:30:00Z","url":"https://acme.example/faq","by":"agent-seo-acme-weekly"}
```

Commands take the full id or its last part (`a1b2`).

### Review: the owner's stamp

An output starts in **review**. Only the owner decides:

- on the shelf page, with **Approve** or **Reject** on the row;
- or through the lead: `issue-cli output review <id> approve|reject --quote "<the owner's words>"`. Workers are refused, as with `issue-cli approve`.

`issue-cli output publish <id> [--url <where it went out>]` marks an output **published**, and only once it is approved. On the content workflow, the Draft box asks for `output add`, and the Published box asks for `output publish`. The issue's own `owner review → publish` gate still stamps the issue as a whole. The shelf stamps each piece, so the owner can approve eight of ten pages a weekly pass made. On the bundled `marketing` workflow the two stamps agree: `owner review → publish` runs `outputs_approved`, so it waits while any of the issue's outputs is still in review, a revised one included.

### Where it shows

- **Company page**: each department room and the Product line show `📦 N to review · M made this week`, linking to their shelf. HQ shows the outputs to review across departments and the newest five.
- **The shelf** (`/p/<project>/shelf`, `?dept=marketing&state=review`): tabs by department and state with counts, and a row per output: icon or image thumbnail, title linked to the URL or the shelf file, department, kind, the issue (to its station) or job, who, when, state, and Approve / Reject for those in review. Shelf files are served only from `.outputs/files`, under a sandbox CSP.
- **The station** (the issue page) lists the outputs the issue made.
- **Waiting on you**: a department with outputs in review is one item in the company panel, on the Status page (the Waiting card links to the shelf), in `status.md` and in the sidebar count.
- **CLI**: `issue-cli outputs [<dept>] [--state review|approved|rejected|published] [--issue <slug>] [--json]` lists the shelf by department. `issue-cli output show <id>` shows one output. `issue-cli start` / `show` print `Outputs: 2 (1 to review)`, or on a department or content-workflow issue that made nothing yet, the `output add` command. A transition into `owner review`, `publish` or `done` on such an issue prints the same hint. It is a hint, not a gate.
- **Reading and commenting** (markdown outputs): the title of a `.md` file or path opens the **reader**, `/p/<project>/shelf/read/<id>` (the raw file stays one click away as **Raw file**). The document is rendered (headings, tables, task lists; raw HTML dropped; a leading YAML frontmatter block shows as a metadata table above the article, its `title:` the title when the file has no `# ` heading) with a contents column and a Day / Night switch for the sheet. The owner comments in the margin: select a passage (a **Comment on this passage** button appears), use 💬 on a heading, or write on the whole document; threads take replies and **Resolve**. Comments live beside the file in `<issues>/.outputs/comments.jsonl` (never in it), each anchored by the quoted passage, 30 characters either side and its heading, and each owner comment or reply is posted on the output's issue as `revise: output ab00 v1 · Brand › Proposal · “…” — <text> (thread c-1a2b · issue-cli output comments ab00)`. Only the owner's browser writes them (`fromOwnerBrowser`; anything else gets a 403 naming the CLI). An agent reads them with `issue-cli output comments <id> [--all]` (where each thread sits in the newest version), answers with `issue-cli output reply <id> <thread> --text "..."`, and adds the version that answers them with `issue-cli output revise <id> --file <file> [--note]`: a copy next to the earlier ones, the output back in review. The reader then shows **Changes since vN-1** (a line diff), keeps each thread on its passage when the passage survived, puts it under its heading when only the heading did, and folds it under "not in vN" otherwise. The shelf row shows the version and `💬 N open`; `outputs`, `output show` and the issue's `Outputs:` line show the open count and the command.
- **`issue-cli health lint`**, check `departments`: warns about log lines it skipped, a file or path that is gone, and an output in review for more than 14 days. Outputs of a department `projects.yaml` no longer has are listed as history (an info line, no warning), and so are decisions scoped to it.

## Department leads

Each department in `departments:` can have its own **lead**, reporting to the CEO. HQ is the CEO, and Product's work stays the CEO's: neither gets a department lead.

```
human
  └─ CEO                     lead-<project>             project checkout
       ├─ Product workers    agent-<slug>
       └─ Marketing lead     lead-<project>-marketing   project checkout
            └─ workers       agent-<slug>               Marketing's issues
```

- **Starting it**: the human starts it, like every agent. Use **▶ Start lead** in the department's room on the company page, **Start the Marketing lead** in the agents canvas panel, or `issue-cli lead spawn --department marketing` (the CEO runs it with the human's words). It starts through the same launcher as the CEO (`lead_terminal`, `--headless`), and a live lead is reattached, never started twice. `issue-cli lead spawn` on a project with departments ends with a tip naming them. `product` and `hq` are refused (that's the CEO), as is a name not in `departments:`.
- **Its brief**: who it is and that it reports to the CEO; the department's mission, role line, workflow file and statuses; its issues (`issue-cli list --department <key>`), goals and shelf commands; the role prompt file inlined; then the CEO's lead rules (dispatch and approve only with the human's quoted words). It writes its own handoff note, `sessions/YYYY-MM-DD-lead-<project>-<dept>.md`. The session exports `ISSUE_VIEWER_LEAD_SCOPE=department:<key>`.
- **Scope**: `issue-cli dispatch` and `issue-cli approve` from a department lead refuse an issue of another department or of Product, naming the CEO. The CEO keeps authority over everything.
- **Who a worker reports to**: a department issue's worker reports to its department lead while that session is live. Otherwise the CEO covers for it, and the briefing says so with the spawn command. The route is derived from the live sessions, never stored. `issue-cli show` / `start` print `Lead: lead-acme-marketing (live) · CEO: lead-acme (live)` for a department issue (Product issues keep `CEO: …`).
- **The worker's briefing** gains `## Your department`: the mission, the role line, the workflow file, the goals command, the `output add` command, and the role prompt inlined (up to 8 KB; the rest is in `issue-cli department show <key>`). Product's issues get none.
- **CLI**: `issue-cli lead whoami` shows the department lead role, its scope and the CEO it reports to. `issue-cli lead list` lists the department leads (live or not, open issues). `issue-cli lead message department:<key> "…"` reaches one (Codex sessions only, as for the CEO).
- **Viewer**: the agents canvas draws each department lead under the CEO with its department's workers (tree and radial). The company page's room shows the lead at its desk: **Talk to the Marketing lead** when it is live, **▶ Start lead** when it is not. `/p/<project>/lead?dept=<key>` is its chat (the CEO chat page, its side panel listing only the department's workers; an unknown department is 404). `/leads` lists each project's department leads under its CEO.
- **Not here**: the L2 hall stays Product's floor; a department's stations and agents show in its room. Its connectors are listed in `## Your department` ([Connectors](#connectors)).

## Groups

A **group** is a department other departments sit under: GTM (go-to-market) over Marketing and, later, Sales. A department joins one with `parent:`; the group is an ordinary `departments:` entry, so it has everything a department has (mission, role prompt, goals, lead, shelf, jobs). Its colour is the exception: every group, nested ones too, is drawn in the group teal (the `--group` token in `static/workshop.css`, `companyGroupColor` in map.go) so it never reads as HQ's yellow, and a `color:` on a group is ignored. A group may sit inside another group ([Nested groups](#nested-groups)).

```yaml
    departments:
      - name: GTM
        mission: "A steady pipeline of new clients"
        prompt: departments/gtm/role.md
        goals:
          - {id: pipeline, name: New client conversations, target: 8, period: month, rolls_up: clients}
      - name: Marketing
        parent: gtm                       # the group's name or key
        goals:
          - {id: pages, name: Client pages refreshed, target: 40, period: week, rolls_up: pipeline}
```

```
human
  └─ CEO                        lead-<project>
       └─ GTM lead              lead-<project>-gtm         the lead of leads
            └─ Marketing lead   lead-<project>-marketing
                 └─ workers     agent-<slug>
```

- **The lead of leads** is the group's department lead, `lead-<project>-gtm` (`issue-cli lead spawn --department gtm`, or ▶ Start lead in its room). It reports to the CEO. It dispatches and approves the group's own issues and its departments', with the owner's words like any lead, and nothing else (`issue-cli dispatch` / `approve` refuse the rest and name who to ask). It may also start its departments' jobs. Its brief adds `## Your departments`: each department with its lead's session and whether it is live, mission, and its list and goals commands.
- **Reporting:** a department lead under a group reports to the group's lead while it is live, else to the CEO. A worker on a department issue goes to its department's lead, else the group's lead, else the CEO, taking the first that is live. The worker's briefing names the chain ("You report to the Marketing lead …, who reports to the GTM lead …, under the CEO …", or the GTM lead covering). `issue-cli show` / `start` print `Lead: … · GTM lead: … · CEO: …`.
- **CLI:** `issue-cli lead whoami` shows a group lead's departments and a department lead's group and who it reports to right now; `issue-cli lead list` shows each group's department leads indented under it; `issue-cli goals` heads a department under a group `(in group gtm)`; `issue-cli department list` marks a group (`gtm  GTM (group: marketing; can join later: sales-b2c)`) and a department in one (`· in group GTM`).
- **Viewer:** the company page draws a group as one room with its lead, goals, jobs and the count of its and its departments' issues waiting on you, with its departments' rooms inside. The map draws it as one building with theirs inside. The Crew page's group column holds its lead and its departments' columns. `/leads` lists the group lead with its department leads under it.
- **Adding one:** `issue-cli department add gtm` installs GTM from the bundled group template, then Marketing under it from its own template ([Templates](#templates)). GTM comes with the `planning` workflow (`workflows/gtm.yaml`) and the `weekly-plan` job ([The week plan and report](#the-week-plan-and-report)). `department add <template> --parent gtm` puts a department under a group you have; the company page's New department tile asks "Under which group?" once the project has one. `department remove gtm` is refused while departments sit under it.
- **Asking its own departments:** a group may file work straight into its departments (`requested_by: gtm` on a Marketing issue). That is never off route, even when the project declares `connections: requests:`; anything else follows the declared routes.

### Nested groups

A group may name a parent group, so a company grows an org chart: GTM over Outbound and Marketing, Outbound over Sales. At most **3 groups** sit above any department (`tracker.MaxGroupDepth`). Source: factory/nested-groups-a-group-inside-a-group-with-goals-rolling-up-and-the-map-drawing-it#D-1, #D-2.

```yaml
    departments:
      - {name: GTM, goals: [{id: pipeline, target: 10, rolls_up: clients}]}
      - {name: Outbound, parent: gtm, goals: [{id: meetings, target: 10, rolls_up: pipeline}]}
      - {name: Sales, parent: outbound, goals: [{id: calls, target: 10, rolls_up: meetings}]}
      - {name: Marketing, parent: gtm}
```

- **Goals** roll up level by level: Sales's calls into Outbound's meetings, Outbound's meetings (its own value, else the mean of its departments') into GTM's pipeline, the pipeline into HQ. Spend and the waiting-on-you count of a group's room cover every department under it, at any depth.
- **Leads:** a department lead reports to its group's lead while it is live, else the next group's up the chain, else the CEO; a worker's issue goes to the first live lead on that chain. A group lead dispatches and approves its own issues and those of every department under it. The brief names the chain; a group's `## Your departments` marks a sub-group and who reports to its lead. `lead whoami` prints `Group: Outbound in GTM (their leads … come first while live, nearest first, …)`; `lead list` indents each level.
- **Routing:** a group asks any department under it straight. `via: <group>` may name any group above the asker; a route still names one forwarder (no multi-hop `via:`).
- **Viewer:** the company page draws a nested group's room inside its group's room, the map draws its building inside the group's building (the lot still fits 300px), Crew nests its column and /leads its list.
- **Adding:** `department add <template> --parent outbound` puts a department under a nested group; a group template may take `--parent` too. Either is refused when a department would end up more than 3 groups deep ("under X, the department would sit 4 groups deep …"), or when the parent's own chain is broken.
- **Lint** (`departments`): `parent: chain loops (A → B → A)` and `parent: chain is too deep (V → W → X → Y → Z)`; that department is treated as in no group until it is fixed.
- **Scenario:** `playtest/scenarios/nested-groups` and the `nested-groups` session.

### The week plan and report

Each week the lead of leads plans what its departments do, and the owner gets a report of what moved. Both are read from what is already on disk; nothing in them starts work.

**The pack:** `issue-cli department week <group>` prints the week for the group and each of its departments (`--json` for scripts). A plain department works too, covering only itself; `hq` and `product` are refused, because they have the Status page and the Report. For each department it shows:

- **Goals**, with how far each moved in the last 7 days (calendar days: the window starts at local midnight seven dates back, as every "this week" count does). For a manual goal, that is the newest value in the window against the newest before it. A goal read from a file shows its value only.
- **Outputs** waiting on the owner's review, and outputs reviewed or published in the window.
- **Jobs**, with each one's verdict when due and its last run.
- **Handoffs** in and out: open requests between boxes (`requested_by:`), and "waiting on <lead> to forward it" when a declared route's relay is pending.
- **What is blocked, and on whom:** `blocked_on:` (the issue and its department), a ready gate or an open `question:` (the owner), a pending forward (the CEO or the group lead).
- The run issue's `## Week plan` for this ISO week, when it has one.

**The report:** `--write` writes it to `<issues>/.reports/<group>/<ISO week>.md` (for example `2026-W44.md`). The file sits with the work files, so a private tracker keeps it private (Settled rule "Business data sits with the work files"). Writing the same week again replaces that week's file; other weeks are never touched. Its first line is a JSON comment with the week's counts: goals moved, outputs shipped (published) and reviewed, blocked. The room reads that line, so a past week is never recomputed.

**Where it shows:** the group's room on the company page has a **Week** block with the latest report's week and counts, whether it has a plan, and the file. It links to `/p/<project>/reports/<group>/<week>`, the report rendered with tabs for the other weeks. With no report yet, the block names the `--write` command. `issue-cli department show <group>` prints the latest report. A group lead's `lead whoami` and its brief name `department week`.

**The job:** the GTM template's `weekly-plan` job (Mondays 08:00, brief `departments/gtm/jobs/weekly-plan.md`) makes each week's run an issue in the group. The run walks the `planning` workflow:

```
gather → plan → report → done
```

1. **gather:** run `department week <group>`.
2. **plan:** write `## Week plan` in the run issue, with a `### <department>` heading each. File new work with `issue-cli create --department <dept>`: it lands at its first status and waits for the owner's start. Name a due job with the command the owner or its lead runs.
3. **report:** run `department week <group> --write` and post the path as a `progress:` comment.
4. **done:** the owner's approval, their read-through. YOLO passes it and queues it for review after the fact. It is not `owner_only`, because nothing in it publishes or spends.

**What never happens:**

- A run never dispatches, approves, runs a job, or moves any issue but its own. Workers are refused by `dispatch`, `approve` and `job run` anyway.
- A step that publishes or spends stays behind its own `owner_only` gate; the plan only names it.
- The run itself starts like any job: the owner's ▶ Start run or `job run --quote`, or the job's standing start once the owner turns it on (`issue-cli job auto on gtm/weekly-plan --quote "…"`). See Settled rule "Nothing goes out without the owner's stamp".

**A GTM installed before this:** add `workflow: workflows/gtm.yaml` (copy `cmd/issue-cli/templates/workflow/planning.yaml` there) and the job to the GTM entry in projects.yaml, plus a brief file:

```yaml
    jobs:
      - {id: weekly-plan, name: Week plan and report, every: week, on: mon, at: "08:00", brief: departments/gtm/jobs/weekly-plan.md}
```

The brief template is `cmd/issue-cli/templates/departments/gtm/jobs/weekly-plan.md` (`{{department}}` is filled with the group's key). `department week` works without any of this.

## Templates

A **department template** is a ready department: a workflow file, a role prompt and a goals skeleton, bundled with issue-cli (`cmd/issue-cli/templates/departments/<name>/`) like the workflow templates. **Marketing** (SEO and content, on the `marketing` workflow: the `content` statuses plus a `measure` step, with each channel a [type of work](#channels-as-types)) is the first. **Video** (a video studio driving an outside [partner](#partners), on the `video` workflow; [Video studio](#video-studio)) is the second. **Sales** (B2C: the offer, the landing page and waitlist, launches and a weekly funnel review, on the `sales-b2c` workflow; [Sales (B2C)](#sales-b2c)) is the third. **Legal** (compliance checks, draft documents and legal checks before a change ships, on the `legal` workflow; [Legal](#legal)) is the fourth. Product is built in: it is the `development` workflow every project runs.

```
issue-cli department list                    # HQ, Product and the templates, installed or not
issue-cli department add marketing --dry-run # the plan, nothing written
issue-cli department add marketing           # install it
issue-cli department add marketing --commit  # install it and commit the files it wrote
issue-cli department add marketing --mission "Acme's shop found on Google" --goal "visits=Visits/200/week"
issue-cli department update marketing --goal "posts=Posts published/4/week" --drop-goal pages
issue-cli department update marketing        # newer template text, only where you never edited
issue-cli department remove marketing
```

`department add <template>`:

- writes `workflows/<key>.yaml` (the template's workflow template, as `workflow init` writes it), `departments/<key>/role.md` and each job's brief as `departments/<key>/jobs/<id>.md`, relative to the project root. `<key>` is the department's name in lower case;
- adds the department to the project in projects.yaml, under `departments:` (creating the key when the project has none): name, colour, role, mission, systems, `prompt:`, `workflow:`, the goals, each manual with no value yet, and the template's [recurring jobs](#recurring-jobs). The mission and goals are the project's own when you pass them, else the template's samples, marked ([Your own mission and goals](#your-own-mission-and-goals)). Their standing start is off: a due run waits in Needs you until you start it or turn it on;
- ends with the next steps: `department show <key>`, `create --department <key> --title …`, `lead spawn --department <key>`, the workflow's types of work (`Types of work: piece (default), seo-pass`), which files to edit (the role prompt and each job's brief), and `issue-cli connectors <key>` (for a template without connectors, the config to add; for one with them, which keys are set);
- for a template with a `partner:` (Video), adds the `partner:` block with the path you chose (`--partner-path <dir>`, default the template's) and the pinned commit, its connectors with `{{path}}` filled in, and **prints the steps to install the partner there**. It never runs them: nothing is cloned, created or started, and the path need not exist. Lint's `departments` check says "path … is not a folder: install the partner there" until you have. `--json` carries them as `partner_path` and `install`; the viewer's preview shows them under **Install its partner yourself**;
- for a template with `connections:` (Sales), **prints the handoffs to declare** under the project's `connections:` in projects.yaml, leaving out the ones the project declares already, and never writes them: once a project declares any `requests:`, routing refuses every route not listed, so declaring them is the owner's change. `--json` carries them as `connections`; the viewer's preview shows them under **Handoffs to declare**;
- ends by naming the files it wrote that git does not track yet and the `git add … && git commit … -- <those files>` to run, so they aren't lost or swept into an unrelated commit. With `--commit` it commits exactly those files itself (`chore(departments): add the <name> department`), whatever else is staged; projects.yaml is never in that commit. Outside a git repository it says nothing (with `--commit`, that nothing was committed). `issue-cli health lint` warns about department files git does not track (check `untracked`: the role prompt, the workflow file, job briefs and everything under `departments/<key>/`; ignored files are your choice and not flagged).

What it never does:

- **overwrite a file.** One that is already there with the template's content is reported as already there; one that differs is **kept** and reported as your edited file. For a kept workflow file it prints the `workflow init --template <name> --out workflows/<key>.template.yaml` command that writes the bundled one next to yours to compare ([Upgrading an installed Marketing](#upgrading-an-installed-marketing)). Only the projects.yaml entry is added;
- **change the rest of projects.yaml.** The entry is spliced in as text, so comments, quoting and every other project stay byte for byte. The result must load and contain the department, else the file is left as it was. A symlinked projects.yaml is edited through the link;
- **install a template twice.** A template installs once, under its own name; that holds for a group template too. The project has it when a department has the template's name, or its workflow file is stamped with the template's workflow (a Marketing installed under another name before `--name` went away). The refusal is the same in the CLI and the viewer: "Marketing is already a department of <project> (<projects.yaml>): a template installs once. To add it again, remove it first: issue-cli department remove <key>";
- run from a worker session: it edits the owner's file. A lead runs it with the owner's words; `--dry-run` works for anyone.

### Your own mission and goals

A template's `mission:` and `goals:` are samples ("Your site(s) refreshed every week", pages 10 a week): every project sets its own. Give them at add, or after:

```
issue-cli department add legal --mission "No launch ships with a legal gap I haven't seen" --goal "open-findings=Open findings/0/month"
issue-cli department update legal --mission "…"                      # only the mission: line changes
issue-cli department update legal --goal "baseline-pass=Baseline checks passing/90/month"
issue-cli department update legal --goal "dpa=Signed DPAs/3/quarter"  # a new id is added after the last goal
issue-cli department update legal --drop-goal open-findings           # its lines go
```

- `--goal "<id>=<name>/<target>/<period>"` is repeatable. The name may hold `/` (target and period are the last two fields); target is a number; period is `week`, `month`, `quarter` or `none`. A goal whose id the template has keeps the template's `unit:`, `direction:`, `source:` and `rolls_up:` (Marketing's `visits` still reads the weekly SEO pass's metrics file); only name, target and period come from the flag.
- At add, `--mission` replaces the sample mission and any `--goal` makes the entry's goals exactly the ones given, in that order. They apply to the template's own department: a group's departments (Marketing under `gtm`) get their samples, and the add names an update command for each. `--dry-run` shows the entry with them, and its `Apply:` line repeats them.
- Without them, the entry gets the samples with `# sample: replace` at the end of the `mission:` line and of each goal's `- id:` line, and the add's last line is the command to set them: `Set this project's own mission and goals (now the template's samples, marked # sample: replace): issue-cli department update marketing --mission "…" --goal "<id>=<name>/<target>/<period>"`.
- `department update <name>` with `--mission`, `--goal` or `--drop-goal` does only that, on any department (not only one from a template), and not the template-text refresh it does without them. Each value is spliced in as text: only its own lines change (and the lines a new goal or a missing `target:` / `period:` adds, or a dropped goal takes), comments elsewhere and every other byte of projects.yaml stay; the result must load and read as asked before it replaces the file. Setting a value drops its line's `# sample:` comment. `--dry-run` says what it would set; workers are refused.
- **Keeping a sample.** Set it to the same value (`--goal "posts=Posts published/2/week"`): its line then ends `# sample: kept`, and lint stops naming it.

Lint's `departments` check warns about a department from a bundled template whose mission, or a goal (same id, name, target and period), still reads the template's sample, today's or an earlier release's (`internal/depttemplate/update.go`), unless its line says `# sample: kept`. The finding's fix is the update command for exactly the fields left. A department added before the markers is judged by its values alone, so an old entry's "A steady pipeline of new clients" is flagged as well. Editing the value by hand is just as good: a value that no longer reads a sample is the project's, whatever comment stays on its line.

**Group templates.** A template with `group: true` and `children: [marketing]` is a [group](#groups): **GTM** is the first. `department add gtm` writes `departments/gtm/role.md` (a group has no workflow file: its own issues run on Product's), adds GTM's entry, then installs each child from its own template with `parent: gtm` in its entry, never overwriting a file. A child you already have with no `parent:` is moved under the group (one `parent: gtm` line spliced under its `name:`), its files left as they are; one already under another group is left there and the plan says so. `department add <template> --parent <group>` installs a department under a group the project has; a group template takes no `--parent`. A group template's `optional:` names departments that can join it later and aren't installed with it: GTM's is `sales-b2c`. `department add gtm` ends with "Can join GTM later: Sales (issue-cli department add sales-b2c --parent gtm)", `department list` shows it (`can join later: sales-b2c`), and adding Sales with no `--parent` on a project that has GTM says "GTM is a group here and takes Sales: add --parent gtm to put it under GTM". `department list` and the tile list departments first, then groups.

`department remove <name>` takes the entry out of projects.yaml and names the files it left in place. It refuses while the department has issues that haven't finished its workflow, listing each with the `set-meta … --key department` command that moves it, and while departments sit under it (a group).

The Updates page (`/p/<project>/updates`) has a **Departments you can add** section: each template the project hasn't installed, what it brings its **Add…** button (the preview below) and its `department add` command. It isn't counted in the sidebar's Updates number, which counts workflow features.

**When a template's text changes.** The templates speak of your site(s), customers and the owner, whoever runs the project: one site of your own or many. A department installed from an earlier text (the marketing template's "client sites" before 2026-10-31) keeps what it has until you ask: `issue-cli department update <name>` replaces a file (`role.md`, a job's brief, the workflow file) or a `role:`, `mission:` or goal `name:` in projects.yaml **only when it still holds an earlier template's text byte for byte**, known by the hashes and values in `internal/depttemplate/update.go`. Anything you edited is kept and named; `--side` writes the template's text next to each edited file (`role.template.md`, `marketing.template.yaml`) to compare, never over a side file that is already there with other text; lint's `untracked` check names a copy left in place apart, with `rm` as its fix: delete it once compared, never commit it. `--dry-run` says what would change. projects.yaml values are spliced in as text, comments kept; if one fails, what was replaced before it stays replaced and running the update again finishes it. The workflow file follows the same rule, narrower: only an earlier text that differs from today's in wording alone is listed (none for marketing: its report type came through `workflow upgrade`), so an update never brings in a catalog feature (a gate, a status) behind `workflow upgrade`'s offer and Decline; `TestEarlierWorkflowsDifferInWordingOnly` holds that. A file one byte away from it (upgraded, edited, or older) is kept, and `--side` puts the new bundled file next to it. A wording change in a workflow template is not a catalog feature, so `workflow upgrade` doesn't offer it. `--commit` on add unstages the files again when git refuses the commit (a hook), so they never ride along with a later one. The Updates page has a **Departments with new template text** section: each installed department with something to replace, what it replaces, what it keeps, and the commands. Whoever changes a template's text adds the old file's sha256 and old values there, and the old file under `internal/depttemplate/testdata/earlier/<key>/<path>.<n>` (`TestEarlierHashesAreBacked` checks every hash has its file and none is today's text). Today's hash of every template's `role.md` and job briefs is pinned in `internal/depttemplate/testdata/current.sha256`: a changed file fails `TestBundledFilesChangeOnlyWithTheirEarlierHash` until its old hash and text are saved as above and its pin is set to the new hash, which the failure prints.

**From the company page.** The **+ New department** plot on the company floor (`/p/<project>/company`), and the last row of its phone list, link to `/p/<project>/departments/new`, which with no template asked is the picker; on the classic page (`?classic=1`) the tile opens the same form in place. The form has one choice per bundled template the project hasn't added (a group template says which departments it installs) and, once the project has a group, **Under which group?** (none, or each group; ignored for a group template). With every template added the tile says "Every department template is added" and has no form. **Preview →** opens `/p/<project>/departments/new?template=<key>`, the plan `department add --dry-run` prints: each file with its state (written, already there, or your edited file kept), the projects.yaml path and the exact entry. Nothing is written before **Add <name>**, which runs the same install (internal/depttemplate), reads the project back from projects.yaml so the room shows without restarting the viewer, and returns to the company page with "✓ Added <name>: …" and the new room outlined. Start its lead from the room. The viewer adds departments only from the templates. A template the project has is refused on the preview with the reason, and Add answers 409 with it, writing nothing. Add (`POST /p/<project>/departments/add`) takes only the owner's browser: a cross-origin request, or one that sends neither `Sec-Fetch-Site: same-origin` nor a same-host `Origin` (an agent's curl), gets 403; agents use `issue-cli department add` with the owner's words. A project without a projects.yaml shows the tile with no form. A department added with the CLI while the viewer runs still shows after a restart.

**Shared lines.** Lines several templates carry have one source, a file in `cmd/issue-cli/templates/departments/_shared/` (heavy tools, publish exactly, the Published box, the shelf hint, the licence's changes clause, the stamp header). Installed files are plain text, so a template quotes the line word for word; `TestSharedTextsQuotedWordForWord` fails on a template that names one and rewords it.

**Adding a template**: a folder `cmd/issue-cli/templates/departments/<name>/` with `department.yaml` (`name`, `description`, `color`, `role`, `mission`, `systems`, `workflow`: a bundled workflow template, `goals`, `jobs`: each with its `brief:` a file in the folder, `jobs/<id>.md`) and `role.md`. The file is read strictly: an unknown key, a job whose schedule doesn't parse or a missing brief is an error. Marketing ships `weekly-seo-pass` (Mondays 07:00, `type: seo-pass`). Optional, for a department that drives an outside program: `partner:` (`name`, `adapter`: a registered one, a default `path`, the pinned `commit`, and `repo`, the clone URL the steps name; only name, adapter, path and commit go into the entry), `connectors:` (as under a department, secrets as references only; `{{path}}` in a command, argument or check is the partner's checkout) and `install:` (a file in the folder: the steps `department add` prints, with `{{path}}`, `{{commit}}` and `{{repo}}` filled in). `connections:` (`requests:` and `feeds:`, by box key, each route touching the template's own department) are the handoffs `department add` prints to declare. A group template's `optional:` lists department templates that can join it later. `connector_examples:` (a file in the folder holding a `connectors:` list) is a connector the department can use but doesn't get: `department add` writes it into the entry **commented out**, inside the entry before its `goals:` (so adding or removing a department keeps it with its entry), and the owner uncomments it to turn it on. Marketing's holds the `openseo` [rankings source](#channels-as-types) and the `umami` visits source; Sales' the same `umami` and the `sendportal` email tool. A template without them adds its connectors by hand under the installed department ([Connectors](#connectors)). `files:` lists starter files in the folder the owner edits, each written to `departments/<key>/<file>` like `role.md` and never over an existing one (Marketing's `subreddits.md`, [Social: Reddit](#social-reddit)); an entry is one plain file name, not `role.md`. `partner_slots:` names the [partners catalog](#the-partners-catalog)'s slots the department fills (a slot the catalog doesn't have is an error); `department add` names their rows and installs none.

### Bringing a template change into a live department

An installed department's workflow file is the project's own, and git may not track it (the hall's Edit then writes it in place, with no commit). Two commands cover part of a later template change: `workflow upgrade --department <key>` proposes the catalog features the file lacks, each with a snippet, and `department update <key>` replaces the file only while it still holds an earlier template's text. Anything else goes in by hand: a type or status the template gained since, or a file with your own edits. Run it from the project's main checkout, the folder the board reads, not from a worktree (Marketing as the example):

```
issue-cli workflow upgrade --department marketing    # catalog features the file lacks (reads only)
issue-cli department update marketing --dry-run      # template text it would replace (reads only)
cp -p workflows/marketing.yaml workflows/marketing.yaml.bak-$(date +%F)
issue-cli workflow init --template marketing --out workflows/marketing.template.yaml
diff workflows/marketing.yaml workflows/marketing.template.yaml
cp -p workflows/marketing.yaml workflows/marketing.next.yaml
# copy only the new pieces into workflows/marketing.next.yaml, as text
issue-cli process workflow --file workflows/marketing.next.yaml    # the candidate, read only: statuses, paths, file lint
issue-cli process transitions --file workflows/marketing.next.yaml --type social
mv workflows/marketing.next.yaml workflows/marketing.yaml
issue-cli process workflow --department marketing    # the statuses and every type's path
issue-cli process transitions --department marketing --type social   # a changed type's gates
issue-cli health lint --check department,departments,schema,owner-only
issue-cli workflow scan --department marketing       # record the features the file has now
rm workflows/marketing.template.yaml
```

- **Splice, don't overwrite.** Copy the new type, status or transition from the bundled file into yours and keep everything else as it is: a different line in your file is your edit. Leave out what the issue-cli the board runs (`issue-cli --version`) doesn't know yet: lint's `schema` check names an unknown key. Record on the issue what you brought in and what you left out (`issue-cli decide <slug> --provisional --text …`).
- **Check the file the board reads.** `process workflow --department` prints `Workflow file:` first; it must be the file you edited. With `--project` from a worktree, it is the main checkout's.
- **Dry-run the candidate first.** `process workflow --file <path>` and `process transitions --file <path>` read any workflow file without writing anything: its lifecycle and every type's path, its gates (with `--type` / `--system`), a load error naming the file (exit 1), and the file checks `health lint` runs on a workflow file (an unknown key with its nearest name, the template stamp, placeholders). Splice into a copy (`marketing.next.yaml`), check it with `--file`, and only then move it over the live file. `process transitions --workflow` names a system overlay, not a file: a path there is refused and points to `--file`. The board's checks (issues on the file's statuses, project-wide keys) need the live file, so run `health lint` after the move; if a check is red and you can't fix it, put the backup back: `mv workflows/marketing.yaml.bak-<date> workflows/marketing.yaml`.
- **projects.yaml** (a system the new type uses under `systems:`, a job's `type:`): the same dated backup, then a splice of the lines you add, as text, and `health lint --check departments`. The viewer reads workflow files again when they change; it shows a projects.yaml change after its next restart.
- **piece and seo-pass publish through `cms` (marketing@v0.64.0).** Splice `types.piece.statuses` (its `brief`, `draft` and `publish` prompts; the type had none before) and, under `types.seo-pass.statuses`, the new `publish` prompt and the two `cms` lines of `draft`, then `issue-cli process transitions --file workflows/marketing.next.yaml --type piece` (owner review → publish still `owner_only`) and `workflow scan`, which records `cms-publish`. `workflow upgrade --department marketing` proposes that entry until it is in. Nothing calls Ghost until the `cms` connector is uncommented in projects.yaml ([Publishing to the site](#publishing-to-the-site)); until then publish asks the owner for the link.
- **Page rules (marketing@v0.65.0).** Add the `Page rules:` box to the `Draft` section of the base `brief → draft` and to the seo-baseline's `Technical basics` (the bundled text), then `workflow scan`, which records `page-rules`; `workflow upgrade --department marketing` proposes it until then. `department update marketing` brings `role.md`'s `## Page rules` and the weekly SEO pass's page-check step where you haven't edited them ([Page rules](#page-rules)).

### Video studio

`issue-cli department add video [--partner-path <dir>]` installs **Video**: short explainer and demo videos made by an outside studio program, and [reels](#reels), its [partner](#partners), OpenMontage first. Nothing of OpenMontage is in this repository (it is AGPL-3.0; video epic D-3): the add prints the steps to clone `calesthio/openmontage` at the pinned commit to the path you chose, run `make setup`, keep model weights in it (`HF_HOME=<path>/.hf-cache`), put `<path>/.venv/bin` first on PATH, turn HyperFrames telemetry off (`~/.hyperframes/config.json`, `"telemetryEnabled": false`; video epic D-6), and leave its `.env` with empty keys; then the licence notes (OpenMontage AGPL-3.0 as a separate program, Remotion's company licence, piper GPL-3.0, each model's own licence). The entry records the path and the commit, nothing else of the install. `TestTrackedFilesHoldNoPartnerFile` (internal/partner) fails on any tracked file that looks like OpenMontage's (its catalog row's `marks:`), or, with `PARTNER_CHECKOUTS=<checkout>` (or `OPENMONTAGE_CHECKOUT`), is byte for byte one of its files. Lint warns until the entry names your recorded exception ([The licence rule](#the-licence-rule)).

- **Workflow `video`**, type `studio` (the default): brief → proposal → script → scene plan → assets → compose → review → publish → done, one status per partner stage. Type `reel` has its own short path ([Reels](#reels)). Leaving proposal, script, scene plan, assets, review and publish is an `owner_only` gate: YOLO passes none of them ([Nothing goes out without the owner's stamp](#settled-rules)). Each has a way back with a `revise:` comment. compose → review needs the render on the shelf (`has_output: video`).
- **Role** (`departments/video/role.md`): the worker drives the partner from its checkout as the partner's own guide says, records an approval at the partner only after the owner's stamp is on the issue (quoting it), and never copies the partner's files, skills or prompts here. **One model per machine:** the [heavy-tools line](#running-partners-on-this-machine), and a model is freed before the next; local models first, a paid tool only when the owner says so (video epic D-1).
- **Connector `openmontage`** (kind tool, `<path>/.venv/bin/python`): `FAL_KEY` and `ELEVENLABS_API_KEY` as `keychain:dark-factory-video/<VAR>` references, both `optional:` so the zero-key path runs, `sends: true` (video epic D-2). Its keys are not metered: the reel connectors below are.
- **Connectors `kling` and `elevenlabs`** (reels, paid, [metered](#spend-caps)): `kling` is the official Kling CLI (`npm i -g @klingai/cli-global`, found on PATH), billed from the Kling membership credits, with no secret (`kling login` is the owner's, in their own shell, and the connector refuses `login`/`logout`); `elevenlabs` is text-to-speech called by issue-cli itself, its key `xi-api-key: keychain:elevenlabs-api`. Both ship with no `cap:`, so every billing call is refused until the owner writes one, and with no `voices:` (install.md shows how to add them).

#### Reels

The video workflow has two [types of work](Workflow/types.md): **`studio`** (the default; the flow above, with its 6 owner_only gates, for long explainer and demo videos) and **`reel`**, a 15–60 s vertical video for Instagram Reels, TikTok or Shorts made from a storyboard JSON filled into the project's own template (the reel kit, `issue-cli reel init|validate|render|qa|shelve`; reels epic D-1: no OpenMontage for reels). A reel asks the owner twice: one review of the finished reel on the shelf, and the stamp that publishes it (reels epic D-2).

```
issue-cli create --department video --type reel --title "<the reel's one message>"
```

| Step | What happens | Who passes it |
|:--|:--|:--|
| brief | topic and one message, audience, length, placement, template shape, voice, call to action under `## Reel brief` | the worker (`has_section: Reel brief`); brief → script appends `## Legal check` |
| script | every spoken line and caption under `## Script`; `## Legal check` filled in | the worker; script → render runs `reel_legal_check` |
| render | storyboard, render, QA, on the shelf with its QA report (`## Render`) | the worker; render → review needs `has_output: video` and runs `reel_legal_check` again, now with the storyboard's narration and captions |
| review | the owner approves or rejects the reel on the shelf, then stamps `publish` | review → publish needs both `outputs_approved: video` (the review) and an owner_only stamp (YOLO never passes it): nothing goes out before them |
| publish | posted with the caption, hashtags, the AI label and the disclaimer; the link under `## Published` | publish → done needs only `## Published` ticked: no third stamp |

The path is brief → script → render → review → publish → done (`render` is a base status only the reel walks). A `revise:` comment goes back: script → brief, render → script, review → render or script. Voices and paid clips go through the department's spend-capped connectors (`elevenlabs`, `kling`), never a key in a file.

**The legal check** (`reel_legal_check`, [docs/workflow.md](workflow.md#reel-legal-check); `issue-cli reel-legal [<slug>]` prints the same findings without moving, exit 1 on a hold) is plain Go over the issue's text: no shell, no network, no owner. It reads `## Script`, the `## Legal check` declarations (`- key: value`: `music`, `music_licence`, `ai_content`, `ai_label`, `face`, `persona`, `consent`, `disclaimer`, `claim: <phrase> = <authorisation>`, `storyboard`; a value still in `<…>` is unset) and the phrase lists in `departments/video/reel-legal.yaml`. It holds the reel on:

- a nutrition or health claim (EU Regulation 1924/2006: "schudnij", "zdrowy", "lose weight", "high protein", …) with no `claim:` line naming its authorisation;
- no `music:` line, or a track with no licence (`music_licence:` missing, `unknown`, `tbd`);
- AI content (an AI person, a cloned voice, AI footage: `ai_content: yes`, or `face: avatar`) without `ai_label: yes` (EU AI Act art. 50, Meta and TikTok rules);
- a first-person testimonial ("schudłam", "I lost", "as a customer"), or a face whose `persona:` is missing or a customer (Omnibus directive 2019/2161: fake reviews);
- a face (`avatar` or `real`) without `consent:` naming a non-empty file in the project: the person's written consent for commercial use;
- a trademark (Thermomix, Vorwerk, TM6, …) in the same sentence as an endorsement phrase ("oficjalny", "recommended by", "partner"), or named with no `disclaimer:`.

A missing or empty rules file holds every reel, so the check never passes by accident. It is a floor, not legal advice: the owner still reviews the finished reel, and the Legal department answers what a list can't.

**Your own copy.** `department add video` writes the workflow to `workflows/video.yaml` and the rules to `departments/video/reel-legal.yaml`; both are the project's from then on. Change a gate, a prompt or the path in `workflows/video.yaml` (a type's `transitions:` with `replace: true` swaps an edge's gates; [Types of Work](Workflow/types.md)); add your language's words, your market's claims and the marks near your product in `reel-legal.yaml`, or point a gate's `path:` at another file. A Video installed before the reel type gets it from `issue-cli workflow upgrade --department video` (catalog `reel-type`) and the rules file from `issue-cli department update video`.


**Reels** (short 9:16 videos) don't use the studio partner: they come from the [reel kit](reels.md) (`issue-cli reel`), a storyboard JSON filled into the project's copy of the starter Remotion template, encoded with FFmpeg, checked by QA and shelved for one owner review.

### Sales (B2C)

`issue-cli department add sales-b2c --parent gtm` installs **Sales** under GTM, beside Marketing (D-5, D-6: the GTM lead is the lead of both leads); without a GTM group it installs at the top level. B2C only: an offer, the product's own pages and emails, a weekly look at the funnel. No CRM, no pipeline, no cold outreach; those are B2B work.

- **Workflow `sales-b2c`**: research, brief, draft, owner review, publish, measure, done, with types of work: `offer` (default: what a plan or test invite contains, brief → draft → owner review → publish → done), `landing` (the landing page and waitlist form, its consent wording; then measure), `launch` (an invite wave or announcement to the waitlist; then measure), and the two weekly reports `funnel-review` and `support-digest` (research → owner review → done, closed by the owner's approval on the shelf, `outputs_approved: report`).
- **Gates**: brief → draft needs Marketing's brand foundation (`departments/marketing/brand.md` and `icp.md`: Sales writes in Marketing's voice; the hint names the foundation issue). draft → owner review needs the draft on the shelf (`has_output`). **owner review → publish is `owner_only`**: everything a customer sees goes out only after the owner's stamp, and YOLO never passes it ([Nothing goes out without the owner's stamp](#settled-rules)). A text that lives in the product's code goes to Product as a request via HQ (`create --requested-by sales`); Sales never edits the product.
- **Goals from real numbers**: `visits`, `signups`, `testers` (week) and `retention` (pct, month), each `source: file:<issues>/.goals/sales/weekly-funnel.json#<id>` ([a job's metrics file](#goals-a-job-fills-from-a-connector)). Only the weekly funnel review writes that file (`issue-cli goal write sales/weekly-funnel …`), with the numbers it read from a connector or the owner: never an estimate, and a number not read is left out. Until its first run, each goal reads "no value yet: <issues>/.goals/sales/weekly-funnel.json is not written yet"; a run missed shows "no value since <date>".
- **Jobs**: `weekly-funnel` (Mondays 08:30, type `funnel-review`: the metrics file and the report on the shelf) and `support-digest` (Fridays 16:00: themes from the week's support conversations, each feature request filed to Product via HQ). Both off until `issue-cli job auto on sales/<job>`.
- **Handoffs**, printed to declare: Marketing → Sales `feeds` (the launch audience and campaign results); Sales → Product `requests` via HQ (feature requests and checkout changes; the engine-request rule, marketing foundation D-8).
- **Visits from Umami**: `department add` writes an `umami` connector into the entry commented out (`connector_examples:`), the same one as Marketing's ([Channels as types](#channels-as-types)): a read-only `kind: rest` connector on Umami's REST API, `url: http://localhost:3000/api`, `Authorization: "Bearer keychain:umami-api-key"` and only `get: [/websites/*/stats]`, `sends: false`. Uncomment it to turn it on; the weekly funnel review reads visits from it: `issue-cli connector exec sales/umami -- '/websites/<id>/stats?startAt=<ms>&endAt=<ms>'`.
- **Email from SendPortal**: below `umami`, a commented `sendportal` connector for a launch to the waitlist: SendPortal (MIT), self-hosted wherever the owner runs it, a `kind: tool` running `curl` against its REST API with `SENDPORTAL_API_TOKEN: keychain:sendportal-api-token`, its `check:` on `/api/v1/ping`, no `per_run:` (it stays up to take sign-ups). It sends, so it has `sends: true` and `needs_approved_output: true`: `connector exec` refuses it until the owner approves the issue's newest output on the shelf, on top of the `owner_only` gate before `publish`. The `launch` type's publish prompt names it. Its comment carries the consent rule: double opt-in is unverified in SendPortal, so a list collected under a new consent version is checked before a campaign goes to it. The mail provider SendPortal sends through (SES, Postmark, SendGrid, Mailgun, Mailjet or SMTP) is the owner's choice and sign-up.
- **Payments from the payments kit**: below `sendportal`, two commented connectors for when the product charges, both `kind: tool` running `payments-kit` against the kit the product runs beside its app ([From a department](payments.md#from-a-department)): `payments` with the kit's read token (`PAYMENTS_TOKEN: keychain:payments-read-token`, `sends: false`; a payment's state and the provider only, the kit answers anything else 403) and `payments-refund` with the api token (`keychain:payments-api-token`, `sends: true`, `needs_approved_output: true`), which only a refund's publish step uses after the owner's yes, with the issue's slug as the idempotency key. Both `check:` with `payments-kit check`, which prints the mode first. The sandbox/live switch is `mode:` in the product's `payments.yaml`, the owner's: live wants a real provider, Keychain-only keys separate from the sandbox ones, and Legal's word on the checkout texts; the stand-in refuses it. `TestSalesB2CPaymentsConnectorsPreset` uncomments them and lints them clean.
- **Other connectors**: `issue-cli connectors sales` prints the config to add: a support inbox for the digest.

#### When you start charging

Money is not on the default path. The end of `workflows/sales.yaml` holds a commented block between `BEGIN when-you-start-charging` and `END when-you-start-charging`: the types `checkout` (the flow and its texts, built by Product), `price-change` (a price or discount, with the lowest price of the last 30 days beside any discount) and `refund` (one customer's refund or withdrawal: brief → owner review → publish → done). Each reaches publish only through the same `owner_only` gate, so every price, checkout text and refund is the owner's call even in YOLO; checkout and price changes also wait for Legal's word (a `legal:` comment) until a Legal department's handoff replaces it. To turn it on: remove `# ` from each line of the block, add `Checkout` to the department's `systems:`, and add the money goals the block lists (`paid`, `mrr`, `refunds` and `churn`, down) to the department in projects.yaml, read from the same metrics file. Uncomment the payments kit's two connectors with it. A refund uses a separate restricted payment key, a `keychain:` reference: the refund's brief reads the payment through `sales/payments`, and only its publish step, after the owner's yes, refunds through `sales/payments-refund` (`-- refund <payment-id> --amount <approved> --idempotency-key <slug>`; `TestSalesB2CChargingNamesPaymentsConnectors`). `TestSalesB2CWorkflowMoneyGatesOwnerOnly` uncomments the block and checks every type's way into publish is owner_only; `TestSalesB2CDepartmentLintClean` lints it both ways.

The code a `checkout` or `refund` builds on is the [payments kit](payments.md): one provider-neutral interface (hosted checkout, signed webhooks turned into neutral events, refunds, the invoice hook) that the product runs beside its app, with a stand-in provider until the owner picks a real one.

### Legal

`issue-cli department add legal` installs **Legal**, a top-level department (no `parent:`: it serves Product and Sales alike, so it sits under no group; factory/factory-departments-floor-map-and-capabilities#D-5). Its agents check the product (code, data flows, the texts a customer sees, the third parties it uses) against GDPR and the consumer law where it sells, draft the documents the owner needs, and answer other departments' legal checks. **They flag, cite and draft; they never give a legal sign-off**: anything binding goes to the owner, and real exposure to a lawyer. Every finding cites the law by article from its official text (EUR-Lex for EU law, ISAP or the Dz.U. ELI link for Polish law, the regulator's pages) with the date it was read, since the law changes. It has no connectors: it reads the repo and the web, `mem-guard check` before a bulk fetch (Settled rule "Research watches memory").

- **Workflow `legal`**: brief → research → draft → owner review → publish → done, with four [types](Workflow/types.md):
  - `compliance-check` (the default): brief → research → draft → owner review → done. A report on the shelf, one line per check: check · source (article, read on) · how to verify · status.
  - `document` (privacy policy, terms or regulamin, cookie notice, DPIA, art. 30 register, breach procedure, a processing agreement): brief → draft → owner review → publish → done. The draft goes on the shelf as `--kind document`; **owner review → publish is `owner_only`**, so YOLO never passes it: a customer sees it or it binds the owner.
  - `legal-check`: brief → research → owner review → done. The answer to a [handoff](#handoffs) (`issue-cli handoff legal --for <slug>`), a report on the shelf; it is what [the handoff gate](#the-handoff-gate) waits for.
  - `due-diligence`: brief → research → draft → owner review → publish → done. A pack for a product or a component as neutral engineering documentation (dependencies and licences, provenance from git, third-party ties, data posture, test evidence, architecture), never with deal, buyer or price context; sharing it outside is the owner's publish gate (legal epic D-1). Filed on demand: `issue-cli create --department legal --type due-diligence`.
  - Every path ends with the owner's approval of the newest version on the shelf (`outputs_approved` on owner review → done or → publish), which YOLO never stamps. A `revise:` comment goes back to research or draft.
- **Jobs**: `monthly-baseline` (the 1st, 07:00, a `compliance-check`: re-run the product's baseline table and diff it against the last run) and `law-watch` (the 15th: re-read every cited source and note what changed, e.g. a directive's transposition). Jobs run by day, week or month, so the outline's quarterly law watch runs monthly. Their standing start is off.
- **Goals**: `baseline-pass` (pct of baseline checks passing) and `open-findings` (down), set by the baseline run with `issue-cli goal set`.

### Channels as types

A marketing **channel** (SEO, PPC, social, cold outreach) is a [type of work](Workflow/types.md) in Marketing's workflow file, not a department of its own. Each type has its own path through the statuses and its own prompts, and pairs with a **system of the same name** in the department (`seo-pass` ↔ `SEO`): its folder under `issues/`, its room in the department and the board's `?system=` filter. The pairing is a convention the template and its comments keep; the engine doesn't bind a type to a system, so name both when you create one by hand.

The bundled `marketing` workflow (`cmd/issue-cli/templates/workflow/marketing.yaml`) has four channel types (`piece`, `seo-pass`, `seo-baseline`, `social`), three [brand foundation](#brand-foundation-and-research-mode) types that come before them, and `report`, research that ends on the shelf. Every type but `report` goes out in `publish`, behind `owner review → publish`, which is `owner_only`: YOLO never passes it, so nothing is posted, sent or paid for without the owner ([YOLO Mode](Workflow/yolo.md#gates-yolo-never-passes)). The types:

| Type | Path | For |
|:--|:--|:--|
| `piece` (default) | brief → draft → owner review → publish → done | one post, page or email |
| `seo-pass` | brief → draft → owner review → publish → **measure** → done | an SEO pass over your site(s) (system `SEO`) |
| `social` | **research** → brief → draft → owner review → publish → **measure** → done | social posts, after research on similar profiles, competitors and trends (system `Social`; [Social: research before the brief](#social-research-before-the-brief)) |
| `seo-baseline` | brief → draft → owner review → publish → **measure** → done | once per site, the SEO groundwork: technical basics, a waitlist, pre-rendered pages and measurement, with the owner's account, DNS and CDN steps (system `SEO`; [SEO baseline walk-through](seo-baseline.md)) |
| `foundation` | research → brand → icp → owner review → publish → done | the brand and the ICP, settled with the owner |
| `market-size` | research → market size → owner review → publish → done | TAM / SAM / SOM, later or again |
| `keyword-research` | research → shortlist → owner review → publish → done | the ICP's search words clustered by intent, what to own, and a positioning line ([Keyword research for positioning](#keyword-research-for-positioning)) |
| `competitor-scan` | research → **competitors** → owner review → publish → done | who competes, what ranks for the ICP's queries and the topic gap, from web search only, settled into `competitors.md` (system `SEO`; [Competitor scan](#competitor-scan)) |
| `report` | research → owner review → done | research or report only: one report on the shelf, nothing published (the [growth scout](#the-growth-scout)'s runs) |

No content channel's brief (`piece`, `seo-pass`, `social`) moves to draft until the department's `brand.md` and `icp.md` exist.

`seo-baseline` is technical, not content, so its brief doesn't wait for them. Its brief writes the owner's answers to five questions under `## Owner answers` (where the traffic goes, market, content cadence and who approves, where to start, whether AI training bots are allowed), and brief → draft appends `## Technical basics`, `## Waitlist`, `## Pre-rendered pages` and `## Measurement`, each box naming what the site must have and the test or command that checks it, plus `## Owner steps`: `(owner)` boxes for the Search Console Domain property and service account, Bing Webmaster Tools, the CDN's AI-bot blocking and the deploy, which no agent can tick. draft → owner review needs the four sections ticked (or N/A, such as no waitlist when the traffic goes to an open sign-up) and the change set on the shelf (`has_output: site`); publish → measure needs the owner's steps. `TestSeoBaselineTemplate`, `TestSeoBaselineOwnerSteps` and `TestSeoBaselineWalk` keep it so. The owner's side, step by step: [SEO baseline: a walk-through](seo-baseline.md); catalog entry `seo-baseline`.

- `measure` records what went out and what it did: the goal values (`issue-cli goal set marketing/<id> <value>`) and a `## Measure` note with where the numbers came from. A number you can't read yet (rankings before Search Console is connected) is "no value yet", not zero.
- **The rankings source** is an `openseo` connector: OpenSEO (MIT), self-hosted on this machine and read over MCP at `http://localhost:3011/mcp`. `department add marketing` writes it into Marketing's entry commented out; uncomment it to turn it on. Its `deny_tools:` turns off the tools that send outside the machine or start paid schedules (`submit_indexnow_urls`, `set_report_sharing`, `set_ai_visibility_schedule`). It costs nothing until the owner adds a DataForSEO key: audits, Search Console and GA4 work without one, while keyword, rank and SERP data need it. That key is never a connector secret: it stays in Keychain (`security add-generic-password -s dataforseo -w`) and goes into OpenSEO's own `.env` as `DATAFORSEO_API_KEY`. The seo-pass measure prompt and the weekly brief read ranks, Search Console and GA4 from it, within a cost cap of $1 of paid data per pass (`maxCostUsd` on each paid call). Without the connector, a number nobody can read is still "no value yet".
- **Visits** come from an `umami` connector, written into the entry commented out below `openseo`: Umami (MIT), wherever the owner runs it (self-hosted, port 3000 by default, or Umami Cloud), a read-only `kind: rest` connector on its REST API: `url: http://localhost:3000/api` (Cloud: `https://api.umami.is/v1` with an `x-umami-api-key` header), `Authorization: "Bearer keychain:umami-api-key"` (a self-hosted token from `/api/auth/login`, or a Cloud API key), only `get: [/websites/*/stats]`, the path the visits goal reads, and `sends: false`: issue-cli makes the GETs itself, so no write call is reachable. A call: `issue-cli connector exec marketing/umami -- '/websites/<id>/stats?startAt=<ms>&endAt=<ms>'`. It has no `per_run:`: a collector stays up to receive visits. Marketing's `visits` goal (500 a week to start) reads `source: file:<issues>/.goals/marketing/weekly-seo-pass.json#visits` ([a job's metrics file](#goals-a-job-fills-from-a-connector)); the weekly brief's measure step writes it with `issue-cli goal write marketing/weekly-seo-pass visits=<n>`, and `goal set marketing/visits` refuses and names that command. `pages` and `posts` stay manual (`goal set`).
- **Social posts** go out through a `social` connector, written into the entry commented out below `umami`: Postiz (AGPL-3.0, the social slot's default by the open-source partners epic's D-5, used unchanged over HTTP under the owner's licence exception D-4), self-hosted on this machine and read over MCP at `http://127.0.0.1:4007/api/mcp`, its one key as `Authorization: "Bearer keychain:postiz"`. It is named by its slot, not the tool, because the `social` type's prompts name the connector `social`. Its key drafts and posts (no read-only key), so `sends: true`; `deny_tools:` turns off Postiz's own AI tools; `at:` allows the read calls at brief, nothing at draft or owner review, scheduling only at publish and reads at measure. Postiz's `type: now` is inside each post (`socialPost[].type`): publish's `deny_args: {integrationSchedulePostTool: {"socialPost[].type": [now]}}` names that path, so the bridge refuses a post sent to go out at once and only scheduled ones reach Postiz, besides the `owner_only` gate before publish; its MCP has no analytics tool, so measure reads each post's numbers through the `social-analytics` connector (below). It needs about 2.4 GB while up, so it carries `per_run:` (`tool: postiz`): started for one run, stopped after on every way out ([Per run](#per-run) shows it). `TestMarketingSocialExampleIsPostizPerRun` keeps it lint-clean when uncommented, and `TestPostizPerRunStopsOnEveryWayOut` stops it on every way out.
- **Social numbers**: the social type's measure reads each post's numbers through the `social-analytics` connector, written commented out below `social`. It is a `kind: rest` connector on Postiz's public REST API at `http://127.0.0.1:4007/api`, with `get:` limited to `/public/v1/analytics/post/*` (per post), `/public/v1/analytics/*` (per channel), `/public/v1/posts` and `/public/v1/integrations` (to find the ids). Its key is the same Keychain item, `Authorization: "keychain:postiz"` (Postiz's public API takes the key without `Bearer`). Only GETs are reachable, so it is `sends: false` even though the key can post. It has `social`'s own `per_run:` with `tool: postiz`, so the two share one lease: Postiz starts for the call and stops after, unless a social run still holds it. One call reads every post at one start: `issue-cli connector exec marketing/social-analytics -- '/public/v1/analytics/post/<post id>?date=7' …`, with the post ids publish listed under `## Published`. A network Postiz has no analytics for (Bluesky, Mastodon, Reddit) answers `[]`, which measure writes as "no value yet". `TestMarketingSocialAnalyticsExampleIsRestPerRun` keeps it lint-clean and on `social`'s lease. `TestRestConnectorReadsOnlyGET`, `TestRestConnectorRefusesPathsBeforeStart` and `TestRestConnectorSharesThePostizLease` run it against a stand-in Postiz REST server on loopback.
- **The site**: `piece` and `seo-pass` put an approved post or page out through a `cms` connector, written commented out below `reddit`: Ghost (MIT, the cms slot's default, from the CMS spike's D-1), the CMS and the site in one container, at `http://127.0.0.1:2368` by default. It is named by its slot, not the tool, so a swap is that entry and its helper. It is a `kind: tool` connector, `command: sh` on `departments/marketing/cms-ghost.sh`, which `department add marketing` installs beside `role.md` (our own POSIX sh over Ghost's Admin API with curl, openssl and jq; no Ghost code). Its one secret is `GHOST_ADMIN_KEY: keychain:ghost-admin-key`, the Admin API key from a Ghost custom integration (`<id>:<hex>`). The helper signs a 5-minute HS256 token with it (kid `<id>`, aud `/admin/`), computing the HMAC over stdin so the key is on no command line. Its `check:` reads `/ghost/api/admin/site/`, which needs no key. The key publishes, so `sends: true` and `needs_approved_output: true` hold every call until the owner approved the issue's newest output on the shelf, besides the `owner_only` gate before publish. It has no `per_run:`: about 180 MB at idle, and once live it is the site. Three calls, each printing one JSON line `{"id","status","url"}`, the `url` for `issue-cli output publish <id> --url`:
  - `issue-cli connector exec marketing/cms -- sh departments/marketing/cms-ghost.sh publish --file <approved.md> --title "<t>" --kind post|page [--slug s] [--excerpt e] [--meta-description d] [--at <ISO time>]` publishes at once (status `published`, or `scheduled` with `--at`), never as a draft first.
  - `… cms-ghost.sh update --id <id> --file <approved.md> [--title t]` reads the post's `updated_at` first (Ghost refuses a write without it), then writes the new text.
  - `… cms-ghost.sh get --slug <slug>` reads one back.

  The file goes out byte for byte as one Lexical markdown card: the helper reads the card back out of its request and sends nothing when it differs (a file that isn't UTF-8). It never passes `?newsletter=` (or any query), so publishing mails no one. `--id` and `--slug` take letters, digits, `-` and `_` only. `GHOST_URL` names another Ghost. `issue-cli cms up` stands that Ghost up on this Mac in one command: the compose, the setup, both Keychain items and link tagging off (else Ghost adds `?ref=` to every link); it writes no config, so the owner uncomments the entry after ([Stand up the CMS](stand-up-the-cms.md), with the go-live steps). `department add marketing` prints it under Partner options, from the Ghost row's `setup:` in the partners catalog. `TestMarketingCmsExampleIsAGhostTool` keeps the entry lint-clean when uncommented. The `TestCmsGhost*` tests run the helper against a stand-in Ghost on loopback: the token's header, claims and signature, publish, update and get, no query string, the file unchanged, and the key on no command line.
- The base workflow has no `publish → done` edge, so a type that measures can't skip it; `piece` adds its own `publish → done`.
- `seo-pass` overrides the brief, draft and measure prompts: the sites and pages in the pass, local pages, FAQs and schema, each page on the shelf with `--kind page`, then the week's numbers.
- The weekly SEO job sets `type: seo-pass` ([Recurring jobs](#recurring-jobs)), so each week's run is an seo-pass. A site too big for one pass gets its own: `issue-cli create --department marketing --type seo-pass --system SEO --title "<site>: SEO pass"`.

```
issue-cli process workflow --department marketing   # the types and their paths
issue-cli create --department marketing --type seo-pass --system SEO --title "..."
issue-cli list --department marketing --type seo-pass
```

#### Social: research before the brief

The owner's rule (factory/open-source-partners-integrate-outside-tools-openmontage-openseo-postiz-instead-of-building-them-in-house#D-6): before a social brief, research what works on similar profiles and competitors, and which trends are rising. So `social` starts at `research`:

```
issue-cli create --department marketing --type social --system Social --title "Launch week posts"
```

| Step | What happens | Gate out |
|:--|:--|:--|
| research | Similar profiles and competitors, from `departments/<key>/competitors.md` and `icp.md` where they exist, plus ones found: per network, which posts work, their formats, cadence and times, hooks, topics and calls to action, each finding with the posts' links. Trends: rising topics, hashtags, sounds, formats and searches per network, each with its link and date. Findings under `## Research`; the report on the shelf (`issue-cli output add --issue <slug> --kind report --title "<topic>: social research" --file <research.md>`) | 5 distinct links under `## Research` (`section_min_links`) and the report on the shelf (`has_output: report`); adds the `## Brief` boxes |
| brief | Reads brand.md, icp.md and the research output; cites the output by its id (o-…) and the finding behind each choice (network, format, hook, time, hashtags); each network's rules through the `social` connector's read calls | brand.md and icp.md exist, and the `## Brief` boxes are ticked, the citation first |
| draft | One variant per network, no connector call, one `--kind post` output | `## Draft` ticked |
| owner review | The owner reviews the posts and the research they rest on | `owner_only` approval, and every output on the shelf approved (`outputs_approved`): the research report too |
| publish, measure | As below: schedule only what was approved; measure reads each post's numbers through the `social-analytics` connector (GET only) | |

**Read-only research.** The research reads through the department's `trends` and `competitors` connectors' read calls when they are on (`issue-cli connectors`), and otherwise public pages only, said so in `## Research`. It never posts, schedules, follows, likes, comments or messages, never logs in to an account to read it, and makes no `social` connector call. Login-based scraping of X, Instagram, TikTok or LinkedIn is out (ToS and account bans). The prompt names connectors, never the tool behind them: the open-source picks for each (factory/open-source-tools-for-social-research-what-works-on-similar-profiles-and-competitors-and-trend-detection, on the shelf) are a `connectors:` entry under that name, added by the owner. The heavy-tools line applies ([Running partners on this machine](#running-partners-on-this-machine)).

`TestSocialTypeNamesNoTool` keeps the prompts' words and refuses a tool name in any of them, `TestSocialStartsAtResearchAndCitesIt` walks create → research → brief → draft through its gates, `TestSocialTypeOnItsPath` and `TestSocialPublishOwnerOnlyLint` keep `owner review → publish` the only `owner_only` gate and health lint's `owner-only` check clean (and naming `type social` if it isn't). Catalog entry `social-channel`: `issue-cli workflow upgrade --department marketing` proposes it to an installed Marketing; add `Social` to the department's `systems:` in projects.yaml with it.

#### Social: Reddit

The owner (2026-11-14): Reddit as a source for social research and a place to post and comment, through the owner's review. Reddit is a network inside `social`, not a channel type of its own (factory/factory-departments-floor-map-and-capabilities#D-7).

**What Reddit leaves open.** Reddit stopped accepting new public Data API access requests on 2026-10-31; RSS, the last route without credentials, ended 2026-11-13; apps not registered with Reddit lose access from 2027-01-12, and the rest of public access ends in March 2027. Every route here, reading and posting, needs a Reddit app the owner already holds or one Reddit approves (an application, an account, and for commercial use possibly Reddit's contract terms). That step is the owner's; nothing in the engine scrapes Reddit or logs in as a user to get around it. Until there is an app, research says "Reddit: not read" and publish finds no Reddit channel in Postiz. The options and the pick are in this issue's research report on the shelf (factory/reddit-as-a-social-source-and-channel-read-chosen-subreddits-for-research-post-and-comment-on-them-through-owner-review).

| Piece | What it does |
|:--|:--|
| `departments/<key>/subreddits.md` | The owner's list: one table row per subreddit (`Subreddit` as `r/<name>`, `Why`, `Self-promotion rule` quoted with its rules page link, `Allowed`, `Checked`). Written once by `department add marketing` from the template's `files:` (never over an edited copy); agents read it and never change it. `issue-cli health lint`, check `departments`, warns about a row without a rule (empty, a dash or the `<placeholder>`), a name not shaped `r/<name>`, a subreddit listed twice and a table without its header. No file, no warning: Reddit is optional |
| `reddit` connector (commented example) | [reddit-mcp-buddy](https://github.com/karanb192/reddit-mcp-buddy) (MIT), run unchanged as `npx -y reddit-mcp-buddy@<pinned>`, read-only: app-only keys `REDDIT_CLIENT_ID: keychain:postiz-env/REDDIT_CLIENT_ID` and `REDDIT_CLIENT_SECRET: keychain:postiz-env/REDDIT_CLIENT_SECRET`, no username, password or refresh token, so its write tools don't exist, and `deny_tools: [create_post, add_comment]` on top; `sends: false`. A light Node process that lives as long as the agent session, so no `per_run:` |
| `social` connector (Postiz) | Posts to a subreddit (title, type self/link/media, flair) and threads a first comment under our own post, with the same Reddit app given to Postiz as `REDDIT_CLIENT_ID`/`REDDIT_CLIENT_SECRET`. `triggerTool` at brief and publish reads a subreddit's flairs and post requirements. Started per run and stopped after, like every Postiz action (#D-5). No Reddit analytics |

**On the social path.** research reads each listed subreddit through the `reddit` connector (top posts of the month with their permalinks, the questions that come back, one search for the brand and each competitor), never one that isn't listed, and proposes new ones in the report; without the connector it reads nothing from Reddit's site or a mirror. brief plans a post or reply only in a listed subreddit, quotes its self-promotion rule, reads its current flairs and post requirements through `social`, and says where they differ from the list; the `## Brief` box "Each Reddit post or reply names a subreddit from departments/<department>/subreddits.md, quotes its self-promotion rule and keeps within it" holds brief → draft (`check --na` when no Reddit). draft gives each post its subreddit, title, type, flair, body and optional first comment, and puts replies in other people's threads under `### Replies (posted by the owner)` with each thread's permalink. publish, after the owner_only gate, schedules the approved posts through `social` and sends no reply: the owner posts those by hand (no tool we run can reply in someone else's thread without a user login). measure writes "no value yet" for Reddit.

**Keys.** The connect-socials page puts the Reddit app's id and secret into Keychain service `postiz-env`, accounts `REDDIT_CLIENT_ID` and `REDDIT_CLIENT_SECRET` (its scheme for every network's values), and passes them to Postiz; the `reddit` connector reads the same items, so the owner saves them once. By hand: `security add-generic-password -s postiz-env -a REDDIT_CLIENT_ID -w` (and `REDDIT_CLIENT_SECRET`). Nothing goes in the repo (factory/department-connectors#D-1). The redirect URI for the Reddit app is `<postiz>/integrations/social/reddit`.

`TestMarketingRedditExampleIsReadOnly` keeps the example read-only and the starter written, `TestSocialPromptsTeachReddit` the prompts and the Brief box, `TestSubredditsLint` the list's warnings.

#### Publishing to the site

A `piece` or an `seo-pass` puts an approved post or page on the project's site through the department's `cms` connector (Ghost by default: the CMS spike's pick, factory/spike-a-simple-self-hosted-cms-for-projects-marketing-pieces-picked-tried-and-mapped-onto-the-publish-step#D-1), which runs the helper `departments/<key>/cms-ghost.sh` with the key from Keychain. Only publish calls it, after the `owner_only` gate; the connector's `needs_approved_output` holds a call until the newest shelf version is approved as well.

- **brief** (`piece`): where it will be published names `site (cms)`, post or page, when the department has the connector (`issue-cli connectors`). No `cms` call.
- **draft**: a piece or a refreshed page for the site goes on the shelf as the markdown file that will go out (`--file`), sent byte for byte at publish. No `cms` call: the draft stays on the shelf.
- **publish** (`piece`): the approved output's id and file from `issue-cli outputs --issue <slug> --state approved --json`, then `issue-cli connector exec marketing/cms -- sh departments/marketing/cms-ghost.sh publish --file <it> --title "<title>" --kind post|page`, which prints `{"id","status","url"}`; that URL goes to `issue-cli output publish <id> --url <url>` and under `## Published`. A place the brief names other than the site (an account, a list) uses that place's connector.
- **publish** (`seo-pass`): per approved output, a page or post that already exists is `get --slug <slug>` for its id, then `update --id <id> --file <it>`; a new post is `publish`. One `output publish --url` per output.
- **No `cms` connector** (no site yet, or the entry still commented): publish sends nothing. The worker names the approved file and asks the owner to put it out and leave an `answer:` comment with the link, which `output publish --url` then records.

Catalog entry `cms-publish`; `TestPieceAndSeoPassPublishThroughCms` and `TestBriefAndDraftMakeNoCmsCall` keep the prompts and the `owner_only` gate.

#### Page rules

Every page a Marketing department ships or measures keeps two rules, on every project (the owner, 2026-11-20, after a site's six articles all had 66–78-character titles, Bing's "Title too long", and served `text/html` with no charset, so Bing read their OpenGraph as Latin-1):

- **`<title>` at most 60 characters including the brand suffix**, the primary phrase first. The h1 and og:title may be longer.
- **`charset=utf-8` in the Content-Type header** of every text response: HTML, sitemap.xml, robots.txt, llms.txt, feeds. A `<meta charset>` is not enough, since a crawler goes by the header.

`issue-cli page-check <url>` checks a page, `issue-cli page-check <site>/sitemap.xml` every page in the sitemap: one line per URL with its status, Content-Type and title length, the rule a failing URL breaks and its fix, `page-check: PASS` or `FAIL (n of m URL(s) …)`, exit 1 on a failure, `--json` for a script. It makes only GETs, one at a time, at most `--max` (500) URLs, and needs no project or connector. Where the department meets it:

- **role.md**, `## Page rules`: the two rules and the command.
- **The Draft box** the base brief → draft appends (piece, seo-pass): `Page rules: … checked by issue-cli page-check <url>`; draft → owner review waits for it. A piece with no URL before publish ticks it with "no page yet" and checks the published page.
- **seo-baseline's `## Technical basics`**: the box for the whole site (the server or CDN sends the header, the article template keeps titles short).
- **The weekly SEO pass** runs page-check on each site's sitemap, writes its last line in the brief, and opens one fix per site listing every failing URL (`<site>: page rules (title length, charset)`), unless one is already open.

A live Marketing gets role.md and the job brief from `issue-cli department update marketing` (when unedited), and the two boxes from the catalog entry `page-rules`, which `issue-cli workflow upgrade --department marketing` proposes. `TestMarketingTemplateHasThePageRules`, `TestPieceWaitsForThePageRulesBox` and `TestInstalledMarketingGetsThePageRules` hold them.

#### Adding the next channel

PPC and cold outreach are commented-out slots at the end of `types:` in the marketing workflow. Adding one is a change to the workflow file (yours, or the bundled template for every project), not engine code:

1. **Uncomment its type** in `workflows/marketing.yaml`: remove `# ` from each line between `# BEGIN channel <name>` and `# END channel <name>`, in both blocks so marked (its eval under `evals:`, which health lint's `evals` check asks for, and its type under `types:`). Or copy `seo-pass`. Give it its own `statuses:` prompts: what a brief covers for that channel, what the draft is (ad copy, a post with images, an email sequence), what to measure (spend and conversions, reach, replies).
   - **`social` shipped this way first** and is now live, with a research step ([above](#social-research-before-the-brief)): brief reads each network's rules through the connector's read calls (or the network's published limits, said so), draft makes no connector call and puts one `--kind post` output on the shelf with a section per network, publish schedules only the approved posts at the approved dates (never "now" unless the owner's approval says so) and lists each post's id and link under `## Published`, measure reads each post's numbers through the `social-analytics` connector (a `kind: rest` connector, GET only) or, without it, the `social` connector's analytics calls only, or writes "no value yet" with the reason. The prompts name the department's connector `social`, never the tool behind it: add a connector named `social` for whichever tool you choose ([Swapping a partner](#swapping-a-partner)). `TestCommentedChannelSlotsLoadWhenUncommented` keeps the remaining slots loadable and lint-clean, with `owner review → publish` still `owner_only`.
   - PPC and cold outreach stay a description and a path: they spend or send to strangers, so their prompts are the owner's to write.
2. **Add its system** to the department in projects.yaml: `systems: [SEO, Social, PPC]` (or `Outreach`). Its issues go under `issues/<System>/` and show as a room and a filter.
3. **A recurring job** (optional): a `jobs:` entry with `type: <channel>` and its own brief, for example a weekly social calendar.
4. **Check it**: `issue-cli health lint` (a path entry that isn't a status, a section a gate checks but nothing on the path appends, or a job's `type:` the workflow lacks are warnings), then `issue-cli process workflow --department marketing`.

A channel whose work never goes out (research, reporting) can drop `owner review` and `publish` from its `path:`; one that doesn't measure leaves `measure` off and adds a `publish → done` transition, as `piece` does.

#### Upgrading an installed Marketing

A Marketing department installed before channels has the `content` workflow in `workflows/marketing.yaml`. Nothing rewrites it: `department add` keeps an edited file, and `issue-cli workflow upgrade --department marketing` only proposes the catalog's features for its own template (`content`). To move to channels:

1. Write the bundled file next to yours: `issue-cli workflow init --template marketing --out workflows/marketing.template.yaml`.
2. If you never edited yours, replace it with the new one. Otherwise copy across what you need: the `measure` status, the `publish → measure` and `measure → done` transitions, and `default_type:` / `types:`. Issues without `type:` become `piece` and keep their path.
3. Add `type: seo-pass` to the `weekly-seo-pass` job in projects.yaml, and run `issue-cli workflow scan --department marketing` to update the file's features record, then `issue-cli health lint`.

### Brand foundation and research mode

Before any channel, Marketing settles who the brand is and who it is for. Without that, every brief has to guess. The foundation is two types of work in the same workflow file, both in **research mode**. The owner names a topic (the business, a product, a market), the worker researches it (your own site(s), competitors, search results, reviews, forums), and proposes answers. The owner settles them at a review gate. The worker helps them decide and doesn't decide for them.

**`foundation`**: the basics, needed before channel work.

| Status | What the worker does |
|:--|:--|
| `research` | Lists each source under `## Research`, with its link and one line on what it says. If the issue names no topic, the worker asks the owner. Moving on needs at least 5 distinct http(s) links there (`section_min_links`). |
| `brand` | Proposes `## Brand`: 3–5 traits, the voice with a short example, do / don't guidelines, each with its source, and what the owner has to settle. |
| `icp` | Proposes `## ICP`: who (segments, roles, size, place), their problems in their own words, where they are, how to reach them (targeting, a message per segment) and who is not a target. Puts the brand and ICP drafts on the [shelf](#outputs-shelf) as `--kind report`. |
| `owner review` | Says in chat what to review and what is open. Moving on needs `publish` approval in the issue viewer and the report's newest version approved on the shelf (`outputs_approved: report`): a revision puts it back in review. A `revise:` comment sends the issue back to `icp`. When the owner only answers the open questions (an `answer:` comment, a shelf comment, chat), the worker revises in place: it updates `## Brand` and `## ICP`, puts the new version on the shelf with `output revise`, and stays in owner review. The gate is `owner_only` although publish only writes local files, because what it settles is the owner's call and every channel builds on it. |
| `publish` | Writes exactly what was approved to `departments/<key>/brand.md` and `icp.md`, next to the department's `role.md`, and marks the shelf report published. The text comes from the issue's settled `## Brand` and `## ICP` (the owner's answers applied, the same as the approved shelf version). Each file is markdown: `# Brand: <topic>` (or `# ICP: <topic>`), a `Settled <date> from <slug>, shelf <id> v<N>.` line, then the section without its checkboxes and with the owner's answers in place of the open questions. `publish → done` checks that both files exist. |

**`market-size`** walks research → market size → owner review → publish → done. `## Market size` gives TAM, SAM and SOM, each with its number and unit, the method (top-down from a published figure, or bottom-up from counts × price), the sources and every assumption. A number that can't be supported is "no value yet" with the reason. SAM starts from the ICP. At publish it writes `departments/<key>/market.md`. It gates nothing, so run it once the ICP is settled and again when the market moves.

**Basic first.** The shared `brief → draft` transition, which `piece` and `seo-pass` both walk, checks `departments/<key>/brand.md` and `icp.md` with the [`file_exists`](workflow.md#files) validator. Until both are there, the refusal names the missing file and gives the command that starts the foundation:

```
departments/marketing/brand.md is missing — the brand foundation comes first: issue-cli create --department marketing --type foundation --title "Brand foundation: <topic>"
```

That includes the weekly SEO job's runs: a run waits at `brief` until the foundation is published. `issue-cli health lint` warns about the missing files ahead of time. Files written some other way (by hand, or from before this change) count too: the gate checks that the file exists and isn't empty, not which issue wrote it.

**`keyword-research`** settles the words: see [Keyword research for positioning](#keyword-research-for-positioning) below. It gates nothing either.

**Where the results are read.** The template's `role.md` tells every worker of the department to read `brand.md`, `icp.md`, `market.md`, `keywords.md` and `competitors.md`. The channels' `brief` prompts say to cite them, to target a `keywords.md` cluster marked own or contest where the file exists, and to name the ICP segment the piece is for. `<key>` is the department's key, so a Marketing installed as `--name Growth` reads `departments/growth/`.

**Memory.** Research is web-heavy, so the research prompt carries the [heavy-tools line](#running-partners-on-this-machine).

```
issue-cli create --department marketing --type foundation --title "Brand foundation: <topic>"
issue-cli create --department marketing --type market-size --title "Market size: <topic>"
issue-cli create --department marketing --type keyword-research --title "Keywords and positioning: <topic>"
issue-cli create --department marketing --type competitor-scan --system SEO --title "Competitor scan: <topic>"
```

#### Keyword research for positioning

The foundation says who the brand is for; it doesn't say which words they search with, which of those the department can own, which to leave to bigger players, or what positioning line the words support. A `keyword-research` issue does, in research mode like the foundation, and settles the answer with the owner.

| Status | What the worker does |
|:--|:--|
| `research` | **Free data first**: Search Console queries for your site(s), through the `openseo` connector when the owner turned it on (free without a DataForSEO key); search autocomplete and related searches; competitors' titles, headings and landing pages; forums, reviews and communities where the ICP talks; the site's own search terms. Paid keyword volume only through `openseo` with the DataForSEO key the owner keeps in Keychain, inside the pass's cost cap (`maxCostUsd`); no key is ever written into a file, and the template adds no other connector. Each source goes under `## Research` with its link; moving on needs at least 5 distinct links (`section_min_links`). The prompt carries the heavy-tools line. |
| `shortlist` | Reads `brand.md`, `icp.md` and `market.md` where they exist. `## Keywords`: one entry per cluster with the ICP's own words and where each was seen, the intent (informational, commercial, transactional, navigational), a difficulty guess (low / medium / high and who ranks today), our fit (**own**, **contest** or **leave**, and why) and the sources. A volume with no source is "no value yet". `## Positioning`: one proposed statement, the clusters it rests on and what it leaves to others, then what the owner has to settle. Moving on needs the `## Keywords` boxes ticked and the draft on the shelf (`has_output: report`; the refusal gives the `output add` command). |
| `owner review` | As for the foundation: `revise:` sends it back to `shortlist`; answers alone are revised in place with `output revise`. Moving on needs the newest shelf version approved (`outputs_approved: report`) and `publish` approval, which is `owner_only`: the positioning is the owner's call, so YOLO never passes it. |
| `publish` | Writes the approved `## Positioning` and `## Keywords` to `departments/<key>/keywords.md` (`# Keywords and positioning: <topic>`, a `Settled <date> from <slug>, shelf <id> v<N>.` line, the positioning line first) and marks the shelf report published. It doesn't touch `brand.md` or `icp.md`; a positioning that differs from `brand.md` is said in chat, and a new `foundation` settles it. `publish → done` checks `keywords.md` exists. |

`keywords.md` gates nothing: `piece` and `seo-pass` briefs read it where it exists and target a cluster marked own or contest, never one marked leave. Lint warns about it only once a `keyword-research` issue has run to owner review and the file still isn't written (as for [competitors.md](#competitor-scan)). Every run is scored by the `keyword-research` eval ([Evals](benchmark.md#evals)).

**A worked example** (the fixture `cmd/issue-cli/testdata/keyword-research/plumbing.md`, a placeholder one-van plumber, which `TestKeywordResearchWorkedExample` walks from `create` to `done`):

| Cluster | The ICP's words (source) | Intent | Difficulty | Our fit |
|:--|:--|:--|:--|:--|
| Boiler repair | "boiler repair near me", "boiler repair same day" (Search Console, autocomplete) | transactional | medium | own |
| Landlord boiler service | "boiler service for rentals", "gas safety certificate" (forum) | commercial | low | own |
| Emergency plumber | "emergency plumber 24/7" (competitor A) | transactional | high | leave: no night cover |
| Drain unblocking | "blocked drain" (Search Console, competitor B) | transactional | medium | contest |

Positioning: **"The same-day boiler plumber landlords can book at a fixed price."** It rests on Boiler repair and Landlord boiler service, and leaves emergency night calls to others. For the owner to settle: is "fixed price" a promise kept on every job?

An installed Marketing from before gets the type as the `keyword-research` catalog feature: `issue-cli workflow upgrade --department marketing` proposes it once `brand-foundation` is in. Copy the `shortlist` status, the `keyword-research` type and its eval from the bundled file, add `keywords.md` to `role.md`'s list, then run `issue-cli workflow scan --department marketing`.

#### Competitor scan

The social research and the SEO briefs need to know who competes and which queries matter. A one-off `report` can propose that list, but it settles nothing, so nothing later reads it. A `competitor-scan` issue (system `SEO`) runs the scan in research mode and settles the list with the owner in `departments/<key>/competitors.md`. It uses **web search only**: no `openseo` call, no keyword-volume or rank API, no log-in, nothing bought. Nothing leaves the machine.

| Status | What the worker does |
|:--|:--|
| `research` | Reads `brand.md`, `icp.md` and, where it exists, `keywords.md`. Builds the query set from the ICP's search terms and the clusters, by intent (informational, comparison such as `<competitor> alternative`, transactional), each with its ICP segment. Runs each query through its own web search tool and reads public pages, recording the result domains and their page types per query. Lists the sources under `## Research`. Moving on needs at least 5 distinct links (`section_min_links`) and the brand foundation: `research → competitors` checks `brand.md` and `icp.md` with `file_exists`, and the refusal gives the `--type foundation` command. The prompt carries the heavy-tools line. |
| `competitors` | `## Competitors`: **Direct** (one row per competitor: key, name, URL, licence or pricing, or "check" when no source confirmed it, segment, what to watch for, with its source), **Adjacent** (another way to solve the same problem, and why it matters), **Queries** (each with its intent, its segment, what ranks and whether the project appears), **Topic gap** (topics ranked by how many queries they come up in, page types the competitors use that the project lacks, suggested pieces; a frequency is not demand), **Method and limits** (what a web-search scan can't know: volumes, difficulty, real SERP features, backlinks), and the **proposed competitors.md**. Moving on needs those boxes ticked and the report on the shelf (`has_output: report`). |
| `owner review` | The same as for keyword research: a `revise:` comment sends it back to `competitors`, and answers alone are revised in place with `output revise`. Moving on needs the newest shelf version approved (`outputs_approved: report`) and the `publish` approval, which is `owner_only` because every SEO and social brief reads the list. YOLO never passes it. |
| `publish` | Writes the approved file to `departments/<key>/competitors.md`: `# Competitors: <topic>`, a `Settled <date> from <slug>, shelf <id> v<N>.` line, `Refresh:` and `Rule:` lines (name competitors in internal notes, never knock them by name in copy), then `## Direct` (`Key \| Name \| URL \| Licence \| Segment \| Watch for`), `## Adjacent` (`Key \| Name \| URL \| Why`), `## Queries` (one per line in a fenced block, `intent \| segment \| query \| target piece (or —)`) and `## Baseline (<date>)` (where the project appeared). A re-scan replaces the file. It doesn't touch `brand.md`, `icp.md` or `keywords.md`. Marks the shelf report published with no `--url`. `publish → done` checks that `competitors.md` exists. |

**Who reads it.** `competitors.md` gates nothing. The `seo-pass` brief and the weekly SEO job's brief (`jobs/weekly-seo-pass.md`) read its `## Queries` and `## Direct` where it exists. They re-run a few of the queries the pass's pages target, and say in the brief which competitor ranks now and whether the site appears, compared with `## Baseline`. A new name goes in the brief as a proposal for the owner and is never written into the file. A full re-scan is a new `competitor-scan` issue. Where the file doesn't exist, the brief says "No competitors.md yet" with the create command and goes on. The `social` research reads the same file for similar profiles and competitors ([Social: research before the brief](#social-research-before-the-brief)). Every run is scored by the `competitor-scan` eval ([Evals](benchmark.md#evals)).

**Lint.** `issue-cli health lint` (the `departments` check) names a missing `competitors.md` only once a `competitor-scan` in the department has run to owner review, and names the issue that will write it. The rule is general: a type's own `file_exists` path counts once an issue of that type stands at or after the `from` of its path's first human-approval edge. So `keyword-research`'s `keywords.md` and `market-size`'s `market.md` warn at the same point. A department that never ran one hears nothing. `TestFileGateWarnsForATypeOnlyPastOwnerReview` covers this; `TestCompetitorScanTypeShape` and `TestCompetitorScanWalksToCompetitorsMD` cover the type.

**The worked example** is a scan Dark Factory's own Marketing ran on 2026-11-02 as a `report`, before this type existed (output d5d9 on its board, `issues/.outputs/files/product/o-20261102-d5d9-competitor-keyword-scan.md`). It read `brand.md` and `icp.md` and ran 36 web searches in three intents. It listed 9 direct competitors (open-source agent orchestrators and "AI company" tools) and 6 adjacent ones, each with its licence or "check". For each query it recorded what ranked: vendor blogs, directories, "N best alternatives" lists and repos. The baseline was that the product appeared in none of the 36 result sets. It ranked the topic gap (alternatives and comparison pages came up in 10 of the queries) and said what it couldn't know (volumes, difficulty, real SERP features). Its §5 is the proposed file, in the format `publish` writes. As a `report` it could only propose that file, never write it, and that gap is why this type exists. Dark Factory has had no Marketing of its own since 2026-11-15, so the type lives in the template for projects' Marketing departments.

An installed Marketing from before gets the type as the `competitor-scan` catalog feature: `issue-cli workflow upgrade --department marketing` proposes it once `brand-foundation` is in. Copy the `competitors` status (and its board column), the `competitor-scan` type and its eval from the bundled file, and the competitors.md lines in the `seo-pass` brief prompt. `issue-cli department update marketing` brings `role.md` and `jobs/weekly-seo-pass.md` up to date where you haven't edited them. Then run `issue-cli workflow scan --department marketing`.

#### Adding the foundation to an installed Marketing

`department add marketing` installs it. A department file from before it gets it as the `brand-foundation` catalog feature: `issue-cli workflow upgrade --department marketing` proposes it with a snippet. `upgrade` only proposes a template's own entries, so a Marketing file still stamped `template: content@…` needs its stamp set to `template: marketing@v0.57.0` first. The same pass then also proposes `types-of-work` and `measure-after-publish` ([Upgrading an installed Marketing](#upgrading-an-installed-marketing)). Copy the four statuses (`research`, `brand`, `icp`, `market size`) with their prompts, their transitions, the `foundation` and `market-size` types, and the two `file_exists` validates on `brief → draft` from the bundled file (`issue-cli workflow init --template marketing --out workflows/marketing.template.yaml` writes it next to yours). Then add the `## Brand, ICP and market` section to `role.md` and run `issue-cli workflow scan --department marketing` and `issue-cli health lint`. Once the gate is in, open the first `foundation` before the next channel brief.

## Recurring jobs

A department can have work it does on a calendar: a weekly SEO pass over your site(s), a daily lead search, monthly invoice chasing. Each is a **job** in its `departments:` entry, and each time a job is due it makes a **run**: an issue in the department, started like any worker.

```yaml
departments:
  - name: Marketing
    workflow: workflows/marketing.yaml
    systems: [SEO]
    jobs:
      - id: weekly-seo-pass
        name: Weekly SEO pass
        every: week          # day | week | month
        on: mon              # week: mon..sun (default mon); month: 1..28 or last (default 1)
        at: "07:00"          # local time on this machine (default 00:00)
        brief: departments/marketing/jobs/weekly-seo-pass.md   # from the project root
        priority: medium     # optional: the run issue's priority
        type: seo-pass       # optional: the run's type of work (default: the workflow's default_type)
        overlap: false       # optional: true starts a new run while the last one is open
        since: 2026-11-21    # optional: the first day; a period due before it is never due
```

A job's key is `<department>/<id>` (`marketing/weekly-seo-pass`).

`brief:` is a file from the project root, or `builtin:<name>` for a brief bundled with the engine (`cmd/issue-cli/templates/jobs/<name>.md`; today `builtin:memory-consolidate`, [Shared Memory](memory.md#the-consolidation-pack-and-job), `builtin:nightly-improve`, [Every night](benchmark.md#every-night), `builtin:decision-drift`, [the drift sweep](workflow.md#decision-ledger), `builtin:retro-sweep`, [`retro`](CLI/overview.md#retro), `builtin:lead-flush`, [the nightly flush](patterns/lead-session.md#the-nightly-flush), and `builtin:growth-scout`, [the growth scout](#the-growth-scout)). A brief may use `{{slug}}`, the run issue's own slug.

**Jobs on Product.** The `Product` entry may list `jobs:` too (key `product/<id>`): the engine's own recurring work, such as the nightly loop. Its runs are Product issues under `Product/` with no `department:` field, on Product's workflow, and the CEO ticks them. HQ still runs no jobs (lint warns). Source: factory/nightly-self-improvement-loop-benchmark-on-main-one-queued-experiment-and-the-mornings-better-or-worse-numbers-on-the-report#Q1 (assumed under YOLO). A bundled brief needs no file in the project and changes with the engine; lint names the bundled ones when a `builtin:` is unknown.

### The growth scout

`brief: builtin:growth-scout` is a recurring job that looks outward: distribution channels and growth-hacking ideas, each written as a cheap experiment for the owner to pick. A Marketing department (or a GTM group whose workflow has a `report` type) schedules it, for example monthly:

```yaml
      - id: growth-scout
        name: Growth ideas
        every: month
        brief: builtin:growth-scout
```

- **Its runs are reports.** With no `type:`, a growth-scout run is type `report` (research → owner review → done on the `marketing` template), never a `piece` that walks to publish. A workflow without a `report` type refuses the run, and `issue-cli health lint` (`departments`) warns with the fix: `issue-cli workflow upgrade --department <dept>` proposes it (catalog `report-type`).
- **Inputs.** The README and site, `departments/<key>/brand.md`, `icp.md`, `competitors.md` and `market.md` when present, `issue-cli goals <key>`, and the earlier reports (`issue-cli outputs <key>`) and the previous run, so ideas already picked, tried or rejected are not proposed again.
- **The report.** 10-15 experiment cards sorted by ICE: idea and channel, hypothesis with its evidence, the first cheap test (under a day of agent work and $0-20), metric and threshold, risk (**ask Legal** when it touches personal data or third-party terms), needs (none, an outside account, money) and ICE. On the shelf with `output add --kind report`, plus a `report:` comment.
- **Nothing is executed.** No account, post, email, ad or spend. The owner picks on the shelf; the department lead files a picked idea as an issue in the department (a channel type, or a piece). An idea that needs the engine changed is a request via the CEO (factory/marketing-brand-foundation-brand-analyst-icp-and-tamsamsom-before-channel-work#D-8). Its standing start is off until the owner turns it on, like every job.

### A run is an issue

`issue-cli job run` creates the run as `<System>/<id>-<period>` (the department's first system, else its name), for example `SEO/weekly-seo-pass-2026-w44.md`:

- **Fields:** `department:`, `job: marketing/weekly-seo-pass`, `run: 2026-W44`, the label `job`, the job's `priority:`, `type:` when the workflow has types, and a `slug:` pin.
- **Type:** the job's `type:`, else the type its bundled brief needs (`builtin:growth-scout`: `report`; a workflow without it stops the run with the fix), else the workflow's `default_type`. A type the department's workflow doesn't define stops the run with the types it has, and `issue-cli health lint` warns about it. Marketing's weekly pass is an `seo-pass`.
- **Status:** the first status of the run's type in the department's workflow. On the `marketing` template that is `brief`.
- **Body:** the brief file, with `{{period}}`, `{{date}}` and `{{last_run}}` filled in, then a `## Run` line naming the job and the previous run.

Its worker is dispatched like any other, with `## Your department` in its briefing. Everything the engine already keeps for an issue is the run's record: the gates on its path, the Timeline, the token estimate in its stats sidecar, and the outputs it puts on the shelf (`output add --issue`). On `content` and `marketing`, nothing goes out before `owner review → publish`. Under YOLO, that gate waits in YOLO's after-the-fact review instead.

- **Periods.** A daily run is keyed `2026-10-30`, a weekly one by ISO week (`2026-W44`), a monthly one `2026-10`. A period has one run. A second `job run` in the same period names the existing run and creates nothing, unless you add `--again` (which makes `…-2`).
- **Due.** A job is due once its period's `on`/`at` has passed and that period has no run.
- **The first period (`since:`).** A job registered mid-period is otherwise due at once for the period it was added in, even one whose due time has passed. `since: YYYY-MM-DD` makes every period due before that day not due and not missed: `job due` shows the first period on or after it as next. To skip a period, move `since:` past its due time. An owner's `job run --quote` before `since:` makes the first period's run.
- **Missed periods don't stack.** If nobody ran the job for three weeks, there is one run, for the latest week. The run log notes `missed: 2`.
- **Open runs block the next.** While the last run is still open, the next one is held ("last run … is still in draft"), unless the job sets `overlap: true`.

### Who starts a run

The engine runs nothing on a timer: no daemon, cron or watcher creates or starts a run. Runs start in one of three ways:

1. **You.** Press **▶ Start run** on the job's row in Needs you, or tell a lead, who runs `issue-cli job run <job> --quote "<your words>"`. This works whether or not the job is due.
2. **The lead's check-in.** At each check-in a lead runs `issue-cli job due`. For each due job it prints one verdict and the one command to run:
   - `start it now`: run `issue-cli job run <job> --scheduled`.
   - `waits for the owner`: the job is in Needs you.
   - `hold: <reason>`: the job is held, and the hold clears by itself.

   The department's lead ticks its own department's jobs. The CEO ticks the jobs of departments whose lead isn't live.

   A lead's tick that runs `queue next` or `autostart next` and never `job due` still sees the jobs it may start: each prints `⚠ job <job> is due (<period>) on its standing start: issue-cli job run <job> --scheduled`, and `queue next --start` starts that run first, through the same checks as `job run --scheduled`, before any queue item. A hold is printed and the queue gets its turn.
3. **The job's standing start.** This is the only unattended start, and it is off by default. `issue-cli job auto on <job> --quote "<your words>"` (or `--all` for every job of the project) stores your words in `.jobs/<project>.json` in the main checkout, next to `.autostart/`, never committed. From then on, `job run --scheduled` may start the due run without you naming it, inside the project's budget:
   - `mem-guard check` passes;
   - memory is under the project's line: auto-start's `--below` while its window is on and not paused, else the queue's (`issue-cli queue limits --below`) when the lead set one, else 50%;
   - the 1- and 5-minute load are not both at or over the core count (or `max_load` while the project's auto-start is on);
   - the project has fewer live workers than its cap, from the same place: auto-start's `--max-workers`, else the queue's `--max-workers`; a queue with none has no cap (memory is the limit), as `queue next`;
   - no project on this machine started a worker within the gap: auto-start's `--gap` while its window is on and not paused, else 5 minutes. This is the same lock and stamp as `dispatch --autostart`.

   These are the limits `queue next` decides with: both read one function (`agentrun.StartLimits`), so a job and a queue item are never held by different lines.

   If any of these fails, nothing starts and the reason shows in `job due`, naming the limit and where it came from: `memory 51% ≥ 50% (auto-start default)`, `memory 80% ≥ 75% (acme queue limit)`, `2/2 workers live (acme auto-start limit)`; with no cap the limits line says `no worker cap (acme queue default)`. `job auto on` prints the limits in force now. Only you, or a lead with your words, turns a standing start on or off (`job auto off <job>|--all`). Workers are refused. Each closed window goes to `.jobs/<project>.log.jsonl`.

Every run's `dispatch:` comment says how it started:

```
dispatch: "run the SEO pass now" (CEO chat; recurring job marketing/weekly-seo-pass, run 2026-W44, 2026-10-29) — recorded by lead-acme
dispatch: "for now all" (recurring job marketing/weekly-seo-pass, run 2026-W44, unattended, turned on by lead-acme in the lead chat 2026-10-29; memory 31%, 2026-10-29) — recorded by lead-acme-marketing
```

### The run log

`<issues>/.jobs/runs.jsonl` sits next to `.outputs/` and `.goals/`, so a private tracker keeps it private too. It has one line per event:

- `due`: once per job and period, with `missed`.
- `hold`: again only when the reason changes.
- `start`: with `how: unattended|owner`, the words, who started it, the memory reading and the issue.

```json
{"op":"due","job":"marketing/weekly-seo-pass","run":"2026-W44","date":"2026-10-29T12:00:00+01:00","missed":1}
{"op":"start","job":"marketing/weekly-seo-pass","run":"2026-W44","date":"2026-10-29T12:05:00+01:00","issue":"seo/weekly-seo-pass-2026-w44","how":"unattended","quote":"for now all","by":"lead-acme","memory":31}
```

### Where it shows

- **`issue-cli jobs [<dept>] [--json]`** shows each job with:
  - its schedule and when it is next due;
  - the last run: issue, period, status, token estimate, outputs and how many wait for review;
  - the verdict when it is due;
  - the standing start, with the command to turn it on.

  With no jobs configured, it prints the config snippet.
- **`issue-cli department show <dept>`** lists the department's jobs.
- **`issue-cli lead whoami`** prints `Jobs: 2 jobs, 1 due (1 waits for the owner). At each check-in: issue-cli job due` for the jobs in the lead's scope. The lead briefs carry the check-in rule while their scope has jobs.
- **Needs you** has a **Due jobs** section: one row per due job without a standing start. The row shows the brief's first line, the last run and its tokens, and **▶ Start run**. The row counts in every "waiting on you" number.
- **The company page:** each department room lists its jobs: ⏱ name, schedule, next, last run (linked to its station), `due · waits for you`, `due · lead starts it` or `due · held`, and auto on/off.
- **`status.md`** has a line: `Jobs: 3 jobs, 1 due waiting for you, 1 held`.
- **`issue-cli health lint`**, check `departments`, warns about:
  - a job with no id, or a duplicate id;
  - a schedule that doesn't parse;
  - a missing `brief:` or brief file;
  - `jobs:` on HQ or on the Product entry (never run);
  - an open run of a job that projects.yaml no longer has.

## Connectors

A department lists the **outside tools** its agents may use: QuickBooks for an Accountant, Search Console or WordPress for Marketing, an ad account for PPC. A connector reaches only that department's lead and workers. Its secrets stay where the owner keeps them, in an env var or the macOS Keychain. The engine never writes a secret value into projects.yaml, issue files, sidecars, logs, the clilog, prompts or pages.

```yaml
departments:
  - name: Marketing
    connectors:
      - name: search-console          # [a-z0-9-], unique in the department
        kind: mcp                     # an MCP server the agent loads
        command: npx
        args: ["-y", "@example/gsc-mcp"]
        secrets:                      # env var the server gets → where the owner keeps it
          GSC_TOKEN: keychain:acme-gsc
          GSC_QUOTA_KEY: keychain:acme-gsc-quota
        optional: [GSC_QUOTA_KEY]     # runs without these when they aren't set
        check: "gsc-cli whoami"       # optional, read-only: exit 0 is healthy
        note: "Search Console for the client roster"
      - name: wordpress
        kind: tool                    # a command run through issue-cli connector exec
        command: wp
        secrets:
          WP_APP_PASSWORD: env:ACME_WP_PASSWORD
        check: "wp --info"
        sends: true                   # it can publish or spend
      - name: openseo
        kind: mcp                     # a remote MCP server (Streamable HTTP)
        url: http://localhost:3011/mcp
        headers:                      # header → [prefix ]reference
          Authorization: "Bearer keychain:openseo"
        deny_tools: [submit_indexnow_urls, set_report_sharing, set_ai_visibility_schedule]
      - name: postiz                  # one key drafts and posts
        kind: mcp
        url: https://postiz.example.com/mcp
        headers:
          Authorization: "keychain:postiz"
        sends: true
        needs_approved_output: true   # tools/call held until the issue's output is approved
        read_tools: [list_integrations]
        at:                           # the tools each status of the issue allows
          draft:
            allow: [list_integrations, create_post]
            deny_args:
              create_post: {type: [now]}  # drafts only: type: now posts at once
          publish:
            allow: [create_post]
            deny_args:
              create_post: {"posts[].type": [now]}  # a path: the type of every item of posts
      - name: social-analytics        # a read-only REST API: GET only
        kind: rest
        url: http://127.0.0.1:4007/api
        headers:
          Authorization: "keychain:postiz"
        get: [/public/v1/analytics/post/*, /public/v1/posts]
```

| Field | Meaning |
|---|---|
| `name` | Required. With the department it makes the address: `marketing/search-console` |
| `kind` | `mcp` (an MCP server loaded into the agent's session: a command on stdin/stdout, or a remote one at `url:`), `tool` (a command the agent runs through `connector exec`) or `rest` (a read-only REST API at `url:`: `connector exec` makes GETs on its `get:` paths, nothing else) |
| `command`, `args` | What runs. An `mcp` connector needs `command:` or `url:`. For a `tool`, the agent may pass its own command after `--` instead |
| `url` | A remote MCP server speaking Streamable HTTP, instead of `command:` (`mcp`), or a `rest` connector's API base, which its `get:` paths are read under. `https://` on any host, `http://` only on this machine (`localhost`, `127.0.0.1`, `::1`), no user or password in it. `connector exec` bridges stdin/stdout to it itself: no npm bridge, nothing fetched at run time |
| `get` | A `rest` connector's allow-list: paths under `url:` it may read, each a pattern where `*` is one segment (`/public/v1/analytics/post/*`). The query is the caller's. `health lint` warns on a `rest` connector without `url:` or `get:`, with `command:`, on an entry that isn't a clean path starting with `/`, on `get:` on another kind, and on an entry whose first segment is a wildcard (`/*`, `/*/status`): it opens every route at the API's first level, including any that changes state on GET, so the fix names the first segment (`/<resource>/status`). Postiz's `social-analytics` entry, wildcards only at the end, passes |
| `headers` | For a `url:` connector: header → where its value is kept, `[<prefix> ]env:<VAR>` or `[<prefix> ]keychain:<service>[/<account>]`. The last word is the reference; the words before it are sent as is, so `"Bearer keychain:openseo"` sends `Bearer <value>`. A header's secret is set or missing like any other and is never optional |
| `secrets` | Env var → reference. A reference is `env:<VAR>` (read from the environment issue-cli runs in) or `keychain:<service>[/<account>]` (a macOS Keychain generic password). Nothing else is accepted |
| `optional` | Env vars from `secrets:` the connector runs without. One that isn't set is skipped: `connector exec`, the check and the launch pass only the keys that are set, and it reads **not set (optional)**, never missing. `health lint` warns about a name that isn't one of its secrets |
| `check` | A read-only shell command, run with the secrets; exit 0 is ok |
| `sends` | The connector can publish, post or spend. It is used only for an output the owner approved on the shelf, then `output publish`. The department's workflow needs an `owner_only` gate before that step, so YOLO never passes it; `health lint` warns when it has none ([YOLO Mode](Workflow/yolo.md#gates-yolo-never-passes)) |
| `needs_approved_output` | Opt-in: `connector exec` holds the connector until the worker's issue has an approved output on the shelf, the newest version approved and none still in review (the same rule as the `outputs_approved` gate). A `tool`, or a `command:` server, doesn't start at all: exit 1, nothing run, no secret read. A `url:` server loads as usual, but each `tools/call` of a tool not in `read_tools:` is answered with a JSON-RPC error and never sent; the shelf is read at each call, so an approval opens the next call in the same session. A lead or the CEO has no issue and is refused; `--as-owner` passes. Opt-in because `sends:` alone also marks tools run before any output exists, on the owner's word on the issue (Video's `openmontage`). `health lint` warns on it on a `command:` server, whose calls the bridge can't see |
| `read_tools` | With `needs_approved_output` on a `url:` server: the tools that only read, open before the approval, e.g. `[list_integrations]`. `health lint` warns when it does nothing (no `url:` or no `needs_approved_output`) and on a name that isn't a tool's |
| `at` | On a `url:` server: the tools each status of the worker's issue allows, `<status>: {allow: [...], deny_args: {<tool>: {<argument>: [<value>, …]}}}`. At each `tools/call` the bridge reads the issue's status again (a transition in the same session changes what passes) and answers a refused call with a JSON-RPC error, never sending it: a status `at:` doesn't name allows no tool; `allow:`, when set, names every tool callable there (unset: every tool); `deny_args:` refuses a call whose argument holds one of the values (a string as is, a number or `true` by its JSON text), and an argument the call leaves out passes. The argument is a top-level name, or a path into the call's arguments: names joined by `.` into objects, `[]` after a name for every item of an array, e.g. `socialPost[].type` (Postiz's per-post `type`) or `meta.mode`. A call is refused when any value the path reaches matches, and the refusal names where it sits (`socialPost[1].type: now`). `[]` on a value that isn't an array takes the value as the one item; a step that finds no such key reaches nothing (that part passes). A key with no `.` or bracket is a top-level name as written; a malformed path (`socialPost[.type`) refuses every call of that tool until it is fixed. Keys whose names hold `.` or brackets, indexes (`[0]`) and wildcards can't be written. It is asked before `needs_approved_output:`, and `read_tools:` don't skip it. A lead or the CEO has no issue and is refused; `--as-owner` passes. `tools/list` is not filtered. `health lint` warns on a status the department's workflow doesn't have, on `at:` without `url:`, on an entry with neither key, on a name that isn't a tool's and on a `deny_args:` path that doesn't parse |
| `deny_tools` | Tools of an `mcp` server the agent CLI switches off at launch, e.g. `[submit_indexnow_urls, set_report_sharing]`: a research server whose few tools send or spend stays `sends: false`. Each name is letters, digits, `_`, `.` or `-`; anything else is skipped and `health lint` warns. Nothing on a `tool` connector |
| `per_run` | A heavy tool that runs only while a run needs it: `start:`, `ready:`, `stop:`, an optional `timeout:` and an optional `tool:` id shared with other connectors that start the same tool. See [Per run](#per-run) |
| `spend` | A paid tool, metered: each billing call's cost recorded, refused once the month reaches `cap:`. See [Spend caps](#spend-caps) |
| `voices` | A `spend: meter: elevenlabs` connector's voices by placement, with consent for a real person's. See [Spend caps](#spend-caps) |
| `note` | A line on the room |

### Secrets

**Where each secret lives.** A key never goes in a file in a repo, this one or a project's:

| Secret | Where it lives | Read by |
|:--|:--|:--|
| A connector's key | Keychain, named `keychain:<service>[/<account>]` in `secrets:` or `headers:`; or an env var exported in the shell profile agents start from, named `env:<VAR>` | `connector exec` / `connector check`, into the child's environment only |
| A vendor CLI's own login (an OAuth token, a session) | the file the CLI writes in the home directory, outside every repo (the Kling CLI's `~/.kling/.credentials`) | the vendor CLI itself |
| A partner's own `.env` (OpenMontage) | the partner's own checkout, outside this repo | the partner; a Keychain key `connector exec` exports wins over it |
| What a project's app needs at run time | Keychain or the host's secret store; in the repo only `.env.example` with the names and empty values | the app |

`.env` and `.env.*` are git-ignored here and in the reel template (`reel init`), and the secret scan refuses a key or an env file in `make leak-check`, the commit-msg hook and, in every project with the guard, the pre-push guard ([The secret scan](workflow.md#the-secret-scan)). Source: factory/department-connectors#D-1.

The owner creates each one. Keychain example: `security add-generic-password -s acme-gsc -a "$USER" -w`. For env, export it in the shell profile agents start from. A secret is **set** or **missing** on this machine, and the engine decides which without reading the value: env by presence, Keychain with `security find-generic-password -s <service>` (without `-w`). The value is read only by `connector exec` and `connector check`, and only into the child process's environment.

### Running one

`issue-cli connector exec <dept>/<name> [-- <cmd> …]` is the only way in:

- **mcp**: it runs the server on stdin/stdout. Agents don't call it themselves: their MCP config points here.
- **Held until approved** (`needs_approved_output: true`): before anything starts, a `tool` or a `command:` server is refused for a worker whose issue has no approved output: `connector marketing/wordpress needs an approved output (needs_approved_output: true) and is held: <id> v2 still in review on the shelf for <slug> — the owner approves the newest version first …`. A `url:` server is held per `tools/call` instead (below).
- **Tools by status** (`at:`, `url:` only): each `tools/call` is checked against the issue's status at that moment: `connector bridge: connector marketing/postiz at draft: create_post with type: now is refused (at: draft: deny_args:); another value of type passes`, `… at publish: create_post with posts[1].type: now is refused (at: publish: deny_args:); another value of posts[].type passes`, `… at publish: list_integrations is not allowed (at: publish allows create_post)`, `… at measure: no tools are allowed (at: names draft, publish)`.
- **mcp with `url:`**: no child process. `connector exec` itself is the server the agent talks to: each JSON-RPC message from stdin is one POST to the URL with the `headers:` (their secrets read now, held only in memory), the `Mcp-Session-Id` the server handed out at initialize and its `MCP-Protocol-Version`. The answer, a JSON body or an SSE stream, goes back to stdout one message per line; at the end the session is closed with DELETE. Requests run side by side (a long tool call doesn't hold up the next one); initialize, notifications and replies go in order. A redirect is never followed (it would carry the header elsewhere) and a request the server refuses, or that gets no reply, is answered with a JSON-RPC error that names the HTTP status, never a header. Not covered: the server-initiated GET stream, resuming a broken stream, OAuth and the old HTTP+SSE transport. A command after `--` is refused.
- **tool**: it runs `<cmd>` after `--`, else `command:` with `args:`. The exit code passes through.
- **rest**: `connector exec <dept>/<name> -- <path> [<path>…]` makes one GET per path under `url:`, in issue-cli itself, with the `headers:` secrets read now: no child process gets the key. It prints one JSON line per path, `{"path": …, "status": …, "body": …}` (the body as JSON, or as a string when it isn't JSON; an `error` when it isn't a 2xx), and exits 1 when any answer isn't a 2xx, after every line is out. GET is the only method the client has, so no write call can be reached, however the call is worded. Every path is checked before anything starts. A path no `get:` pattern matches is refused and the refusal lists the patterns. So is anything but a clean path: a scheme or host, `..`, `.`, `//`, an encoded `/`, or a `#`. A redirect is never followed, a request times out after 30 s, an answer is capped at 4 MB, and a header value the answer repeats is printed as `[secret]`. A 401 or 403 names the header's reference, never its value. `needs_approved_output:` holds it whole, like a tool; `at:`, `deny_tools:` and `read_tools:` do nothing on it (lint says so). `issue-cli connectors` lists its paths and the call. With `per_run:`, all the paths of one call share one start.

Who may run it:

- the department's workers (`ISSUE_VIEWER_ISSUE_SLUG` names an issue of the department) and its lead (`ISSUE_VIEWER_LEAD_SCOPE=department:<key>`);
- **not** another department's agents, Product's workers or the CEO. The refusal names the owning department and its lead;
- not a shell outside any agent session. The owner adds `--as-owner` in their own shell; an agent session can't.

A missing secret, or a header's, stops the run before anything starts or is sent, naming the reference and how to create it. An `optional:` secret that isn't set doesn't: the child runs without that variable, so a department runs with only the keys the owner has set. This is a guard against mistakes, **not a security boundary**: anything running as the owner can read their environment and their Keychain.

### Per run

A tool that costs a lot of memory stays down until a run needs it. The owner writes how, on the connector; a connector without `per_run:` runs as it always did.

```yaml
- name: openseo
  kind: mcp
  url: http://localhost:3011/mcp
  headers:
    Authorization: "Bearer keychain:openseo"
  per_run:
    start: "docker compose -p openseo -f ~/Repo/openseo/compose.local.yaml start"   # returns once launched
    ready: "curl -fsS http://localhost:3011/health"   # read-only: exit 0 once it answers
    stop: "docker compose -p openseo -f ~/Repo/openseo/compose.local.yaml stop"     # safe when it is already down
    timeout: 60                                       # seconds, for start + ready and again for stop
    tool: openseo                                     # optional: other connectors that start this tool name it too
```

All three commands are required: `connector exec` refuses a block that lacks one and lint warns. They run with `/bin/sh -c` in the project root, get the connector's secrets, and their output is thrown away, like a check's, so a printed token leaks nothing and an MCP server's stdout stays clean. `start:` must return: a server that stays in the foreground goes behind `nohup … &`.

A **run** is one `connector exec`: one tool command, or an agent session for an MCP server. Several `kind: tool` calls start the tool several times; `connector exec <addr> -- sh -c '…'` keeps them in one run.

A `url:` server's run starts at its **first call**, not when the session loads it. The first time its tool is up, the bridge saves what the server answered to `initialize` and to its lists (`tools/list`, `prompts/list`, `resources/list`, `resources/templates/list`, each page by its params). Later sessions answer those from the saved copy, and `ping` locally, while the tool stays down. So a worker writing a brief never starts it. The first request the copy can't answer, a `tools/call` mostly, goes through `at:` and `needs_approved_output:` first, so a refused call starts nothing. Then it takes the hold below (mem-guard, `start:`, `ready:`), opens the server's session with the client's own `initialize`, and goes on. When mem-guard blocks it, or the start fails, that call gets a JSON-RPC error with the words below and mem-guard's line. The session goes on without the tool, and its next call tries again. Two calls side by side start the tool once. With no saved copy (the first session, a changed `url:`, a wiped state dir) the session holds the tool at launch, as a `command:` server always does, and saves the copy as it goes. The copy follows the server whenever a session lists while the tool is up. It lives in the state dir's `connectors/<project>/<dept>-<name>/listing.json` (0600): what the server says about its tools, never a header. `issue-cli connectors` shows such a tool as `starts per run at the first call, stopped`.

- **Up already.** The run joins it. Another run's tool needs no `mem-guard check`, since it costs nothing new. A tool that was up before the engine looked (the owner started it) is used and never stopped.
- **Down.** `mem-guard check` first, where mem-guard is installed. Blocked means nothing starts and `connector exec` exits with mem-guard's line, and the record says `held`. It doesn't wait: the run goes on without the tool, or stops and says so.
- **Starting.** `start:`, then `ready:` every half second until the timeout. If either fails, `stop:` still runs, since the tool may be half up. A second run waits on the first one's start.
- **After.** The last run out runs `stop:`, on any way out: the child's exit, EOF on an MCP server's stdin, SIGINT, SIGTERM or SIGHUP. A run killed with SIGKILL can't do that: its lease is an flock, so it ends with the process, and the viewer's sweep (every minute, or `issue-cli connector sweep [<dept>]`) stops what the engine started and nobody holds. The sweep only stops.
- **Stop fails.** The sweep tries again each round, and the record says `stop-failed`.

Each start, hold and stop is a line in `<issues>/.connectors/runs.jsonl` (`started`, `held`, `stopped`, `start-failed`, `stop-failed`, with the time taken and an exit code or mem-guard's line, never a command's output). What's held right now is machine state, in the state dir's `connectors/`. A session's agent CLI gets time for the start: the launch sets codex's `startup_timeout_sec` for that server and claude's `MCP_TIMEOUT` for the session, the timeout plus 10 s. A first call that carries the start gets it too: codex's `tool_timeout_sec` for that server is its own 60 s plus the timeout plus 10 s (claude's per-call wait is already long).

`connector check` doesn't start a stopped tool: it records `stopped`, which isn't failing, so a tool that's down on purpose raises no row. A start that fails `ready:` is the health signal then, and it shows as a Needs you row (`Marketing: openseo start failing`), as a failing stop does.

Turning a tool partner on stays the owner's: no template carries an active `per_run:` (Marketing's `social` example below is written commented, for the owner to uncomment), and a worker has no reason to write one. It isn't limited to some issue types: a `url:` server's tool starts for a session that calls one of its tools (or, before a copy is saved, one that loads it), and a `command:` server's for every session of the department that loads it.

**One tool, several connectors.** Two connectors that start the same tool (Marketing and Sales on one analytics server, or one department in two projects) each keep their own runs unless they say it's one tool. Without `tool:`, the first run out stops the tool under the other's run. With `tool: <id>` on both, the connectors share one set of leases on this machine, in the state dir's `connector-tools/<id>/`: a run joins the tool another connector's run brought up, and the last run out, whichever connector or project it belongs to, runs its own `stop:` with its own secrets and logs `stopped` in its own project's `runs.jsonl`. Either project's sweep leaves a tool another run holds alone. Each connector still uses its own commands, so write them to start and stop the same thing. The id is a folder name: `[a-z0-9._-]`, starting with a letter or digit, up to 64 characters. `connector exec` refuses anything else and lint warns about it. The folder holds the id, lease files and a started flag, no project name. Without `tool:`, nothing changes.

**Postiz, stopped after each run.** Marketing's commented `social` connector is the worked example: Postiz is about 2.4 GB over eight containers (its app, Postgres, Redis, Temporal with its own Postgres, Elasticsearch, admin tools and UI), and the owner's condition is that it is down after every action (open-source partners epic D-5). Postiz's containers are stopped after each run: the last run out runs `stop:`, a killed run leaves them to the sweep.

```yaml
- name: social
  kind: mcp
  url: http://127.0.0.1:4007/api/mcp
  headers:
    Authorization: "Bearer keychain:postiz"
  sends: true
  per_run:
    tool: postiz
    start: 'docker compose -p postiz -f ~/postiz/compose.yaml start'
    ready: 'for i in 1 2 3 4 5 6; do test "$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:4007/api/mcp)" = 401 || exit 1; sleep 2; done'
    stop: 'docker compose -p postiz -f ~/postiz/compose.yaml stop; ids=$(docker ps -q --filter label=com.docker.compose.project=postiz); [ -z "$ids" ] || docker stop $ids'
    timeout: 120
```

- `start:` starts the containers the owner created once with `up -d`; it pulls and creates nothing.
- Postiz's own compose file gives every service `restart: always`, which starts a stopped container again whenever Docker restarts, with no run holding it. The example's comment says to change it to `restart: unless-stopped` in the owner's copy.
- `ready:` passes once the MCP endpoint answers 401 to a request without its key six times in a row, 2 s apart: the backend is up behind nginx (502 while it starts, nothing before nginx). One 401 isn't enough: nginx inside the container marks its backend down for 10 s (`fail_timeout`) in each worker after a refused probe during the start, so a session that began on the first 401 got 502 from another worker (seen in the guided check). Twelve clean seconds outlast every mark. The key isn't needed for that, and a `url:` connector's header secret never reaches a per_run command anyway.
- `stop:` doesn't stop at `docker compose … stop`, which stops only the file's services under the active profiles: a service started under another profile, or an orphan the file no longer names, would stay up. It then stops every container labelled `com.docker.compose.project=postiz`. Both are safe when it is already down. `TestPostizStopReachesEveryContainerOfTheProject` shows the gap on a stand-in docker, and the guided check `scripts/checks/postiz-per-run.sh` runs one real call and finds no Postiz container running and `started` then `stopped` in `runs.jsonl`.

### Spend caps

A paid tool's connector carries a `spend:` block: `connector exec` records what each billing call cost and refuses a billing call once this calendar month (local time) has reached the cap the owner wrote, for this project. The two meters are the Video template's reel connectors:

```yaml
- name: kling
  kind: tool
  command: kling                    # the official Kling CLI, from PATH
  sends: true
  spend:
    meter: json                     # the cost is a number in the command's JSON stdout
    field: creditsConsumed
    unit: credits
    cap: 500                        # a month, this project: the owner's number
    free: [who_am_i, account, query_tasks, tool_list, element_list, element_get, motion_library_list, file_upload]
    refuse: [login, logout]         # the owner's, in their own shell
- name: elevenlabs
  kind: tool
  url: https://api.elevenlabs.io    # issue-cli makes the call itself
  headers:
    xi-api-key: keychain:elevenlabs-api
  sends: true
  spend:
    meter: elevenlabs               # the characters the API bills
    unit: characters
    cap: 100000
  voices:                           # by placement: a storyboard names the placement
    - placement: narrator
      voice_id: <ElevenLabs voice id>
      name: "Owner (clone)"
      real_person: "the owner"      # a real person's voice …
      consent: "2026-11-21, own voice"  # … is refused until this is noted
    - placement: actor-1
      voice_id: <id>
      name: "Library voice"         # no real_person: nothing to consent to
```

| Field | Meaning |
|---|---|
| `spend: meter` | `json` (a `tool` whose command prints JSON: the cost is the largest number under `field:` anywhere in its stdout, since a poll's final answer repeats the submission's) or `elevenlabs` (issue-cli's own calls: `tts`, whose cost is the answer's `character-cost` header, else the subscription's change (as for `music`, below), else the text's characters, and `music`; `check` and `voices search` are free) |
| `spend: unit` | What the amount counts, as the pages print it (`credits`, `characters`) |
| `spend: cap` | The month's limit, the owner's number. None set (or 0): every billing call is refused, saying so, and the room and Needs you show `no spend cap set` |
| `spend: free` | `meter: json`: subcommands that never bill. They run over the cap and record nothing. `--help` and `--version` are free too. Any other subcommand bills, so one the list doesn't know is capped |
| `spend: refuse` | `meter: json`: subcommands `connector exec` never runs |
| `spend: balance`, `balance_field` | `meter: json`: the free subcommand that prints the account balance as JSON and its key, read before and after a billing call whose output names no cost ([a missing cost](#spend-caps)). A `kling` command defaults to `account` and `availableRemainCredits` |
| `voices` | `meter: elevenlabs`: `placement`, `voice_id` (not a secret), `name`, an optional `model`, and for a real person's voice `real_person` with `consent` (where the consent is kept). A voice with `real_person` and no `consent` is refused, and lint warns |

**Running one.**

- `connector exec video/kling -- <subcommand> [<arg>…]`: a metered command runs only its own `command:`; the words after `--` are its arguments (a first word naming the command is dropped, so `-- kling query_tasks g1` works; `-- sh -c …` runs `kling sh -c …`, which kling rejects). stdout streams through unchanged; the first 8 MB are kept to read the cost.
- `connector exec video/elevenlabs -- tts --placement <p> (--text-file <f> | --text "…") --out <x.mp3> [--alignment <x.json>] [--model <id>]`: one POST to `<url>/v1/text-to-speech/<voice_id>/with-timestamps`, the key read into issue-cli only (no child gets it), no redirect followed. It writes the MP3 and, with `--alignment`, the timings exactly as ElevenLabs sends them (`characters`, `character_start_times_seconds`, `character_end_times_seconds`), which the reel kit reads. `-- voices` lists the placements and each real person's consent.
- **The check** of a `meter: elevenlabs` connector is built in (no `check:` needed; one written wins): `connector check video/elevenlabs` reads `GET <url>/v1/user/subscription` with the key and prints the plan and the credits used and left this period under the ✓ line (`starter plan, active: 1240 used, 28760 left of 30000 characters (credits) this period, until 2026-12-03`), kept as `detail` in `checks.jsonl`. A refused key is `failing` (`ElevenLabs refused the key (401)`), a key not set `missing`. It is free: nothing is metered, and it runs at the cap.
- `connector exec video/elevenlabs -- voices search [--language pl] [--gender female] [--use-case narrative_story] [--accent <a>] [--search "<words>"] [--commercial] [--limit N] [--json]`: `GET <url>/v1/shared-voices`, the library, free (nothing metered, runs at the cap). One line per voice, `voice_id · name · accent · use case · gender age`, then its `preview <url>` to listen to; `--json` prints the list (`voice_id`, `name`, `accent`, `gender`, `age`, `use_case`, `language`, `preview_url`, …). `--commercial` keeps voices with no terms beyond the plan's (no custom rate, no live moderation) and notes that commercial use needs a paid plan. The owner picks one and adds its `voice_id` under `voices:`.
- `connector exec video/elevenlabs -- music (--prompt "…" | --prompt-file <f>) --length-ms N --out <x.mp3> [--instrumental] [--model <id>]`: one `POST <url>/v1/music?output_format=mp3_44100_128` (`prompt`, `music_length_ms` 3000-300000, `force_instrumental`, `model_id`). Before it: the spend room as for `tts`, then the free subscription read; refused on a free plan, and refused when the estimate (ElevenLabs' published 900 credits a minute, rounded up) passes what is left of the cap, so a long track can't overshoot by minutes. The cost is the answer's `character-cost` header, else the subscription's `character_count` after minus before, logged with `"source": "balance"` and the song id as `ref`. The count moves some seconds after the answer, so it is re-read every 2 s for up to 30 s. Music and tts hold the connector's billing lock, one call at a time per project, so the change prices only this call. The call is logged `"unknown": true`, and the next billing call (music or tts) held until `connector spend add … --for <id>`, only when the count can't be read after the call, doesn't move, goes down (a new period), or another entry reached the spend log meanwhile. It writes the MP3, `<x>.licence.json` beside it (`{"source": "ElevenLabs Music", "ref": "song <id>, <tier> plan (paid), commercial use, <date>"}`), and prints the storyboard `music` block and the reel-legal lines to paste ([Reel kit](reels.md#voice-and-music-from-elevenlabs)).
- **The unit.** ElevenLabs bills speech, music and everything else from one monthly credit pool, which its subscription API names `character_count` / `character_limit`, and `character-cost` counts the same credits. So a `meter: elevenlabs` cap in `characters` is that pool: a 20,000 cap covers 20,000 characters of speech, or about 22 minutes of music, or a mix.
- Before a billing call it reads the month's spend. Refused, with nothing run and no secret read, when no cap is set (`connector video/kling spends money and has no cap: …`), when the month is at or over the cap (`connector video/kling is at its cap: 500 / 500 credits spent in 2026-11; nothing billed is run until next month or until the owner raises spend: cap: in projects.yaml …`), or while the last call's cost is unknown. The call that crosses the cap runs (its cost is known only after), so the month can end a little over; two calls started at once both see the same room, so the overshoot is at most the calls in flight.
- **A missing cost.** A `meter: json` call that exits 0 and names no cost may still have billed (a Kling `text_to_video` sometimes answers without `creditsConsumed` while the balance drops). So before each billing call the connector reads the account balance with its free balance subcommand (`spend: balance:`, the key `spend: balance_field:`; a `kling` command defaults to `account` and `availableRemainCredits`, so a kling connector needs neither), and when the output names no cost it reads it again after and records the difference, `"source": "balance"`: `connector video/kling: the output named no creditsConsumed: priced from the balance, 300 → 285 credits: 15 credits (entry s-…)`. Billing calls of one connector take a lock in the project (`<issues>/.connectors/<dept>-<name>.billing.lock`), so they run one at a time and a later one waits (`another billing call is running: waiting for it to finish`); the room under the cap is read again under it. The balance can't tell a call in another project, the vendor's app or a top-up from this call, so keep one account's paid calls in one project, or read the entries (`connector spend`).
- **An unknown cost.** The call is logged `"unknown": true` and the next billing call is held only when the output named no cost and the balance can't price it: it couldn't be read before or after (the stderr line says why), it went up, several different balances were in the answer, an entry reached the ledger during the call, or the connector has no balance read. Then someone records the amount: `issue-cli connector spend add video/kling <amount> [--note "…"]` (the owner, or the department's lead), which prices the held call (`--for <entry-id>` or `--ref <generationId>` names another; `--outside` is spend made outside the engine, which lifts no hold). A second `spend add` for a call already priced, by its id, its `generationId` or by default, is refused naming the first entry, so an owner's and a lead's record don't both count. A call that fails and names no cost records 0 and holds nothing; its exit code passes through.
- **Correcting the ledger.** `issue-cli connector spend [video[/kling]]` lists this month's entries with their ids; `issue-cli connector spend remove <entry-id> --reason "…"` (the owner or the department's lead) appends a removal line: the entry leaves the month's total and lifts its hold, and is listed `removed (<id>: <reason>)`. No line is edited in place.

**The ledger** is `<issues>/.connectors/spend.jsonl`, beside `runs.jsonl` (a private tracker keeps it private), one line per billing call or hand record: `{"id":"s-1a2b3c","connector":"video/kling","date":…,"month":"2026-11","unit":"credits","amount":51,"call":"text_to_video","by":"<issue slug>","exit":0,"ref":"<generationId>"}`, with `"unknown":true`, `"source":"balance"`, `"manual":true` (`"for"`, `"outside"`), `"removes":"<id>"` or a `note`. A line written before ids gets a stable one from a hash of the line. It never holds a prompt, a text or a key. The month is the calendar month in local time, not the vendor's billing cycle.

**Where it shows.** `issue-cli connectors [<dept>]` adds `spend 120 / 500 credits this month (2026-11)` to the connector's line and one line per voice (`voice actor-1: … · an actor's voice, consent missing`); `--json` and `connectors.json` carry `spend` and `voices`. The company page's Connectors block shows a bar against the cap, red at the cap or with none set, and the voices. A connector at its cap, with no cap, or held on an unknown cost is a Needs you row (`Video: kling at its spend cap (500 / 500 credits)`). The worker's briefing line says the connector spends money and is capped.

This is separate from [Spend and budgets](#spend-and-budgets), which estimate the agents' own token cost and never refuse anything. Like the rest of `connector exec`, the cap guards against mistakes, not a determined caller: an agent could run `kling` from PATH itself.

### At launch

When the human starts a department worker (`dispatch`) or the department lead (`lead spawn --department`), the launcher adds the department's `mcp` connectors whose secrets are set:

- claude gets `--mcp-config=<checkout>/.agent-logs/<session>/mcp.json`, written fresh at each launch (mode 0600), and for the `deny_tools:` of its connectors `--disallowedTools=mcp__<name>__<tool>,…`. Both are written with `=`: they take a list, and a value after a space would swallow the prompt that follows;
- codex gets `-c mcp_servers.<name>.command=…` overrides, and `-c mcp_servers.<name>.disabled_tools=["<tool>",…]` for the denied tools.

The launch step names the connectors and how many tools are off: `Marketing connectors: openseo (3 tools off)`. The CLI is the guard: `connector exec` filters a server's calls only on a `url:` server with `needs_approved_output:` or `at:`, where its bridge sees each `tools/call`; a `command:` server's calls pass untouched. Like the rest of this, it guards against mistakes and is not a security boundary.

Each server is `issue-cli --project <slug> connector exec <dept>/<name>`, with the session's `ISSUE_VIEWER_*` and `ISSUE_CLI_LOG` variables, so neither form holds a secret. A `url:` connector is launched the same way, never as Claude Code's own `"type": "http"` entry, so its headers stay out of mcp.json. The launch's steps name the connectors it added. Product's workers, the CEO and other departments get no MCP flags.

### In the briefing

The worker's `## Your department` and the department lead's brief list the connectors:

```
- Connectors: search-console (MCP, loaded in this session; tools off: submit_urls) · wordpress (tool: issue-cli connector exec marketing/wordpress -- <cmd>) — it sends: use it only for an output the owner approved, then issue-cli output publish · quickbooks: secret QB_TOKEN missing on this machine, ask your lead (issue-cli connectors marketing).
```

`issue-cli start` / `show` on a department issue print `Connectors: 3 (1 missing) — issue-cli connectors marketing`.

### Health

`issue-cli connector check [<dept>[/<name>]]` runs each `check:` with its secrets, a 20 s timeout and its output thrown away, so a check that prints a token leaks nothing. It appends one line per connector to `<issues>/.connectors/checks.jsonl`, next to the goals and outputs logs (a private tracker keeps it private too):

```json
{"connector":"marketing/wordpress","date":"2026-10-30T12:00:00Z","result":"failing","exit":1,"ms":840}
```

`result` is `ok`, `failing` (with the exit code, -1 on a timeout), `missing` (a required secret isn't set), `no-check` or `stopped` (a `per_run:` tool no run holds; the check isn't run). A connector with `optional:` secrets also records each one as set or not (`"optional":[{"var":"GSC_QUOTA_KEY","set":false}]`, never a value), and the command prints one line per optional secret under the connector's, `  GSC_QUOTA_KEY optional: not set`. The owner or the department's lead runs it. It exits 1 when one is failing or missing.

### Where it shows

- **`issue-cli connectors [<dept>] [--json]`**: each connector with its kind, `sends`, `needs an approved output (open before: <read tools>)`, `tools by status (at: <statuses>)` (`at` in JSON), a `per_run:` tool's state (`starts per run, stopped` or `running (2 runs)`), the last check and its age, and each secret as `VAR reference set|missing`, an optional one `set (optional)` or `not set (optional)`, a header's as `header Authorization: Bearer keychain:openseo set` (`"header": true` in JSON). With nothing configured it prints the config snippet. `issue-cli department show <key>` lists the same.
- **Company page**: the department's room has `🔌 N connectors · M failing`. Unfolded, it lists each connector's kind, the sends badge, its secrets set or missing, the last check ✓/✗ with its age, and a `per_run:` tool's state. **Check now** runs the department's checks in the background (`POST /p/<project>/connectors/check`, same-origin only), and the room reloads when they are in. `GET /p/<project>/connectors.json[?dept=<key>]` is the same list.
- **Needs you**: a department with a connector whose secret is missing, whose last check failed, or whose last start or stop failed is one row ("Marketing: wordpress check failing"), on the company panel, `/needs-you` and the sidebar count.
- **`issue-cli health lint`**, check `departments`: warns about an unknown `kind:`, a duplicate or badly formed name, an `mcp` connector without `command:` or `url:` (or with both), a `per_run:` block missing a command or with a timeout under 0, `url:` on a tool, a `url:` that isn't https or http on this machine, `secrets:` on a `url:` connector (no child gets them: send them as `headers:`), `headers:` without `url:`, and a secret reference, in `secrets:` or `headers:`, that isn't `env:` or `keychain:` (a bare value "looks like a secret value"; the finding never quotes it). It also warns about `connectors:` on `company:` or the Product entry: connectors belong to a department. For a `deny_tools:` entry that isn't a tool name, appears twice, or sits on a `tool` connector, and for a server the engine knows (`connectors.KnownRisky`, matched by the connector's name, command, args or url; OpenSEO's `submit_indexnow_urls`, `set_report_sharing` and `set_ai_visibility_schedule` for now) whose sending or spending tools are still on while the connector isn't `sends: true`: `connector openseo: tools that send or spend are on: …; add them to deny_tools: […], or mark the connector sends: true`. For `needs_approved_output:` on a `command:` server (it holds the whole server: use `url:` with `read_tools:`, or `deny_tools:`), and `read_tools:` that does nothing or isn't a tool name. For `at:`: a status the department's workflow doesn't have (`connector postiz: at: measure is not a status of Marketing's workflow (…): that entry never applies`), `at:` without `url:`, an entry with neither `allow:` nor `deny_args:`, a name in either that isn't a tool's, a `deny_args:` tool missing from that status's `allow:`, and a `deny_args:` argument path that doesn't parse (`connector social: at: publish: deny_args: integrationSchedulePostTool: "socialPost[.type" is not an argument path (…); until it is fixed the bridge refuses every call of integrationSchedulePostTool`). A department's `subreddits.md` ([Social: Reddit](#social-reddit)): a row without its self-promotion rule, a name not shaped `r/<name>`, a subreddit listed twice, or a table without its header.

### Connecting socials from the factory

The owner connects Marketing's social networks from one page, **Connect socials** (`/p/<project>/socials/<dept>`, linked from the room's Connectors block as *Connect socials ›*), instead of editing Postiz's compose file and starting Postiz by hand (epic D-7: "opcje do podania wymaganych danych … proste i najlepiej zintegrowane z factory"). It is the owner's page: every route, the page itself included, needs the owner's signed-in browser ([Security](API/security.md)). It shows for a department with a `social` connector, or whose workflow has the `social` type.

1. **The tool.** Postiz's address (`http://127.0.0.1:4007`) and the path of its compose file. *Show the projects.yaml change* previews the `social` connector spliced into the department's `connectors:`: the Marketing template's example ([Per run](#per-run)), with the address and file filled in and `start:` as `docker compose -p postiz -f <file> -f <override> up -d`. The key field appears with the diff. **Save** writes the key to Keychain item `postiz`, then projects.yaml (refused when the file changed since the diff), then asks Postiz whether the key works. A connector that exists shows its key as set or missing, with *Replace the key*.
2. **The networks.** One row per network Postiz supports (the table is in `internal/socials/postiz.go`, from Postiz's providers and the spike report o-20261102-a64d): how it connects, the developer app to make and how long its review takes, the **redirect URI** to paste into that app (`<postiz>/integrations/social/<id>`), and fields for the app's values (`X_API_KEY`, `LINKEDIN_CLIENT_ID`, …).
   - *Save to Keychain* writes each value to Keychain item `postiz-env/<VAR>`. The first time, it also adds `<VAR>: keychain:postiz-env/<VAR>` under the connector's `secrets:` and the name under `optional:`; the row shows those lines before you save. A compose override in the state dir (`socials/postiz/compose.override.yaml`) maps each var as `${VAR:-}`, so no file holds a value: the start step gets the values from Keychain, and `up -d` recreates Postiz with them at its next start.
   - *Connect* (an OAuth network) asks Postiz for the network's sign-in address and opens it in a new tab. You sign in at the network, which returns to Postiz; the factory never sees that password or token. Postiz stays up for the sign-in until a check sees the new channel, you press *Done*, or 15 minutes pass.
   - **Bluesky** takes an app password (Bluesky Settings → Privacy and security → App passwords), not your account password: the page passes it once to Postiz's own connect call and keeps it nowhere.
   - A network that accepts only an https redirect (Meta's, TikTok) says so: with Postiz on `http://localhost` its sign-in can't finish. Networks Postiz connects only in its own UI (Telegram, Nostr, Lemmy, your own Mastodon instance) are listed under *More in Postiz's own UI*.
3. **Status.** *Check now* starts Postiz, reads each channel (`connected`, `expired`, `disabled`, `needs a step in the tool` where Postiz asks which page to use) and stops it. The result is one line in `<issues>/.connectors/socials.jsonl`: channel ids, providers and states, never a name, handle or token. An expired network is a Needs you row (*Marketing: sign in again to x*) and a chip on the room. Postiz reports expiry only to its first, superadmin user; otherwise the page says *expiry not reported*.
4. **Disconnect.** Per channel, with a required confirm box: Postiz also deletes that channel's scheduled posts.

Every Postiz call runs inside the connector's `per_run:` lease, so Postiz starts for the action, under `mem-guard check`, and stops after (epic D-5); opening the page never starts it. The values you type go from the browser to the viewer's memory and to `security -i` on its stdin, never into argv, a file, a log or an answer (factory/connect-socials-from-the-factory-an-owner-only-page-to-set-up-postiz-and-connect-each-network-with-the-fields-it-needs-keys-into-the-keychain#D-6, provisional). Keychain is macOS only: elsewhere the page says so and writes nothing. `issue-cli connectors <dept>` prints the page's address under a social connector; agents never connect networks.

### Not here

No vault and no encrypted file: the owner keeps the values. The viewer's one secret entry is [Connect socials](#connecting-socials-from-the-factory), which hands what the owner types to Keychain and never reads it back. The engine runs no OAuth flow and refreshes no token: the social tool does both. Connector-specific code is limited to the socials adapter for the social slot's tool; any other connector is a command or an MCP server the owner names. Nothing is published through a connector by the engine itself. There are no spend caps, and checks don't run on a timer (a recurring job can run `connector check`). HQ and Product have no connectors.

## Partners

A department gets a capability from an open-source program run beside it, instead of the engine building it in-house: a *partner*. The partner is always a separate program the owner installs outside this repo; nothing of it is vendored, copied or imported here. It comes in two shapes:

| | Studio partner | Tool partner |
|---|---|---|
| Who owns the process | The partner: its own stages and approval gates | The department: its workflow's statuses and gates |
| How the department reaches it | The `partner:` block, read through an [adapter](#the-openmontage-adapter); the worker drives it from its checkout | A [connector](#connectors), usually its MCP server (`kind: mcp`) or a command (`kind: tool`) |
| What the engine shows | The issue's stage at the partner and whether it waits for you | The connector's secrets and last check on the room |
| On main | OpenMontage for [Video](#video-studio) | OpenSEO, Marketing's rankings source, Umami, Marketing's and Sales' visits source ([Channels as types](#channels-as-types)), and SendPortal, Sales' email tool ([Sales](#sales-b2c)), each commented out until the owner runs it |

The rest of this section is mostly the studio shape, since that is what the `partner:` block is. Video's first studio partner is OpenMontage, an agent-driven video studio the user installs themselves (AGPL-3.0). The engine only **reads** the partner's files, through an adapter, and shows where each issue is at the partner: its stage, and whether that stage waits for you.

### Swapping a partner

A partner is config, not code in the department. For a studio partner, swapping is a change to the `partner:` block only (`adapter:`, `path:`, `commit:`); the department's workflow file doesn't name it (factory/video-department-openmontage-as-a-video-producer-department-template-workflow-connector-videos-on-the-shelf#D-4). A partner whose files no adapter reads needs one first ([Adding an adapter](#adding-an-adapter)). For a tool partner, swapping is a change to the department's `connectors:` entry; the workflow's prompts name the connector by its `name:`, so a replacement under the same name needs no other edit.

### The licence rule

Before a partner is chosen, its licence is checked, in this order (factory/open-source-partners-integrate-outside-tools-openmontage-openseo-postiz-instead-of-building-them-in-house#D-2, and D-3 of the B2C sales epic):

1. **MIT, Apache-2.0 or BSD first.** OpenSEO is MIT.
2. **GPL-3.0 at worst, and with care.**
3. **AGPL-3.0 only as an exception the owner records per tool**, after a search for a permissive one. Recorded so far: OpenMontage (video epic D-3) and Postiz for social scheduling and publishing (factory/open-source-partners-integrate-outside-tools-openmontage-openseo-postiz-instead-of-building-them-in-house#D-4; no template or connector for it on main yet).

Lint checks the AGPL clause (`issue-cli health lint`, check `departments`): a department's connector whose `name:`, or `partner:` block whose `adapter:` or `name:`, is a tool the catalog knows as AGPL needs `exception: <slug>#D-<n>` on its entry, naming the owner's decision on **this project's board**: in force (not replaced or expired) and with the owner's words (not provisional, a quote). Each project's owner records their own: the catalog's `exception:` is the engine's record, not yours. Without one the warning names the tool, its licence and the rule, and points at the permissive rows (`department partners --slot <slot>`). A tool the catalog doesn't know raises nothing.

```yaml
connectors:
  - name: postiz
    kind: mcp
    url: http://127.0.0.1:5000/mcp
    exception: factory/open-source-partners-integrate-outside-tools-openmontage-openseo-postiz-instead-of-building-them-in-house#D-4
```

Every department lead's brief carries the rule in one sentence (`tracker.PartnerLicenceRule`): "Before a partner is chosen, its licence is checked: MIT, Apache-2.0 or BSD first, GPL-3.0 at worst and with care, AGPL-3.0 only as an exception the owner records per tool, and whatever the licence it is never vendored."

Whatever the licence, a partner is **never vendored**: it runs as a separate program from its own checkout or install, this repo never changes it, and the repo holds only the config that points at it. Your own copy is yours to change, and the licence then applies to you: under the AGPL, publish your changes if you modify it and let others use it over a network. `TestTrackedFilesHoldNoPartnerFile` (internal/partner) guards that for every tool in [the partners catalog](#the-partners-catalog): no tracked path carries one of a row's `marks:` (paths only that tool's own checkout has; required on an AGPL row), `.gitmodules` names no row, and for each checkout in `PARTNER_CHECKOUTS` (`:`-separated) no tracked file is byte for byte one of its files. A department template that ships a partner prints its licence notes with the install steps ([Video studio](#video-studio)).

### The partners catalog

Which tool can fill a department's slot is listed in one public data file, `cmd/issue-cli/templates/partners.yaml`, bundled with issue-cli. One row per tool: its `slot` (analytics, email, seo, social, video, support, payments, cms), `shape` (tool, studio or service), `licence` with the `licence_url` of the LICENSE file read and the date it was read (`licence_read`), `upstream`, what it `needs` on the machine in words, whether it `runs` always (it must stay up to receive visits or sign-ups) or per run, and whether it costs money, in words only: no prices (factory/department-budgets-what-each-department-spends-in-tokens-and-money-shown-in-its-room#D-2). An AGPL row names the decision that records the owner's exception for it (`exception:`), or says `none: <why>`: Listmonk is listed as an email fallback with none, so choosing it needs the exception first ([The licence rule](#the-licence-rule)). A slot has at most one `default:` (the spikes' pick). Postiz is the social slot's default, the owner's pick (open-source partners epic D-5), and Mixpost Lite stays listed as the swap candidate if Postiz's size hurts. Ghost is the cms slot's default, the CMS spike's pick (179 MiB at idle, measured), with Payload, Strapi (MIT outside `ee/`), Decap CMS and WordPress (GPL-2.0-or-later) listed beside it; Marketing's `cms` connector runs it ([Channels as types](#channels-as-types)). The payments slot's one row is the [payments kit](payments.md), with its adapters in the note (the stand-in built in; Stripe, Paddle, Przelewy24 and Tpay planned); Sales' `payments` connectors run it.

```
issue-cli department partners                 # every slot
issue-cli department partners --slot email    # one slot; --json for the rows
```

A template names the slots it fills in `partner_slots:` (Sales: analytics, email, support, payments; Marketing: seo, analytics, social, cms; Video: video), and `department add` ends with a **Partner options** block naming the rows for each (`--json`: `partner_options`, slot → ids), and, under a slot whose default row has a `setup:` (Ghost's: `issue-cli cms up`, [Stand up the CMS](stand-up-the-cms.md)), the one command that stands that tool up on this machine; `department partners` shows it as `stand it up:`. **Listing turns nothing on**: the add writes the same files and entry as without it, and a tool becomes a connector only when you add one ([Connectors](#connectors)). The file ships in the public repo, so it holds public facts only. Licences change: read a row's LICENSE again before relying on it, and update `licence_read` when you do. `TestPartnersCatalogRowsComplete`, `TestPartnersCatalogHoldsNoPrice` (internal/partner) and `TestTemplatePartnerSlotsKnown` (internal/depttemplate) keep the rows complete.

### Running partners on this machine

- **Keys are Keychain references.** A partner's API keys go in `secrets:` or `headers:` as `keychain:<service>[/<account>]` (or `env:<VAR>`), never as values ([Secrets](#secrets)). Keys a zero-key path doesn't need are `optional:` (Video's `FAL_KEY` and `ELEVENLABS_API_KEY`); a key the partner reads from its own `.env` stays in that file, outside this repo.
- **Watch memory.** Heavy tools: where mem-guard is installed, run `mem-guard check` before anything that loads a model, starts a browser or a big server, or fetches many pages, and don't start while it says BLOCKED. One local model and one browser at a time; plain fetches first. Every template that names mem-guard quotes this line from `templates/departments/_shared/heavy-tools.md` word for word (video epic D-1, `TestSharedTextsQuotedWordForWord`). A partner that costs a lot of memory is started only for the runs that need it (Postiz, about 2.4 GB, only for publishing runs: the D-4 above). The engine starts a tool only for a run, and only one whose owner wrote its `per_run:` block ([Per run](#per-run)).
- **Sending goes through the owner.** A partner connector that publishes or spends is `sends: true`, used only for an output the owner approved; a studio partner's gates are answered by the owner ([The owner's answer](#the-owners-answer)).

```yaml
departments:
  - name: Video
    systems: [Video]
    workflow: departments/video/workflow.yaml
    partner:
      name: OpenMontage        # shown on the pages (the adapter's name when unset)
      adapter: openmontage     # which reader understands its files
      path: ~/Repo/openmontage # your own checkout; relative paths are from the project root
      commit: 195eab2          # the pinned SHA; lint compares it with the checkout's HEAD
      live_port: 4750          # optional: its local viewer, linked only while it answers
```

**One issue is one partner project.** The project id is the issue's `partner_project:` field (`issue-cli set-meta <slug> partner_project <id>`), else the last part of its slug. It must be one plain folder name; anything else is refused before a file is read.

### The OpenMontage adapter

It reads, as JSON or YAML only, with each file capped at 1 MB:

- `projects/<id>/project.json` (`version`, `pipeline_type`);
- the stage order from the checkout's own `pipeline_defs/<pipeline_type>.yaml` (never hard-coded: it changes upstream);
- each `projects/<id>/checkpoint_<stage>.json`: `status` (`in_progress`, `awaiting_human`, `completed`, `failed`), `review.summary`, `artifacts`, `cost_snapshot`, `error`.

The current stage is the first stage, in the pipeline's order, without a `completed` checkpoint. `awaiting_human` there is "awaiting you". It knows version `1.0` of `project.json` and of the checkpoints; any other version, or a status it doesn't know, shows **partner format not understood** with the file and version, and no stage: it never guesses. Its test fixtures (`internal/partner/testdata/openmontage/`) are written for this repo, not copied.

### Where it shows

- **Station**: a Partner block in the side column: `OpenMontage: pitch · awaiting you`, the stage's summary, `$0.40 spent of $2.00` (spent, against spent + reserved + remaining), its artifacts, and a link to the partner's live view while `127.0.0.1:<live_port>/api/health` answers (asked at most every 30 s, 300 ms timeout; the viewer never starts it). With no folder yet it names the id it looks for.
- **Needs you**: a stage awaiting you is one row under To approve (`OpenMontage: pitch`), with the summary and spend; it opens the Station. When the issue also has a ready gate, the partner's words go on that gate row instead: one decision, one row ([One waiting-on-you count](#settled-rules)).
- **Room**: a foot item `OpenMontage · 1 awaiting you · 2 working` over the department's open issues; `path missing` or `adapter unknown` in red when the block is broken.
- **issue-cli**: `show` and `start` print `Partner: OpenMontage: pitch · awaiting you (project <id> at <path>)`; `department show <key>` prints the block (adapter, path, pin).
- **`issue-cli health lint`**, check `departments`: no or an unknown adapter, a path that isn't a folder, and a checkout whose HEAD isn't the `commit:` pin.

### Adding an adapter

An adapter is Go in `internal/partner`: a type with `Status(cfg tracker.Partner, dir, project string) (partner.Status, error)`, registered in `init` with `partner.Register("<name>", …)`. To take part in the gate bridge it also implements `partner.Approver`: `Approvals` (each stage's gated and approved flags) and `ApprovalHow` (how the worker writes the partner's approval, quoting the D-n). It reads files only (`partner.ReadFile` caps the size; `TestPartnerAdapterOnlyReads` refuses `os/exec`, `net/http` and writes in the package), returns `StateNone` with no error for a project that isn't there, and wraps `partner.ErrFormat` for a format it doesn't know.

### The owner's answer

A partner's gate is answered in Dark Factory, and the partner's own approval follows from that answer, never the other way round.

1. **The worker stops at the gate.** At a gated stage the worker writes the partner's checkpoint as awaiting the owner (OpenMontage: `status: awaiting_human`) and stops.
2. **The owner answers.** The Needs you row (`OpenMontage: pitch`) and the Station's Partner block have **Approve** and **Send back**; your words are optional for Approve and needed for Send back. Only your browser can answer (`POST /p/<project>/issue/<slug>/partner`, an owner route); an agent gets 403 and the way to ask on its trail. The answer must be for the stage that waits now: if the partner moved on since the page loaded, it is refused (409, reload).
3. **It becomes a D-n.** `- **D-7** · 2026-11-02 · owner on Needs you: OpenMontage pitch approved, "go with concept B" (ref partner-approved:pitch@20261101T102000Z)`, logged as the owner's browser in `.decisions/log.jsonl`. The ref names the stage and the time of the checkpoint it answers, so a stage asked again after a send-back waits on you again. A send-back carries `partner-sent-back:<stage>@…` and does not move the issue's status: revising is the partner's own loop.
4. **The worker is told.** Its live session gets one line: the decision to quote and, in the adapter's words, how to write the partner's approval (OpenMontage: re-write `checkpoint_<stage>.json` with `human_approved=True` and append a `decision_log` entry quoting the D-n). With no worker live, `issue-cli start` and `show` say the same: `Partner: OpenMontage: pitch · approved by you` and the next step.
5. **Once answered, it no longer waits on you.** The row leaves Needs you and the Station says `You approved pitch (D-7): the worker writes it at OpenMontage`.

Approving an engine gate whose row carries the partner (one decision, one row) answers the partner too: the same click records the partner's D-n.

**Who else may record it.** `issue-cli decide --ref partner-approved:<stage>` (or `partner-sent-back:`) is the owner's act: a worker is refused, a lead relays the owner's own words with `--quote` (factory/department-outputs-shelf#D-2).

**The check.** An adapter that can list approvals (`partner.Approver`) lets the engine pair each gated stage the partner marks approved with a `partner-approved:<stage>` D-n on the issue. One without is flagged as `OpenMontage: pitch approved at the partner with no owner decision (partner-approved:pitch)`: by `issue-cli health lint` (check `departments`, on the issue), on the Station in red, and with a ⚠ in `issue-cli show`. The worker briefing's `## Your department` has a `- Partner:` bullet with the rule.

### Not here

A department workdir and video on the shelf are their own issues; the [Video studio](#video-studio) template installs the block for you. The engine never starts the partner, its viewer or its tools, and never writes in its checkout: the worker writes the partner's approval.

## Connections

What passes between HQ, Product and the departments, derived from what is on disk (`internal/connections`), and, when `projects.yaml` declares them, what *should* pass ([Declared connections](#declared-connections-and-routing)). Messages between sessions are never a source: they are not on disk.

| Kind | From → To | Comes from |
|---|---|---|
| **request** | the asker → the issue's department | `requested_by: <department or hq>` on the issue, or the issue citing a decision on another department's issue (`<slug>#D-n`). Drawn **via HQ** when the issue's `approval:` or `dispatch:` comment was written or recorded by the CEO (`lead-<project>`), **via the group** when the asker's group lead (`lead-<project>-<group>`) recorded it |
| **output** | the maker → the user | a shelf output whose issue another department's issue depends on, is blocked on or cites; or `issue-cli output use <id> --by <department or issue>` (a `use` event in `.outputs/outputs.jsonl`). A rejected output feeds nobody |
| **dependency** | the waited-on → the waiting | an issue's `depends_on` or `blocked_on` naming an open issue in another department |

A link inside one department is no connection. Each line counts its **open** items (the issue is open; a recorded use from the last 30 days); items closed in the last 30 days (`done_at`) are listed under them. A dependency on a done or missing issue is not drawn (the card marker says to remove it).

**Recording a request.** `issue-cli create` writes `requested_by:` when a department lead, or a worker on another department's issue, files an issue that lands in a different department; `--requested-by <department|hq>` sets it by hand, and `set-meta <slug> --key requested_by --value <department>` checks the name. The CEO's own issues get none. Engine work a department needs goes Marketing → CEO → Product: the department lead files it (`requested_by:` filled), the CEO approves it, and the line runs through HQ.

**Where it shows.** The company page lists every line in a Connections block under the floor, and `static/connections.js` draws them as **roads**: one road per pair of rooms that pass work, whatever kinds it carries, routed like streets through the gaps between the rooms. The open traffic moves along it as parcels in the sender's colour: ● request, ▲ handoff, ■ output, ◆ dependency. With reduced motion the parcels stand still and each road shows its counts instead. Hovering or focusing a road names it under the floor, and clicking it opens the drill-in. Room feet show `⇄ in N · out M`; the map's lot shows `⇄ N`. `/p/<project>/connections?from=&to=&kind=` lists each line's issues with the reason; `connections.json` gives the same edges. `issue-cli connections [--from] [--to] [--kind] [--json]` prints them, and `department show <name>` counts them. A project without departments has none, and says so. Hovering, focusing or tapping a room lights its roads (a group's also lights the roads of the rooms inside it), and a row of the Connections block lights its road ([UI](UI/overview.md)).

### Declared connections and routing

`projects.yaml` can declare, at project level next to `departments:`, who may ask whom and whose outputs feed whom. Box keys as everywhere ([Settled rules](#settled-rules), Box keys); both lists are optional.

```yaml
connections:
  requests:                                   # who may ask whom
    - {from: marketing, to: product, via: hq}  # engine work through the CEO (D-8)
    - {from: sales, to: marketing, via: gtm}   # inside the group, through its lead
    - {from: marketing, to: sales}
  feeds:                                      # whose outputs another uses
    - {from: marketing, to: sales, what: "brand and ICP"}
```

- **Routing.** Once `requests:` has an entry, a department lead or worker whose request (`issue-cli create`, `requested_by:` filled or `--requested-by`; `set-meta --key requested_by`) is on no declared route is refused, and the refusal lists the routes the asker has (`Sales may ask: Marketing via GTM`) and the entry the owner would add. Requests from HQ are never refused (the CEO routes), nor is the CEO or the owner's shell filing one for a department. Without `requests:` nothing is refused. Source: factory/declared-connections-who-may-ask-whom-and-what-feeds-what-with-routing-rules#D-1, #D-2.
- **Via.** `via: hq` or `via: <group>` names who forwards the request. The department still files it (factory/marketing-brand-foundation-…#D-8), and `create` says who forwards it; a department lead other than the forwarder's can't `dispatch` it until the forwarder's lead has dispatched it or recorded its `approval:`. The refusal names that lead. Department leads can't dispatch Product's issues anyway, so `via: hq` into Product is the CEO's by scope. Source: #D-3.
- **Feeds** are drawn, not enforced: `issue-cli output use` on an undeclared pair records the use and says the line is marked not declared. Dependencies are never declared. Source: #D-4.
- **Marks.** Each request and output line of a kind the project declares is marked: *declared*, *not declared* (traffic off the declared routes: a red `!` sign on its road), or, for a declared one nothing passes on, *declared, no traffic* (a thin dotted road with no parcels). The Connections block, the drill-in page (`data-route`, the legend), `connections.json` and `issue-cli connections [--json]` (`route:`, `declared_via:`, `what:`) all carry them.
- **Briefing.** A department lead's `## Your department` lists its routes ("Your routes … you may ask product via hq; sales"), or says it has none.
- **Lint** (`departments` check): an end or `via:` naming no box, a route from a box to itself or via one of its own ends, `via:` on something other than hq or a group, `via: <group>` for an asker not in that group, `via:` on a request from HQ, `via:` on a feed, a feed from HQ, a duplicate, and a department's request straight to Product without `via: hq` (D-8). Lint warns; an entry whose boxes don't resolve is skipped.
- **No catalog entry.** The feature catalog covers `workflow.yaml`; `projects.yaml` department keys (groups, goals, connectors, jobs, connections) are documented here and checked by lint. Source: #D-5.

**Not yet:** lines between projects on the map; wildcards and multi-hop `via:`; editing routes from the page.

### Handoffs

One box asks another for work as an issue: `issue-cli handoff <to> --title "…" --why "…" [--for <your issue>] [--system S] [--type T]`. It is `create` with three things recorded: `requested_by:` (the asker's box), `handoff_route:` (the route taken, box keys, e.g. `marketing → hq → product`) and `handoff_for:` (the asker's issue it serves; a worker's own issue by default). The issue starts at the receiving workflow's first status with a `## Handoff` section (from, route, for, why), and the asker's issue gets a `handoff: asked …` comment. There is no store besides the issue: the state comes from its status and its `handoff:` comments. Source: factory/department-handoffs-one-department-asks-another-for-work-as-an-issue#D-1.

- **Who asks.** A department lead hands off from its department, a worker from its issue's box. The CEO and the owner's shell hand off from HQ, or from `--from <box>`. The target is a box of this project (`department list`); anything else is refused (never across projects), and so is the asker's own box.
- **Route.** The declared routes decide ([Declared connections](#declared-connections-and-routing)): off them it is refused with the routes the asker has. Without `requests:`, a department's handoff to Product goes via HQ (Settled rule "Engine requests go via the CEO"), and anything else goes straight. #D-3.
- **Forward or decline.** A handoff on a via route waits for the relay lead: the CEO for `via: hq`, the group's lead for `via: <group>`. Until then a department lead can't `dispatch` it. The relay lead (or the owner's shell) runs `issue-cli handoff forward <slug> [--note …]`, which writes a `handoff: forwarded by …` comment and nothing else, or `issue-cli handoff decline <slug> --reason "…"`, which parks it through the park transition with the reason and tells the asker. A forward signed by anyone else doesn't count. #D-2.
- **Never past a gate.** The issue starts before backlog (create's rule), and forward only comments. No handoff command transitions past a gate or records an approval, so no handoff passes an `owner_only` gate.
- **The asker is told.** When a handoff reaches its workflow's last status (`transition`), or is declined, parked or closed, the asker's `handoff_for` issue gets `handoff: <slug> (asked of <box>) is done` (or declined: <reason>, parked), and the output prints the SendMessage call to the asker's lead. It happens once per handoff. Nothing is pasted into a session (issue-cli never pastes into a Claude session, [Agent Dispatch](agent-dispatch.md)). #D-4.
- **Lists.** `issue-cli handoffs [--from <box>] [--to <box>] [--waiting] [--json]`: open handoffs and the ones closed in the last 30 days, with route and state (*waiting on HQ to forward*, *at <status>*, *done*, *declined: …*, *parked*). `--waiting` is the CEO's or a group lead's list of forwards to do (`--waiting-on <box>` for another relay). These wait on the CEO, not the owner, so they add no rows to Needs you (Settled rule "One waiting-on-you count"). #D-5.
- **Where it shows.** Each room (departments, groups, Product, HQ) gets a **Handoffs** block when it has any: *Asked* (what the room asked others for), *Inbox* (what others asked of it) and, on HQ and a group, *To forward*. Up to 3 rows each, with a state chip, and *+N more* opens the drill-in. A handoff travels its route's roads (Marketing to HQ, HQ to Product) as a ▲ parcel even before the forward. The line's row says *N waiting to be forwarded*. The drill-in lists each item's *handoff · <state>* and route, and `connections.json` and `issue-cli connections` carry `state` and `route`. #D-6.
- **Briefings.** A department lead's `## Your department` names `issue-cli handoff` and `handoffs --from`. A group lead's names `handoffs --waiting` and forward/decline, and so does the CEO's when the project has departments. `create` with `requested_by:` filled prints a tip for `handoff`, and the dispatch refusal on a pending relay names `handoff forward`.
- **No catalog entry** for the handoff itself: no `workflow.yaml` key (#D-7). The gate below has one.

#### The handoff gate

A workflow can make a transition wait for another department's answer: the validate rule `handoff_answered: <department key>`, with optional `paths:` (globs) on the action. Legal is its first user: a product names it before shipping so a branch touching data flows, the texts a customer sees or a dependency manifest (a new third party) waits for Legal's answer.

```yaml
- from: "testing"
  to: "human-testing"
  actions:
    - type: "validate"
      rule: "handoff_answered: legal"
      paths: ["server/**", "src/copy/**", "package.json", "go.mod"]
```

- **Answered** means a handoff to that department (its `handoff_route:` ends at it) with `handoff_for: <this issue>`, at its workflow's `done`. An open one is named in the hold ("Waiting on: <slug> (at <status>)"); a declined or parked one is "Not an answer". With none, the hold prints `issue-cli handoff <department> --for <slug> --title "..." --why "..."`.
- **Paths.** With `paths:` the rule holds only when the work branch's own changes (the three-dot diff `decisions_respected` reads, from the dispatch's recorded base) match one, and names the files. A branch that matches none passes. One it can't read (no recorded base, no branch) holds, saying so. Without `paths:` every issue on that edge waits.
- **YOLO doesn't pass it.** The answer is the department's done issue; Legal's done needs the owner's approval of the answer on the shelf.
- **Lint.** The `departments` check warns on a `handoff_answered` naming no department of the project: no issue could answer it.
- Catalog entry `handoff-gate` (`propose: false`: a project adds it once it has the department). Generic: any department can be asked this way (factory/factory-departments-floor-map-and-capabilities#D-9).

## Spend and budgets

What each box spent: tokens and an estimated cost in USD, by week (from Monday, local time), for the last 4 weeks. Read from Claude Code's transcripts on this machine; numbers only, nothing leaves the machine and no transcript text is copied anywhere. Source: factory/department-budgets-what-each-department-spends-in-tokens-and-money-shown-in-its-room (D-1 to D-4).

### Where the numbers come from

`internal/spend` reads `~/.claude/projects/<cwd slug>/*.jsonl` (and each session's `subagents/`; `$CLAUDE_CONFIG_DIR/projects` when set) with the benchmark's transcript reader (`bench.EachMessage`). Each assistant message counts once by its id, on the day of its `timestamp`. A session counts for:

| Where it ran | Counts for |
|---|---|
| an issue's worktree (`<root>/.worktrees/<slug>`) | the issue's department (`DepartmentOf`; a job run is an issue, so it counts for its department) |
| the project folder, named `lead-<project>` | HQ (the CEO) |
| the project folder, named `lead-<project>-<dept>` | that department (a group's lead: the group) |
| the project folder, named `agent-<slug>` (a worker without a worktree) | the issue's department |
| the project folder, no name (your own sessions) | **unattributed**: listed by `issue-cli spend`, never in a box |
| the project folder, another project's lead or worker | skipped |

A **group** shows its own (its lead's) plus its departments'; **HQ** shows everything attributed. Only Claude Code writes these transcripts, so the boxes count Claude Code only.

**By subscription.** The report also splits the project's spend by agent CLI: `claude` is every box plus your own sessions, and `codex` comes from Codex's session logs (`$CODEX_HOME/sessions/**/rollout-*.jsonl`, else `~/.codex/sessions`). Each session counts when its `session_meta` cwd is the project checkout or one of its worktrees, from its `token_count` events' `last_token_usage` (input without the cached part, cached input as cache read, output). Codex tokens are unpriced, since a subscription is a flat fee. Nothing else in Codex's folder is opened. `issue-cli spend` prints `By subscription (this week · last 4 weeks)`, and HQ's Spend block shows `By subscription: claude 1.2M tokens · codex 300k tokens this week` once both have spent something (factory/route-work-between-claude-and-codex-subscriptions-ccr-pick-the-agent-per-task-fall-back-when-one-hits-its-limit#D-4). Files last changed before the window are skipped and each file's numbers are kept in memory until it changes, so the polled company page doesn't re-read them.

### Context per turn (`spend --context`)

Nearly all the input tokens are cache reads: every turn re-reads the whole context (the prompt of one API call: input + cache read + cache write; output is not context), so a session costs its context × turns. `issue-cli spend --context [--days N] [<box>] [--json]` shows where that comes from, over the last N calendar days (default 7), with the boxes attributed and rolled up as above:

- **sessions, turns**: a session is one main transcript (`<id>.jsonl`); its subagents have their own, smaller context and are not read. Each message counts once by its id across the folder.
- **floor**: the first turn's context, before any work: CLAUDE.md, the briefing, tool definitions and skills. A box shows the median over its sessions. A **resumed** session (its first message is an earlier file's) has no floor of its own, and neither has one whose first turn is before the window.
- **floor by part**: under `Workers:` and `Leads:`, under each top session and in one box's view: the system prompt, the built-in tools (the three biggest named), MCP (MCP tool definitions and server instructions; a deferred tool is only a name, in other), the CLAUDE.md files by kind (global: `~/CLAUDE.md` or `~/.claude/CLAUDE.md`; project; memory: the auto-memory `MEMORY.md`), the skill list, the brief (the first user message: a lead's or worker's brief, or what you typed) and other (agent types, deferred tool names, hook output, session context, date). Read from the first request Claude Code writes into the transcript before the first answer (`api-request-shape`: system and tools; `api-request-blob`: the messages as sent; the `instructions`, `skill_listing` and `mcp_instructions_delta` attachments), each part in characters scaled to the floor's tokens by the session's own ratio (about 2.7 characters a token), so a part is an estimate, about ±10%. A box's figure is each part's median over its sessions with a split, so the parts need not add up to the median floor. A transcript without a request shape (an older Claude Code) has a floor but no split. `--json` gives `floor_parts` (with `tool_sizes`, `files` and `chars_per_token`) per session and per stats block. Lengths, tool names and kinds of file only: no text, no path.
- **turn median, max**: over every turn of the box's sessions.
- **compactions**: `compact_boundary` records in the window.
- **floor share**: Σ floor × turns over the context those sessions read: how much of what they read the floor alone accounts for.

Without a box it prints all sessions, the workers (sessions in an issue's worktree), the leads (the CEO's and each department and group lead's sessions: their floor before and after a change to the [lead setup](agent-dispatch.md#lead-setup) or the lead brief), each box, your own sessions, and the top 5 sessions by context read; with a box, that box and its top 5. Claude Code only: Codex's logs are not read here. Numbers only, as for spend. The Stats page's **Context floor** trend is the worker sessions' floor per day ([the context family](workbench-files.md#the-context-family)).

### By kind of session (`spend --kinds`)

`issue-cli spend --kinds [--all-projects] [--days N | --since <T> [--until <T>]] [--prices <file>] [--json]` splits the tokens by project and kind of session over a window: the last N calendar days (default 7), or `--since`/`--until` (a date, which counts the whole day for `--until`, or an RFC 3339 time). `--all-projects` (which implies `--kinds`) reads every project in projects.yaml, and the transcript folders that belong to none as project `other`. Per row: sessions, turns, tokens read (input + cache read + cache write), output, est. USD, share of the window's USD (of its tokens when nothing is priced) and `% WEEK`. A second table splits the workers by type and stage. Codex is not counted here (plain `spend` shows it per project).

A session's kind comes from its agent name first, then its folder:

| Session | Kind |
|---|---|
| `lead-<project>`, a department lead's name, any other `lead-*` | lead |
| `agent-<slug>` (or an unnamed session in the issue's worktree) | worker: the issue's type, and its stage, the issue's status when each turn ran (from the stats sidecar's transitions) |
| a worker whose issue is a job's run (`job:` field) | `flush` (lead-flush), `sweep` (a job whose id names a sweep, the queue-low sweep included) or `job <id>` |
| `postmortem-*` | post-mortem |
| `critic-*` (the [fresh critic](Workflow/critique.md), named `critic-<issue>`) | critic |
| `bench-*` (a [benchmark](benchmark.md) worker, named `bench-<task>`) | benchmark |
| `review-*` | review (the health review) |
| a subagent's transcript (`<id>/subagents/*.jsonl`) | subagent |
| unnamed, in the project folder | the owner's own |
| any other name, or an unnamed session in another folder under the project | other |

A benchmark task whose worker ran on a sandbox HOME (the default `worker_home:`) wrote its transcript outside `~/.claude/projects`, so its run record's usage counts instead; a run on the owner's HOME is read from its transcripts only, so no task counts twice. Each message id counts once per folder.

### Turns by cause (`spend --turns`)

`issue-cli spend --turns [<slug>] [--days N | --since/--until] [--json]` counts each issue's worker turns (assistant messages in its worktree's sessions and in sessions named for its worker; critics and post-mortems left out) by what caused them, the first that fits:

- **transition**: a shell call running `issue-cli … transition`. **Refused** counts those whose result was an error or printed `failed to transition` / `Error:` (a pipe hides the exit code).
- **validate**: `make validate`, or reading its log (`tail … validate….log`).
- **read**: one `Read` call, or one shell read of exactly one file (`cat`, `head`, `tail`, `nl`, `sed -n`, `grep` or `rg` on one named file; after `cd <dir> &&` and through `| head`, `| cut` and such). Two files in one call are not a single-file read.
- **red round**: any other turn while a check is red: from a red result in the issue's recorded rounds (gate, local CI, critic) to that check's next green, or from a `validate: FAIL` / `validate: red so far` the session saw to its next `validate: PASS`.
- **rest**: everything else.

Each cause also shows the context its turns read (every turn re-reads the whole context). `RED ROUNDS` counts the rounds that started in the window. Two counts name turns a batch would have saved: **dry runs** (`DRY RUNS`, `dry_runs`), transition calls with `--dry-run`, whose check the real call makes too; and **follow-on reads** (`FOLLOW-ON READS`, `follow_on_reads`), read turns straight after another read turn of the same session, a one-file read that could have gone with the one before. A command's or a result's text is read only to pick the cause and is never kept, printed or written.

### Prices: the estimate

Cost is tokens × price per million tokens, per message by its model. Prices come from `$XDG_CONFIG_HOME/ai-workflow-engine/prices.yaml` (else `~/.config/…`), the benchmark's `prices:` shape; the owner's [benchmark files](benchmark.md) fill models it doesn't name. A key prices every model it is a prefix of (`claude-opus-5` prices `claude-opus-5-5`). A [bench run](benchmark.md) reads the same prices the same way, so one model has one cost in both.

```yaml
prices:                 # USD per million tokens
  claude-opus-5-5: {in: 0, out: 0, cache_read: 0, cache_write: 0}
```

The engine bundles no price list (D-2): a model without a price shows `cost unknown`, a box with some unpriced tokens `≥ $x est.`, and `issue-cli spend` names the models and the file to add them to. Every cost is labelled `est.`.

Each entry may say `confirmed: true` once the owner has checked it against the provider's list; `spend --kinds` shows every price as `confirmed` or `assumed`, with the file it came from. `weekly_limit: {usd: N}` or `{tokens: N}` is what a full week of the plan allows: `spend --kinds` then shows each row's `% WEEK`, its share of the limit pro rata for the window (a 3-day window is held to 3/7 of it); without it the column shows `—` and the output names the line to add. `--prices <file>` reads another file of the same shape, for a measurement on assumed prices without touching the owner's.

```yaml
prices:
  claude-opus-5-5: {in: 5, out: 25, cache_read: 0.5, cache_write: 6.25, confirmed: true}
weekly_limit: {usd: 1000}      # or {tokens: 5000000000}
```

### budget:

On a department, a group, the Product entry or `company:` (HQ, the whole project):

```yaml
departments:
  - name: Marketing
    budget: {usd: 50, period: week}          # or {tokens: 20000000, period: month}
```

`period:` is `week` (the default) or `month`. The budget shows next to the spend as "x of y" (`$12.40 of $50.00 this week (est.)`, `3.1M of 20M tokens this month`), red when over. **It never blocks anything**: nothing refuses, pauses or waits on it, and it adds no row to Needs you (D-3, N-18). A `budget:` that doesn't parse never stops projects.yaml from loading: the room shows "budget unreadable" and lint's `departments` check names the mistake with the right shape.

### Where it shows

- **Company page**: each room (HQ, Product, a department, a group) has a **Spend** block (`wb/room-spend`): a 4-week mini chart, this week's tokens and cost, the budget, "incl. N departments" on a group or HQ. Open it for the weeks and the top 5 issues (by tokens over the 4 weeks, linked). A box that spent nothing says "No spend recorded yet" with the command. The tooltip splits this week's tokens (input, output, cache read, cache write).
- **`issue-cli spend [<box>] [--json]`**: every box's weeks, budget and top issues, then the unattributed sessions and, when a model has no price, where to add it.
- **`issue-cli department show <name>`**: a `Spend:` line (this week, the 4 weeks, the budget); `--json` carries `spend`.
- **No catalog entry**: `budget:` is a projects.yaml key, like connections (D-4).

## Department health

The daily health snapshot carries a `departments` family: per department, goals moving (progress over 7 days), outputs reviewed in time (`health.limits.outputs_review_days`, default 7) and waiting, jobs on time and periods missed, and open handoffs with the oldest one's age. Their mean is the department's *on track* share. It shows on the Health view (`/health#departments`), as `♥ N% on track` on each room's foot, on the Report's Health card and as a Stats trend. The `departments` score component counts it only in a project with departments. It counts waiting outputs and due jobs for the trend and adds no rows to Needs you (Settled rule "One waiting-on-you count"). Metric names and limits: [Workbench Files](workbench-files.md#the-departments-family). Source: factory/department-health-family-goals-moving-outputs-reviewed-jobs-on-time.

## The HQ report

An executive summary of the company in HQ: `/p/<project>/hq/report`, linked as **Report →** on HQ's room foot, and the same words as text at `/p/<project>/hq/report.md` (`issue-cli status --hq`). Source: factory/hq-report-an-executive-summary-of-each-department-in-the-ceos-hq (D-1 to D-7).

- **One line per box, folded.** HQ, Product, then each group and department in the company tree's order (a group's departments indented under it). Each section is a fold whose summary is its headline: the on-track dot and `♥ N% on track`, goals moved, outputs shipped and to review, jobs due and periods missed, what is blocked and on whom, handoffs in and out, this week's spend with the budget. Closed, the page is the whole company in a minute; **Expand all** opens every section.
- **The detail** is one labelled row per kind: Health, Goals (each with its move this week), Outputs (linked to the shelf), Jobs (last run, due, missed), Blocked (each issue and on whom), Handoffs, Spend, Week report (a group's latest, or the `--write` command), Lead (the department lead's newest `progress:` line, at any age, with the issue) and Open work. HQ shows its mission, goals with their roll-up, forwards waiting on the CEO and the shelf; Product its open and done work, goals, handoffs in and spend, and links to its Report (`/status`).
- **Derived, not written.** `buildHQReport` (`hq_report.go`) reads the company page's model, `weekplan.Sections` (every department's week section: goals and moves, the shelf, jobs, handoffs, blocked; the logs read once), the newest health snapshot (nothing measured live: no snapshot says so and names `issue-cli health snapshot`; a department the snapshot has no measures for says "not measured"), spend and the leads' comments. No agent writes it, no model is called. The text twin renders the same model.
- **Honest empty states.** A department with nothing says "Nothing here yet" and names `issue-cli department show <key>`; a project without departments shows HQ and Product and names `issue-cli department list`.
- **No new waiting count.** Blockers on the owner are read from the week sections; the report adds no "waiting on you" number (Settled rule "One waiting-on-you count").
- **Who reads it.** The CEO's brief names `issue-cli status --hq` when the project has departments, and `status.md` has a `Departments:` line pointing at it.

## Turning on a department: the Marketing example

The owner's steps to run a real department, in order, with Marketing's weekly SEO pass as the example. Each command is one the pilot's guided check ran on a throwaway project (`scripts/checks/marketing-pilot.sh` on the branch `work/factory/marketing-pilot-seo-as-the-first-department`; check scripts never reach main), so the steps are known to work together. Names in angle brackets are yours; client names, URLs and Keychain service names belong in your project, never in the engine's repo.

1. **Register the project** in `~/.config/ai-workflow-engine/projects.yaml` (`name`, `issues:`, `workdir:`). If its work files hold client data, give it a private tracker first ([Private work and code](workflow.md#private-work-and-code)).
2. **Install the department**: `issue-cli --project <project> department add marketing --dry-run`, then without `--dry-run`. It writes `workflows/marketing.yaml`, `departments/marketing/role.md` and `departments/marketing/jobs/weekly-seo-pass.md`, and adds the entry with its goals and the job. Commit the files it names (or add with `--commit`). SEO is the first [channel](#channels-as-types) (`seo-pass`); the others come later as types.
3. **Settle the brand and the ICP**: `issue-cli create --department marketing --type foundation --title "Brand foundation: <topic>"`, then approve its `publish` once the brand and ICP read right ([Brand foundation and research mode](#brand-foundation-and-research-mode)). Channel briefs can't move to draft before that. Optionally, settle the keywords and the positioning line next (`--type keyword-research`, [Keyword research for positioning](#keyword-research-for-positioning)), so the first briefs target words you chose.
4. **Say what the department should know.** In `departments/marketing/role.md`: your site(s), the tone of voice, what to check before a draft goes to review. In the job brief: what a pass covers per site.
5. **Give it a business goal to count towards** (optional): an HQ goal under `company: goals:`, and `rolls_up: <hq goal id>` on the department goal that feeds it (`marketing/pages`).
6. **Add the connectors.** The rankings source and the visits source are already in Marketing's entry, commented out (`openseo`: start OpenSEO on this machine; `umami`: run Umami where you choose and create the `umami-api-key` Keychain item; then uncomment them). `issue-cli connectors marketing` prints the config for the others. A read-only one first (Search Console as an `mcp` server) is the safer start; a publishing one (WordPress as a `tool`) gets `sends: true`. Each secret is a reference: `env:<VAR>` or `keychain:<service>`. Create the value yourself, for example `security add-generic-password -s <service> -a "$USER" -w`.
7. **Check them**: `issue-cli connector check marketing`. Both should be ok; `issue-cli connectors marketing` shows each secret set.
8. **Start the department's lead**: `issue-cli lead spawn --department marketing`, or ▶ Start lead in the room on the company page.
9. **Run the first pass yourself**: ▶ Start run on the Due jobs row in Needs you, or tell the lead to run `issue-cli job run marketing/weekly-seo-pass --quote "<your words>"`. The run is an `seo-pass` issue in `brief`; its worker puts each page and draft on the shelf.
10. **Review on the shelf** (`/p/<project>/shelf`): approve or reject each output. Only an approved one goes out, then `issue-cli output publish <id> --url <where it went>`.
11. **Record the numbers**: after publish the pass moves to `measure`, where the worker runs `issue-cli goal set marketing/pages <n>` and, with umami on, `issue-cli goal write marketing/weekly-seo-pass visits=<n>`; `issue-cli goals` shows the department and HQ.
12. **Let it run weekly** once a few passes went well: `issue-cli job auto on marketing/weekly-seo-pass --quote "<your words>"` turns on the standing start, inside auto-start's memory and worker budget. `job auto off` turns it off.

Anything a department needs that these pieces don't give is a gap in the shared pieces, not code for that department: fix it in the engine for every department.

## Not yet

- Groups inside groups, and a department in two groups.
- Connector sources, goal trends over time, and setting goals from the page.
- Publishing an approved output through a connector automatically, and editing or removing outputs from the page. Templates carry connectors only as commented examples (`connector_examples:`) or, for a partner, its own tool.
- A job's own `connectors:` list (a job runs as a department worker, so it already gets the department's connectors). Cost caps per run, editing schedules from the page, and jobs on HQ or Product are not there yet.
