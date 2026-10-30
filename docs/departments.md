---
title: "Departments: Role, Goals and Outputs"
order: 9
---

# Departments: role, goals and outputs

A project's **departments** live in `projects.yaml`, under the project. Every project has two: **HQ**, the CEO's room with the business goals, and **Product**, the software factory that runs `workflow.yaml`. `departments:` adds more (Marketing, Sales, …). A department's work is the issues with `department: <name>` in their frontmatter, or in one of its `systems:`; the rest is Product's.

Each department, HQ and Product can have:

- a **mission**: what it is for, in a sentence;
- a **role prompt**: a markdown file a lead or agent of the department starts with;
- **goals** (KPIs): numbers with a target, read by hand or from a file. Department goals **roll up** into HQ's business goals.

Nothing shows unless it is configured. A project without `company:` and goals looks as before: the company header shows the two open epics furthest along.

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

## Where it shows

- **Company page** (`/p/<project>/company`):
  - **The header** shows HQ's first two goals, value / target with a bar in the project's colour.
  - **HQ** shows its mission, then each HQ goal with the department goals rolling into it, in their department's colour.
  - **Each department room and the Product line** show the mission and the first three goals.
  - A stale or unreadable goal is muted, and its tooltip says why.
- **`issue-cli goals [<dept>] [--json]`**: the roll-up as text. Each HQ goal is followed by the goals under it (`↳ Marketing: …`), then each department's goals, with value, target, progress, period, date (`stale, from …`), source, and the error or the `goal set` command for a goal with no value yet. With nothing configured it prints the config snippet.
- **`issue-cli department show <name> [--json]`**: the mission, role, the role prompt's path and content, and the goals. `<name>` is `hq`, `product` or a department.
- **`issue-cli start` / `show`** print one line for an issue whose department has a role prompt or goals:
  ```
  Department: Marketing (Every client site refreshed every week) — role: departments/marketing/role.md · goals: issue-cli goals marketing
  ```
- `issue-cli` run from a project root without `--project` takes the departments, `company:` and the Product entry from the matching `projects.yaml` entry, so agents there see the same goals.
- **`issue-cli health lint`**, check `departments`: warns about a prompt file that does not exist, `rolls_up:` naming no HQ goal (or set on an HQ goal), a duplicate goal id or a goal with none, an unknown `period:`, `direction:` or `source:`, a file source whose file is missing, and `systems:` on the Product entry.

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
- **`--job`** names the job that made it when no issue did (recurring jobs, later). **`--note`** adds a line.

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
- **`issue-cli health lint`**, check `departments`: warns about log lines it skipped, outputs of a department `projects.yaml` no longer has, a file or path that is gone, and an output in review for more than 14 days.

## Not yet

- Putting the role prompt into the dispatch briefing and the department lead's start: factory/department-agents-lead-and-briefing-per-department.
- Connector sources, goal trends over time, and setting goals from the page.
- Publishing an approved output through a connector (factory/department-connectors), outputs from recurring jobs beyond `--job` (factory/department-recurring-jobs), and editing or removing outputs from the page.
