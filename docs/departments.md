---
title: "Departments: Role, Goals, Outputs and Jobs"
order: 9
---

# Departments: role, goals, outputs and jobs

A project's **departments** live in `projects.yaml`, under the project. Every project has two: **HQ**, the CEO's room with the business goals, and **Product**, the software factory that runs `workflow.yaml`. `departments:` adds more (Marketing, Sales, …). A department's work is the issues with `department: <name>` in their frontmatter, or in one of its `systems:`; the rest is Product's.

Each department, HQ and Product can have:

- a **mission**: what it is for, in a sentence;
- a **role prompt**: a markdown file a lead or agent of the department starts with;
- **goals** (KPIs): numbers with a target, read by hand or from a file. Department goals **roll up** into HQ's business goals.

Nothing shows unless it is configured. A project without `company:` and goals looks as before: the company header shows the two open epics furthest along.

## Settled rules

Rules the owner and the lead have already settled for department and Factory work. Don't derive them again from a lead message: follow them, and cite the decision when a design leans on one. A rule changes only by a new decision on the issue that changes it, and then this list changes with it.

- **Secrets are references.** A connector names `env:<VAR>` or `keychain:<service>`, never a value; the owner creates the value, the engine never writes, prints or copies it, and pages show only set / missing. A connector reaches only its department's lead and workers, never the CEO or Product (the owner uses `--as-owner`). Tests use fakes in a sandbox project, never real credentials. Sources: factory/department-connectors#D-1, #D-2, #D-3, #D-4, #D-5, #D-6, #D-7; see [Connectors](#connectors).
- **Private projects stay private.** Content from the owner's private projects (golf-rpg, meals, Jemik, client rosters, client sites, Keychain service names) stays in that project's repo or tracker. This repo, its tests, fixtures, docs and prompts name such a project by name and slug only, with placeholders for the rest. Source: factory/factory-benchmark-and-evals#D-10; the pilot's runbook below.
- **Budgets are shown, never enforced.** A `budget:` is "x of y" beside the spend; nothing refuses, pauses or waits on it until the owner decides otherwise. Spend is read from this machine's Claude Code transcripts, numbers only, and the engine bundles no prices. Source: factory/department-budgets-what-each-department-spends-in-tokens-and-money-shown-in-its-room#D-2, #D-3 (N-18).
- **Business data sits with the work files.** Goal values, outputs and connector checks are logs under the issues dir (`.goals/`, `.outputs/`, `.connectors/`), so a private tracker keeps them private; the viewer holds no state of its own. Sources: factory/department-role-and-goals (values log), factory/department-outputs-shelf#D-1.
- **One waiting-on-you count.** Every "waiting on you" number is the length of `needsYouRows` (`needs_you.go`). Anything department-level that waits on the owner (outputs to review, due jobs, a failing connector) adds rows there; no page counts its own waits on top. Sources: factory/needs-you-list-every-waiting-item-in-one-place-with-approve-all, factory/department-outputs-shelf#D-3.
- **Box keys.** The boxes on the company page and in connections are `hq`, `product`, a department's key and a group's key (`tracker.DepartmentKey`: the name slugified). Use these keys in `requested_by:`, `data-box` and JSON, not display names. Source: factory/connections-between-boxes-requests-outputs-and-dependencies-drawn-between-departments-on-the-floor-map#D-3.
- **Only real data, honest empty states.** No mock or sample data on a page; a box with nothing in it says so and names the command that would fill it. Sources: factory/global-screens (rules for all Workshop Board screens), the connections issue above.
- **Bootstrap mode reads projects.yaml.** Department config lives in `projects.yaml`, so anything issue-cli reads about departments must also work with no `--project`, run from a project root. Test that path. Source: factory/department-role-and-goals (the playtest found the bug; fixed in `cmd/issue-cli/helpers.go`).
- **Writing projects.yaml keeps it byte for byte.** A feature that edits `projects.yaml` splices only its own lines (comments and order kept), reads the result back before an atomic write, and never overwrites a file. The viewer reads `projects.yaml` at start, so a viewer write reloads the project (`reloadProject` in `dept_add.go`). Sources: factory/department-templates-add-a-department-to-any-project#D-2, ui/the-company-pages-new-department-tile-adds-a-department-from-a-template.
- **Department lint: `departments`, plus `department` for workflow files.** New warnings about department config in `projects.yaml` (prompts, goals, groups, outputs, connectors, files a gate waits on) go in the `departments` check. `department` covers only a department's own workflow file: it doesn't load, it has a project-wide key, or an issue's status isn't in it. Two checks exist because they read different files, and `--check department` is already in use; don't add a third for department config. (`owner-only`, which also reads `sends:` connectors, is about YOLO gates in any workflow, not department config.) Source: factory/departments-the-settled-rules-written-down-once#D-1.
- **New departments come from templates.** No empty custom department; groups, templates and channel types are generic engine pieces every project can add. Anything a department needs that the pieces don't give is fixed in the engine for every department, not as code for that one. Sources: factory/factory-departments-floor-map-and-capabilities#D-9, #D-10, factory/marketing-pilot-seo-as-the-first-department#D-5.
- **Groups nest up to 3 deep.** A group is a `departments:` entry; children name it with `parent:`, and a group may sit in a group, at most 3 groups above any department (`tracker.MaxGroupDepth`); a loop or a deeper chain is a lint finding and that department counts as in no group. Its lead is the lead of leads; routing walks department lead → each group's lead up the chain → CEO, first live. Sources: factory/factory-departments-floor-map-and-capabilities#D-5, #D-6, factory/department-groups-a-parent-with-a-lead-of-leads-gtm-over-marketing-and-sales#D-1, #D-2, factory/nested-groups-a-group-inside-a-group-with-goals-rolling-up-and-the-map-drawing-it#D-1, #D-2.
- **Channels are types, paired with a system of the same name.** SEO, PPC, social and cold outreach are types in Marketing's workflow file, not departments. Each channel type has a system of the same name (`seo-pass` ↔ `SEO`); `create --type seo-pass` doesn't set `--system` for you, so pass both. Sales has no template yet. Sources: factory/factory-departments-floor-map-and-capabilities#D-7, #D-8, factory/marketing-channels-as-types-seo-first-room-for-ppc-social-and-cold-outreach#D-1.
- **A template's workflow change reaches tests outside the template.** Changing a bundled department template's `workflow:` or statuses also breaks tests and playtest sessions that expect it (`dept_add_test.go`, `cmd/issue-cli/cmd_department_test.go`, `playtest/sessions/department-templates.yaml`, the scenarios): grep for the old name and run `make validate` and `make playtest-check`. Source: retro of factory/marketing-channels-as-types-seo-first-room-for-ppc-social-and-cold-outreach.
- **Engine requests go via the CEO.** A department that needs the engine changed files the request (`requested_by:` filled); the CEO forwards it to Product. Declared as `{from: <dept>, to: product, via: hq}` under `connections: requests:`, lint warns on a direct route. Sources: factory/marketing-brand-foundation-brand-analyst-icp-and-tamsamsom-before-channel-work#D-8, factory/declared-connections-who-may-ask-whom-and-what-feeds-what-with-routing-rules#D-6.
- **Nothing goes out without the owner's stamp.** Outputs start in review; only the owner approves; an agent publishes only an approved one. A step that publishes, sends or spends sits behind an `owner_only` gate, which YOLO never passes ([YOLO Mode](Workflow/yolo.md#gates-yolo-never-passes)). A job's standing start is off until the owner turns it on with their words. Sources: factory/department-outputs-shelf#D-2, factory/department-connectors#D-6, factory/department-recurring-jobs#D-3, factory/marketing-pilot-seo-as-the-first-department#D-4, workflow/an-owner-gate-yolo-never-passes-spending-money-and-publishing-outside.
- **A pilot is rehearsed on a sandbox; go-live comes later.** A department pilot is first run end to end on a throwaway project with placeholder sites and stand-in connectors (a guided check script); going live on the real project follows from a runbook once the owner gives the repo and connectors. Source: factory/marketing-pilot-seo-as-the-first-department#D-2.
- **Research watches memory.** Research steps run `mem-guard check` before a browser or bulk fetch, prefer plain fetches, and use one browser at a time; tests use fixtures, never the live web. Source: factory/marketing-brand-foundation-brand-analyst-icp-and-tamsamsom-before-channel-work#D-2, #D-7.

