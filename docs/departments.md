---
title: "Departments: Role and Goals"
order: 9
---

# Departments: role and goals

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

## Not yet

- Putting the role prompt into the dispatch briefing and the department lead's start: factory/department-agents-lead-and-briefing-per-department.
- Connector sources, goal trends over time, and setting goals from the page.
