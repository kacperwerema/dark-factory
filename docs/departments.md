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
- **Project map** (`/`, the lot of each project): the bar under the lot is HQ's first goal, else the first goal of Product or a department (`Goal: Marketing · Leads`), with value / target. A goal with no value yet reads "no value yet" and its tooltip gives the `goal set` command. A project without goals keeps the open epic furthest along.
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

## Templates

A **department template** is a ready department: a workflow file, a role prompt and a goals skeleton, bundled with issue-cli (`cmd/issue-cli/templates/departments/<name>/`) like the workflow templates. **Marketing** (SEO and content, on the `content` workflow) is the first. Product is built in: it is the `development` workflow every project runs.

```
issue-cli department list                    # HQ, Product and the templates, installed or not
issue-cli department add marketing --dry-run # the plan, nothing written
issue-cli department add marketing           # install it
issue-cli department add marketing --name SEO
issue-cli department remove seo
```

`department add <template>`:

- writes `workflows/<key>.yaml` (the template's workflow template, as `workflow init` writes it), `departments/<key>/role.md` and each job's brief as `departments/<key>/jobs/<id>.md`, relative to the project root. `<key>` is the department's name in lower case;
- adds the department to the project in projects.yaml, under `departments:` (creating the key when the project has none): name, colour, role, mission, systems, `prompt:`, `workflow:`, the goals, each manual with no value yet, and the template's [recurring jobs](#recurring-jobs). Their standing start is off: a due run waits in Needs you until you start it or turn it on;
- ends with the next steps: `department show <key>`, `create --department <key>`, `lead spawn --department <key>`.

What it never does:

- **overwrite a file.** One that is already there with the template's content is reported as already there; one that differs is **kept** and reported as your edited file. Only the projects.yaml entry is added;
- **change the rest of projects.yaml.** The entry is spliced in as text, so comments, quoting and every other project stay byte for byte. The result must load and contain the department, else the file is left as it was. A symlinked projects.yaml is edited through the link;
- **install over a department you have**, or as `hq` or `product`. Pick another name with `--name`;
- run from a worker session: it edits the owner's file. A lead runs it with the owner's words; `--dry-run` works for anyone.

`department remove <name>` takes the entry out of projects.yaml and names the files it left in place. It refuses while the department has issues that haven't finished its workflow, listing each with the `set-meta … --key department` command that moves it.

The Updates page (`/p/<project>/updates`) has a **Departments you can add** section: each template the project hasn't installed, what it brings and its `department add` command. It isn't counted in the sidebar's Updates number, which counts workflow features.

**Adding a template**: a folder `cmd/issue-cli/templates/departments/<name>/` with `department.yaml` (`name`, `description`, `color`, `role`, `mission`, `systems`, `workflow`: a bundled workflow template, `goals`, `jobs`: each with its `brief:` a file in the folder, `jobs/<id>.md`) and `role.md`. The file is read strictly: an unknown key, a job whose schedule doesn't parse or a missing brief is an error. Marketing ships `weekly-seo-pass` (Mondays 07:00). Templates don't carry connectors yet: add them by hand under the installed department ([Connectors](#connectors)).

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
        overlap: false       # optional: true starts a new run while the last one is open
```

A job's key is `<department>/<id>` (`marketing/weekly-seo-pass`).

### A run is an issue

`issue-cli job run` creates the run as `<System>/<id>-<period>` (the department's first system, else its name), for example `SEO/weekly-seo-pass-2026-w44.md`:

- **Fields:** `department:`, `job: marketing/weekly-seo-pass`, `run: 2026-W44`, the label `job`, the job's `priority:`, and a `slug:` pin.
- **Status:** the first status of the department's workflow. On the `content` template that is `brief`.
- **Body:** the brief file, with `{{period}}`, `{{date}}` and `{{last_run}}` filled in, then a `## Run` line naming the job and the previous run.

Its worker is dispatched like any other, with `## Your department` in its briefing. Everything the engine already keeps for an issue is the run's record: the gates on its path, the Timeline, the token estimate in its stats sidecar, and the outputs it puts on the shelf (`output add --issue`). On `content`, nothing goes out before `owner review → publish`. Under YOLO, that gate waits in YOLO's after-the-fact review instead.

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
| `sends` | The connector can publish, post or spend. It is used only for an output the owner approved on the shelf, then `output publish` |
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

## Not yet

- Connector sources, goal trends over time, and setting goals from the page.
- Publishing an approved output through a connector automatically, connectors in department templates, and editing or removing outputs from the page.
- A job's own `connectors:` list (a job runs as a department worker, so it already gets the department's connectors). Cost caps per run, editing schedules from the page, and jobs on HQ or Product are not there yet.