## Config

```yaml
projects:
  - name: Jemik
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
| `source` | `manual` (the default) or `file:<path>#<metric id>` |
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
  A missing file, a missing id or a value that is not a number shows on the goal as an error. The page and the commands still work.
- Connectors (Search Console, QuickBooks) come later as another kind of source.

## Progress and the roll-up

- **Progress** is `value / target` for `up`. For `down` it is 100% at or below the target, else `target / value`. It is clamped to 0–100%.
- **The roll-up:** an HQ goal with a value of its own shows that value. Without one (manual and nothing recorded), its progress is the **mean progress** of the department goals whose `rolls_up:` names it, counting only those that have a progress. Units are never added together: a count of pages and a sum of euros only meet as percentages.
- **Through a group:** a department under a [group](#groups) may roll a goal up into one of the group's goals (`rolls_up: pipeline`) or straight into an HQ goal. The group goal follows the same rule (its own value, else the mean of its departments' goals), and counts in HQ's mean like any department goal.

## Where it shows

- **Company page** (`/p/<project>/company`):
  - **The header** shows HQ's first two goals, value / target with a bar in the project's colour.
  - **HQ** shows its mission, then each HQ goal with the department goals rolling into it, in their department's colour.
  - **Each department room and the Product line** show the mission and the first three goals.
  - A stale or unreadable goal is muted, and its tooltip says why.
- **Project map** (`/`, the lot of each project): the bar under the lot is HQ's first goal, else the first goal of Product or a department (`Goal: Marketing · Leads`), with value / target. A goal with no value yet reads "no value yet" and its tooltip gives the `goal set` command. A project without goals keeps the open epic furthest along.
- **`issue-cli goals [<dept>] [--json]`**: the roll-up as text. Each HQ goal is followed by the goals under it (`↳ Marketing: …`), then each department's goals, with value, target, progress, period, date (`stale, from …`), source, and the error or the `goal set` command for a goal with no value yet. With nothing configured it prints the config snippet.
- **`issue-cli department show <name> [--json]`**: the mission, role, the role prompt's path and content, the goals, and its [spend](#spend-and-budgets) this week. `<name>` is `hq`, `product` or a department.
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

- **Where it points**: exactly one of `--url` (an http(s) link), `--file` (copied onto the shelf, under `<issues>/.outputs/files/<dept>/`, up to 25 MB) or `--path` (a file in the repo, relative to the project root; it must exist). A file or path that later disappears shows as **missing**.
- **Which shelf**: the department of `--issue`, else `--department`, else Product. A `--department` that disagrees with the issue's is refused.
- **`--kind`** is any word. `post`, `design`, `site`, `page`, `report` and `invoice` get an icon; anything else shows 📦.
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

`issue-cli output publish <id> [--url <where it went out>]` marks an output **published**, and only once it is approved. On the content workflow, the Draft box asks for `output add`, and the Published box asks for `output publish`. The issue's own `owner review → publish` gate still stamps the issue as a whole. The shelf stamps each piece, so the owner can approve eight of ten pages a weekly pass made.

### Where it shows

- **Company page**: each department room and the Product line show `📦 N to review · M made this week`, linking to their shelf. HQ shows the outputs to review across departments and the newest five.
- **The shelf** (`/p/<project>/shelf`, `?dept=marketing&state=review`): tabs by department and state with counts, and a row per output: icon or image thumbnail, title linked to the URL or the shelf file, department, kind, the issue (to its station) or job, who, when, state, and Approve / Reject for those in review. Shelf files are served only from `.outputs/files`, under a sandbox CSP.
- **The station** (the issue page) lists the outputs the issue made.
- **Waiting on you**: a department with outputs in review is one item in the company panel, on the Status page (the Waiting card links to the shelf), in `status.md` and in the sidebar count.
- **CLI**: `issue-cli outputs [<dept>] [--state review|approved|rejected|published] [--issue <slug>] [--json]` lists the shelf by department. `issue-cli output show <id>` shows one output. `issue-cli start` / `show` print `Outputs: 2 (1 to review)`, or on a department or content-workflow issue that made nothing yet, the `output add` command. A transition into `owner review`, `publish` or `done` on such an issue prints the same hint. It is a hint, not a gate.
- **Reading and commenting** (markdown outputs): the title of a `.md` file or path opens the **reader**, `/p/<project>/shelf/read/<id>` (the raw file stays one click away as **Raw file**). The document is rendered (headings, tables, task lists; raw HTML dropped) with a contents column and a Day / Night switch for the sheet. The owner comments in the margin: select a passage (a **Comment on this passage** button appears), use 💬 on a heading, or write on the whole document; threads take replies and **Resolve**. Comments live beside the file in `<issues>/.outputs/comments.jsonl` (never in it), each anchored by the quoted passage, 30 characters either side and its heading, and each owner comment or reply is posted on the output's issue as `revise: output ab00 v1 · Brand › Proposal · “…” — <text> (thread c-1a2b · issue-cli output comments ab00)`. Only the owner's browser writes them (`fromOwnerBrowser`; anything else gets a 403 naming the CLI). An agent reads them with `issue-cli output comments <id> [--all]` (where each thread sits in the newest version), answers with `issue-cli output reply <id> <thread> --text "..."`, and adds the version that answers them with `issue-cli output revise <id> --file <file> [--note]`: a copy next to the earlier ones, the output back in review. The reader then shows **Changes since vN-1** (a line diff), keeps each thread on its passage when the passage survived, puts it under its heading when only the heading did, and folds it under "not in vN" otherwise. The shelf row shows the version and `💬 N open`; `outputs`, `output show` and the issue's `Outputs:` line show the open count and the command.
- **`issue-cli health lint`**, check `departments`: warns about log lines it skipped, outputs of a department `projects.yaml` no longer has, a file or path that is gone, and an output in review for more than 14 days.

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
- **CLI:** `issue-cli lead whoami` shows a group lead's departments and a department lead's group and who it reports to right now; `issue-cli lead list` shows each group's department leads indented under it; `issue-cli goals` heads a department under a group `(in group gtm)`; `issue-cli department list` marks a group (`gtm  GTM (group: marketing)`) and a department in one (`· in group GTM`).
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

- **Goals**, with how far each moved in the last 7 days. For a manual goal, that is the newest value in the window against the newest before it. A goal read from a file shows its value only.
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

A **department template** is a ready department: a workflow file, a role prompt and a goals skeleton, bundled with issue-cli (`cmd/issue-cli/templates/departments/<name>/`) like the workflow templates. **Marketing** (SEO and content, on the `marketing` workflow: the `content` statuses plus a `measure` step, with each channel a [type of work](#channels-as-types)) is the first. Product is built in: it is the `development` workflow every project runs.

```
issue-cli department list                    # HQ, Product and the templates, installed or not
issue-cli department add marketing --dry-run # the plan, nothing written
issue-cli department add marketing           # install it
issue-cli department remove marketing
```

`department add <template>`:

- writes `workflows/<key>.yaml` (the template's workflow template, as `workflow init` writes it), `departments/<key>/role.md` and each job's brief as `departments/<key>/jobs/<id>.md`, relative to the project root. `<key>` is the department's name in lower case;
- adds the department to the project in projects.yaml, under `departments:` (creating the key when the project has none): name, colour, role, mission, systems, `prompt:`, `workflow:`, the goals, each manual with no value yet, and the template's [recurring jobs](#recurring-jobs). Their standing start is off: a due run waits in Needs you until you start it or turn it on;
- ends with the next steps: `department show <key>`, `create --department <key> --title …`, `lead spawn --department <key>`, the workflow's types of work (`Types of work: piece (default), seo-pass`), which files to edit (the role prompt and each job's brief), and `issue-cli connectors <key>` for the connector config, since templates carry none.

What it never does:

- **overwrite a file.** One that is already there with the template's content is reported as already there; one that differs is **kept** and reported as your edited file. For a kept workflow file it prints the `workflow init --template <name> --out workflows/<key>.template.yaml` command that writes the bundled one next to yours to compare ([Upgrading an installed Marketing](#upgrading-an-installed-marketing)). Only the projects.yaml entry is added;
- **change the rest of projects.yaml.** The entry is spliced in as text, so comments, quoting and every other project stay byte for byte. The result must load and contain the department, else the file is left as it was. A symlinked projects.yaml is edited through the link;
- **install a template twice.** A template installs once, under its own name; that holds for a group template too. The project has it when a department has the template's name, or its workflow file is stamped with the template's workflow (a Marketing installed under another name before `--name` went away). The refusal is the same in the CLI and the viewer: "Marketing is already a department of <project> (<projects.yaml>): a template installs once. To add it again, remove it first: issue-cli department remove <key>";
- run from a worker session: it edits the owner's file. A lead runs it with the owner's words; `--dry-run` works for anyone.

**Group templates.** A template with `group: true` and `children: [marketing]` is a [group](#groups): **GTM** is the first. `department add gtm` writes `departments/gtm/role.md` (a group has no workflow file: its own issues run on Product's), adds GTM's entry, then installs each child from its own template with `parent: gtm` in its entry, never overwriting a file. A child you already have with no `parent:` is moved under the group (one `parent: gtm` line spliced under its `name:`), its files left as they are; one already under another group is left there and the plan says so. `department add <template> --parent <group>` installs a department under a group the project has; a group template takes no `--parent`. `department list` and the tile list departments first, then groups.

`department remove <name>` takes the entry out of projects.yaml and names the files it left in place. It refuses while the department has issues that haven't finished its workflow, listing each with the `set-meta … --key department` command that moves it, and while departments sit under it (a group).

The Updates page (`/p/<project>/updates`) has a **Departments you can add** section: each template the project hasn't installed, what it brings its **Add…** button (the preview below) and its `department add` command. It isn't counted in the sidebar's Updates number, which counts workflow features.

**From the company page.** The **+ New department** tile on `/p/<project>/company` opens a form: one choice per bundled template the project hasn't added (a group template says which departments it installs) and, once the project has a group, **Under which group?** (none, or each group; ignored for a group template). With every template added the tile says "Every department template is added" and has no form. **Preview →** opens `/p/<project>/departments/new?template=<key>`, the plan `department add --dry-run` prints: each file with its state (written, already there, or your edited file kept), the projects.yaml path and the exact entry. Nothing is written before **Add <name>**, which runs the same install (internal/depttemplate), reads the project back from projects.yaml so the room shows without restarting the viewer, and returns to the company page with "✓ Added <name>: …" and the new room outlined. Start its lead from the room. The viewer adds departments only from the templates. A template the project has is refused on the preview with the reason, and Add answers 409 with it, writing nothing. Add (`POST /p/<project>/departments/add`) takes only the owner's browser: a cross-origin request, or one that sends neither `Sec-Fetch-Site: same-origin` nor a same-host `Origin` (an agent's curl), gets 403; agents use `issue-cli department add` with the owner's words. A project without a projects.yaml shows the tile with no form. A department added with the CLI while the viewer runs still shows after a restart.

**Adding a template**: a folder `cmd/issue-cli/templates/departments/<name>/` with `department.yaml` (`name`, `description`, `color`, `role`, `mission`, `systems`, `workflow`: a bundled workflow template, `goals`, `jobs`: each with its `brief:` a file in the folder, `jobs/<id>.md`) and `role.md`. The file is read strictly: an unknown key, a job whose schedule doesn't parse or a missing brief is an error. Marketing ships `weekly-seo-pass` (Mondays 07:00, `type: seo-pass`). Templates don't carry connectors yet: add them by hand under the installed department ([Connectors](#connectors)).

### Channels as types

A marketing **channel** (SEO, PPC, social, cold outreach) is a [type of work](Workflow/types.md) in Marketing's workflow file, not a department of its own. Each type has its own path through the statuses and its own prompts, and pairs with a **system of the same name** in the department (`seo-pass` ↔ `SEO`): its folder under `issues/`, its room in the department and the board's `?system=` filter. The pairing is a convention the template and its comments keep; the engine doesn't bind a type to a system, so name both when you create one by hand.

The bundled `marketing` workflow (`cmd/issue-cli/templates/workflow/marketing.yaml`) has two channel types, and two [brand foundation](#brand-foundation-and-research-mode) types that come before them. Every type goes out in `publish`, behind `owner review → publish`, which is `owner_only`: YOLO never passes it, so nothing is posted, sent or paid for without the owner ([YOLO Mode](Workflow/yolo.md#gates-yolo-never-passes)). The types:

| Type | Path | For |
|:--|:--|:--|
| `piece` (default) | brief → draft → owner review → publish → done | one post, page or email |
| `seo-pass` | brief → draft → owner review → publish → **measure** → done | an SEO pass over the client sites (system `SEO`) |
| `foundation` | research → brand → icp → owner review → publish → done | the brand and the ICP, settled with the owner |
| `market-size` | research → market size → owner review → publish → done | TAM / SAM / SOM, later or again |

Neither channel's brief moves to draft until the department's `brand.md` and `icp.md` exist.

- `measure` records what went out and what it did: the goal values (`issue-cli goal set marketing/<id> <value>`) and a `## Measure` note with where the numbers came from. A number you can't read yet (rankings before Search Console is connected) is "no value yet", not zero.
- The base workflow has no `publish → done` edge, so a type that measures can't skip it; `piece` adds its own `publish → done`.
- `seo-pass` overrides the brief, draft and measure prompts: the sites and pages in the pass, local pages, FAQs and schema, each page on the shelf with `--kind page`, then the week's numbers.
- The weekly SEO job sets `type: seo-pass` ([Recurring jobs](#recurring-jobs)), so each week's run is an seo-pass. A site too big for one pass gets its own: `issue-cli create --department marketing --type seo-pass --system SEO --title "<site>: SEO pass"`.

```
issue-cli process workflow --department marketing   # the types and their paths
issue-cli create --department marketing --type seo-pass --system SEO --title "..."
issue-cli list --department marketing --type seo-pass
```

#### Adding the next channel

PPC, social and cold outreach are commented-out slots at the end of `types:` in the marketing workflow. Adding one is a change to the workflow file (yours, or the bundled template for every project), not engine code:

1. **Uncomment its type** in `workflows/marketing.yaml` (or copy `seo-pass`), and give it its own `statuses:` prompts: what a brief covers for that channel, what the draft is (ad copy, a post with images, an email sequence), what to measure (spend and conversions, reach, replies).
2. **Add its system** to the department in projects.yaml: `systems: [SEO, PPC]` (or `Social`, `Outreach`). Its issues go under `issues/<System>/` and show as a room and a filter.
3. **A recurring job** (optional): a `jobs:` entry with `type: <channel>` and its own brief, for example a weekly social calendar.
4. **Check it**: `issue-cli health lint` (a path entry that isn't a status, a section a gate checks but nothing on the path appends, or a job's `type:` the workflow lacks are warnings), then `issue-cli process workflow --department marketing`.

A channel whose work never goes out (research, reporting) can drop `owner review` and `publish` from its `path:`; one that doesn't measure leaves `measure` off and adds a `publish → done` transition, as `piece` does.

#### Upgrading an installed Marketing

A Marketing department installed before channels has the `content` workflow in `workflows/marketing.yaml`. Nothing rewrites it: `department add` keeps an edited file, and `issue-cli workflow upgrade --department marketing` only proposes the catalog's features for its own template (`content`). To move to channels:

1. Write the bundled file next to yours: `issue-cli workflow init --template marketing --out workflows/marketing.template.yaml`.
2. If you never edited yours, replace it with the new one. Otherwise copy across what you need: the `measure` status, the `publish → measure` and `measure → done` transitions, and `default_type:` / `types:`. Issues without `type:` become `piece` and keep their path.
3. Add `type: seo-pass` to the `weekly-seo-pass` job in projects.yaml, and run `issue-cli workflow scan --department marketing` to update the file's features record, then `issue-cli health lint`.

### Brand foundation and research mode

Before any channel, Marketing settles who the brand is and who it is for. Without that, every brief has to guess. The foundation is two types of work in the same workflow file, both in **research mode**. The owner names a topic (the business, a product, a market), the worker researches it (the client's own sites, competitors, search results, reviews, forums), and proposes answers. The owner settles them at a review gate. The worker helps them decide and doesn't decide for them.

**`foundation`**: the basics, needed before channel work.

| Status | What the worker does |
|:--|:--|
| `research` | Lists each source under `## Research`, with its link and one line on what it says. If the issue names no topic, the worker asks the owner. At least 200 characters are needed to move on. |
| `brand` | Proposes `## Brand`: 3–5 traits, the voice with a short example, do / don't guidelines, each with its source, and what the owner has to settle. |
| `icp` | Proposes `## ICP`: who (segments, roles, size, place), their problems in their own words, where they are, how to reach them (targeting, a message per segment) and who is not a target. Puts the brand and ICP drafts on the [shelf](#outputs-shelf) as `--kind report`. |
| `owner review` | Says in chat what to review and what is open. Moving on needs `publish` approval in the issue viewer. A `revise:` comment sends the issue back to `icp`. |
| `publish` | Writes exactly what was approved to `departments/<key>/brand.md` and `icp.md`, next to the department's `role.md`, and marks the shelf report published. `publish → done` checks that both files exist. |

**`market-size`** walks research → market size → owner review → publish → done. `## Market size` gives TAM, SAM and SOM, each with its number and unit, the method (top-down from a published figure, or bottom-up from counts × price), the sources and every assumption. A number that can't be supported is "no value yet" with the reason. SAM starts from the ICP. At publish it writes `departments/<key>/market.md`. It gates nothing, so run it once the ICP is settled and again when the market moves.

**Basic first.** The shared `brief → draft` transition, which `piece` and `seo-pass` both walk, checks `departments/<key>/brand.md` and `icp.md` with the [`file_exists`](workflow.md#files) validator. Until both are there, the refusal names the missing file and gives the command that starts the foundation:

```
departments/marketing/brand.md is missing — the brand foundation comes first: issue-cli create --department marketing --type foundation --title "Brand foundation: <topic>"
```

That includes the weekly SEO job's runs: a run waits at `brief` until the foundation is published. `issue-cli health lint` warns about the missing files ahead of time. Files written some other way (by hand, or from before this change) count too: the gate checks that the file exists and isn't empty, not which issue wrote it.

**Where the results are read.** The template's `role.md` tells every worker of the department to read `brand.md`, `icp.md` and `market.md`. The channels' `brief` prompts say to cite them, and to name the ICP segment the piece is for. `<key>` is the department's key, so a Marketing installed as `--name Growth` reads `departments/growth/`.

**Memory.** Research is web-heavy. The research prompt has the worker run `mem-guard check` before opening a browser or fetching many pages (where mem-guard is installed), wait while it says BLOCKED, prefer plain fetches to a browser, and keep one browser open at a time.

```
issue-cli create --department marketing --type foundation --title "Brand foundation: <topic>"
issue-cli create --department marketing --type market-size --title "Market size: <topic>"
```

#### Adding the foundation to an installed Marketing

`department add marketing` installs it. A department file from before it gets it as the `brand-foundation` catalog feature: `issue-cli workflow upgrade --department marketing` proposes it with a snippet. `upgrade` only proposes a template's own entries, so a Marketing file still stamped `template: content@…` needs its stamp set to `template: marketing@v0.57.0` first. The same pass then also proposes `types-of-work` and `measure-after-publish` ([Upgrading an installed Marketing](#upgrading-an-installed-marketing)). Copy the four statuses (`research`, `brand`, `icp`, `market size`) with their prompts, their transitions, the `foundation` and `market-size` types, and the two `file_exists` validates on `brief → draft` from the bundled file (`issue-cli workflow init --template marketing --out workflows/marketing.template.yaml` writes it next to yours). Then add the `## Brand, ICP and market` section to `role.md` and run `issue-cli workflow scan --department marketing` and `issue-cli health lint`. Once the gate is in, open the first `foundation` before the next channel brief.

## Recurring jobs

A department can have work it does on a calendar: a weekly SEO pass over the client sites, a daily lead search, monthly invoice chasing. Each is a **job** in its `departments:` entry, and each time a job is due it makes a **run**: an issue in the department, started like any worker.

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
```

A job's key is `<department>/<id>` (`marketing/weekly-seo-pass`).

### A run is an issue

`issue-cli job run` creates the run as `<System>/<id>-<period>` (the department's first system, else its name), for example `SEO/weekly-seo-pass-2026-w44.md`:

- **Fields:** `department:`, `job: marketing/weekly-seo-pass`, `run: 2026-W44`, the label `job`, the job's `priority:`, `type:` when the workflow has types, and a `slug:` pin.
- **Type:** the job's `type:`, else the workflow's `default_type`. A type the department's workflow doesn't define stops the run with the types it has, and `issue-cli health lint` warns about it. Marketing's weekly pass is an `seo-pass`.
- **Status:** the first status of the run's type in the department's workflow. On the `marketing` template that is `brief`.
- **Body:** the brief file, with `{{period}}`, `{{date}}` and `{{last_run}}` filled in, then a `## Run` line naming the job and the previous run.

Its worker is dispatched like any other, with `## Your department` in its briefing. Everything the engine already keeps for an issue is the run's record: the gates on its path, the Timeline, the token estimate in its stats sidecar, and the outputs it puts on the shelf (`output add --issue`). On `content` and `marketing`, nothing goes out before `owner review → publish`. Under YOLO, that gate waits in YOLO's after-the-fact review instead.

- **Periods.** A daily run is keyed `2026-10-30`, a weekly one by ISO week (`2026-W44`), a monthly one `2026-10`. A period has one run. A second `job run` in the same period names the existing run and creates nothing, unless you add `--again` (which makes `…-2`).
- **Due.** A job is due once its period's `on`/`at` has passed and that period has no run.
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
3. **The job's standing start.** This is the only unattended start, and it is off by default. `issue-cli job auto on <job> --quote "<your words>"` (or `--all` for every job of the project) stores your words in `.jobs/<project>.json` in the main checkout, next to `.autostart/`, never committed. From then on, `job run --scheduled` may start the due run without you naming it, inside auto-start's budget:
   - `mem-guard check` passes;
   - memory is under 50%;
   - the project has fewer than 2 live workers;
   - no project on this machine started a worker in the last 5 minutes. This is the same lock and stamp as `dispatch --autostart`.

   If any of these fails, nothing starts and the reason shows in `job due`. Only you, or a lead with your words, turns a standing start on or off (`job auto off <job>|--all`). Workers are refused. Each closed window goes to `.jobs/<project>.log.jsonl`.

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
        check: "gsc-cli whoami"       # optional, read-only: exit 0 is healthy
        note: "Search Console for the client roster"
      - name: wordpress
        kind: tool                    # a command run through issue-cli connector exec
        command: wp
        secrets:
          WP_APP_PASSWORD: env:ACME_WP_PASSWORD
        check: "wp --info"
        sends: true                   # it can publish or spend
```

| Field | Meaning |
|---|---|
| `name` | Required. With the department it makes the address: `marketing/search-console` |
| `kind` | `mcp` (a server on stdin/stdout, loaded into the agent's session) or `tool` (a command the agent runs through `connector exec`) |
| `command`, `args` | What runs. Required for `mcp`. For a `tool`, the agent may pass its own command after `--` instead |
| `secrets` | Env var → reference. A reference is `env:<VAR>` (read from the environment issue-cli runs in) or `keychain:<service>[/<account>]` (a macOS Keychain generic password). Nothing else is accepted |
| `check` | A read-only shell command, run with the secrets; exit 0 is ok |
| `sends` | The connector can publish, post or spend. It is used only for an output the owner approved on the shelf, then `output publish`. The department's workflow needs an `owner_only` gate before that step, so YOLO never passes it; `health lint` warns when it has none ([YOLO Mode](Workflow/yolo.md#gates-yolo-never-passes)) |
| `note` | A line on the room |

### Secrets

The owner creates each one. Keychain example: `security add-generic-password -s acme-gsc -a "$USER" -w`. For env, export it in the shell profile agents start from. A secret is **set** or **missing** on this machine, and the engine decides which without reading the value: env by presence, Keychain with `security find-generic-password -s <service>` (without `-w`). The value is read only by `connector exec` and `connector check`, and only into the child process's environment.

### Running one

`issue-cli connector exec <dept>/<name> [-- <cmd> …]` is the only way in:

- **mcp**: it runs the server on stdin/stdout. Agents don't call it themselves: their MCP config points here.
- **tool**: it runs `<cmd>` after `--`, else `command:` with `args:`. The exit code passes through.

Who may run it:

- the department's workers (`ISSUE_VIEWER_ISSUE_SLUG` names an issue of the department) and its lead (`ISSUE_VIEWER_LEAD_SCOPE=department:<key>`);
- **not** another department's agents, Product's workers or the CEO. The refusal names the owning department and its lead;
- not a shell outside any agent session. The owner adds `--as-owner` in their own shell; an agent session can't.

A missing secret stops the run before anything starts, naming the reference and how to create it. This is a guard against mistakes, **not a security boundary**: anything running as the owner can read their environment and their Keychain.

### At launch

When the human starts a department worker (`dispatch`) or the department lead (`lead spawn --department`), the launcher adds the department's `mcp` connectors whose secrets are set:

- claude gets `--mcp-config <checkout>/.agent-logs/<session>/mcp.json`, written fresh at each launch (mode 0600);
- codex gets `-c mcp_servers.<name>.command=…` overrides.

Each server is `issue-cli --project <slug> connector exec <dept>/<name>`, with the session's `ISSUE_VIEWER_*` and `ISSUE_CLI_LOG` variables, so neither form holds a secret. The launch's steps name the connectors it added. Product's workers, the CEO and other departments get no MCP flags.

### In the briefing

The worker's `## Your department` and the department lead's brief list the connectors:

```
- Connectors: search-console (MCP, loaded in this session) · wordpress (tool: issue-cli connector exec marketing/wordpress -- <cmd>) — it sends: use it only for an output the owner approved, then issue-cli output publish · quickbooks: secret QB_TOKEN missing on this machine, ask your lead (issue-cli connectors marketing).
```

`issue-cli start` / `show` on a department issue print `Connectors: 3 (1 missing) — issue-cli connectors marketing`.

### Health

`issue-cli connector check [<dept>[/<name>]]` runs each `check:` with its secrets, a 20 s timeout and its output thrown away, so a check that prints a token leaks nothing. It appends one line per connector to `<issues>/.connectors/checks.jsonl`, next to the goals and outputs logs (a private tracker keeps it private too):

```json
{"connector":"marketing/wordpress","date":"2026-10-30T12:00:00Z","result":"failing","exit":1,"ms":840}
```

`result` is `ok`, `failing` (with the exit code, -1 on a timeout), `missing` (a secret isn't set) or `no-check`. The owner or the department's lead runs it. It exits 1 when one is failing or missing.

### Where it shows

- **`issue-cli connectors [<dept>] [--json]`**: each connector with its kind, `sends`, the last check and its age, and each secret as `VAR reference set|missing`. With nothing configured it prints the config snippet. `issue-cli department show <key>` lists the same.
- **Company page**: the department's room has `🔌 N connectors · M failing`. Unfolded, it lists each connector's kind, the sends badge, its secrets set or missing, and the last check ✓/✗ with its age. **Check now** runs the department's checks in the background (`POST /p/<project>/connectors/check`, same-origin only), and the room reloads when they are in. `GET /p/<project>/connectors.json[?dept=<key>]` is the same list.
- **Needs you**: a department with a connector whose secret is missing or whose last check failed is one row ("Marketing: wordpress check failing"), on the company panel, `/needs-you` and the sidebar count.
- **`issue-cli health lint`**, check `departments`: warns about an unknown `kind:`, a duplicate or badly formed name, an `mcp` connector without `command:`, and a secret reference that isn't `env:` or `keychain:` (a bare value "looks like a secret value"; the finding never quotes it). It also warns about `connectors:` on `company:` or the Product entry: connectors belong to a department.

### Not here

No vault, no encrypted file and no secret entry in the viewer: the owner keeps the values. No OAuth flows or token refresh. No connector-specific code: a connector is a command or an MCP server the owner names. Nothing is published through a connector by the engine itself. There are no spend caps, and checks don't run on a timer (a recurring job can run `connector check`). HQ and Product have no connectors.

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
- **No catalog entry**: no `workflow.yaml` key (#D-7).

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

A **group** shows its own (its lead's) plus its departments'; **HQ** shows everything attributed. Only Claude Code writes these transcripts: other agents (codex) count as zero. Files last changed before the window are skipped and each file's numbers are kept in memory until it changes, so the polled company page doesn't re-read them.

### Prices: the estimate

Cost is tokens × price per million tokens, per message by its model. Prices come from `$XDG_CONFIG_HOME/ai-workflow-engine/prices.yaml` (else `~/.config/…`), the benchmark's `prices:` shape; the owner's [benchmark files](benchmark.md) fill models it doesn't name. A key prices every model it is a prefix of (`claude-opus-5` prices `claude-opus-5-5`).

```yaml
prices:                 # USD per million tokens
  claude-opus-5-5: {in: 0, out: 0, cache_read: 0, cache_write: 0}
```

The engine bundles no price list (D-2): a model without a price shows `cost unknown`, a box with some unpriced tokens `≥ $x est.`, and `issue-cli spend` names the models and the file to add them to. Every cost is labelled `est.`.

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
2. **Install the department**: `issue-cli --project <project> department add marketing --dry-run`, then without `--dry-run`. It writes `workflows/marketing.yaml`, `departments/marketing/role.md` and `departments/marketing/jobs/weekly-seo-pass.md`, and adds the entry with its goals and the job. SEO is the first [channel](#channels-as-types) (`seo-pass`); the others come later as types.
3. **Settle the brand and the ICP**: `issue-cli create --department marketing --type foundation --title "Brand foundation: <topic>"`, then approve its `publish` once the brand and ICP read right ([Brand foundation and research mode](#brand-foundation-and-research-mode)). Channel briefs can't move to draft before that.
4. **Say what the department should know.** In `departments/marketing/role.md`: the client sites, the tone of voice, what to check before a draft goes to review. In the job brief: what a pass covers per site.
5. **Give it a business goal to count towards** (optional): an HQ goal under `company: goals:`, and `rolls_up: <hq goal id>` on the department goal that feeds it (`marketing/pages`).
6. **Add the connectors.** `issue-cli connectors marketing` prints the config. A read-only one first (Search Console as an `mcp` server) is the safer start; a publishing one (WordPress as a `tool`) gets `sends: true`. Each secret is a reference: `env:<VAR>` or `keychain:<service>`. Create the value yourself, for example `security add-generic-password -s <service> -a "$USER" -w`.
7. **Check them**: `issue-cli connector check marketing`. Both should be ok; `issue-cli connectors marketing` shows each secret set.
8. **Start the department's lead**: `issue-cli lead spawn --department marketing`, or ▶ Start lead in the room on the company page.
9. **Run the first pass yourself**: ▶ Start run on the Due jobs row in Needs you, or tell the lead to run `issue-cli job run marketing/weekly-seo-pass --quote "<your words>"`. The run is an `seo-pass` issue in `brief`; its worker puts each page and draft on the shelf.
10. **Review on the shelf** (`/p/<project>/shelf`): approve or reject each output. Only an approved one goes out, then `issue-cli output publish <id> --url <where it went>`.
11. **Record the numbers**: after publish the pass moves to `measure`, where the worker runs `issue-cli goal set marketing/pages <n>`; `issue-cli goals` shows the department and HQ.
12. **Let it run weekly** once a few passes went well: `issue-cli job auto on marketing/weekly-seo-pass --quote "<your words>"` turns on the standing start, inside auto-start's memory and worker budget. `job auto off` turns it off.

Anything a department needs that these pieces don't give is a gap in the shared pieces, not code for that department: fix it in the engine for every department.

## Not yet

- Groups inside groups, and a department in two groups.
- Connector sources, goal trends over time, and setting goals from the page.
- Publishing an approved output through a connector automatically, connectors in department templates, and editing or removing outputs from the page.
- A job's own `connectors:` list (a job runs as a department worker, so it already gets the department's connectors). Cost caps per run, editing schedules from the page, and jobs on HQ or Product are not there yet.
