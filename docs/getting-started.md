---
title: "Getting Started"
order: 1
---

## From zero to a running factory

This page takes you from a fresh machine to a board with a project, a first issue and a worker on it. Every step was walked on a clean account (empty home folder, no config) and is re-run by a test (`TestNewcomerWalkthrough`), so if one breaks for you, that's a bug ([When a step fails](#when-a-step-fails)).

**You need:** Linux or macOS, `git` with your name and email set (`git config --global user.name …`), `tmux`, and the `claude` or `codex` CLI, signed in, on your `PATH`. To build from a clone you also need Go 1.25 or newer. Nothing else: the tools named `mem-guard`, `rtk` and `i3` in other pages are optional (see [Optional tools](#optional-tools)).

**1. Install the two binaries.** Pick one:

```bash
# a release, into ~/.local/bin
curl -fsSL https://raw.githubusercontent.com/michal-franc/ai-workflow-engine/main/install.sh | bash

# or from a clone (or your fork)
git clone https://github.com/<you>/ai-workflow-engine && cd ai-workflow-engine
GOBIN="$HOME/.local/bin" make install
```

Both put `issue-cli` and `issue-viewer` in `~/.local/bin`; make sure it is on your `PATH`. Plain `make install` uses `go install`, which writes to `$(go env GOPATH)/bin` (usually `~/go/bin`), often not on `PATH`. Check with `issue-cli version`.

**2. Make a project.**

```bash
issue-cli project init ~/src/shop --name "Shop" --config ~/.config/ai-workflow-engine/projects.yaml
cd ~/src/shop && git init && git add -A && git commit -m "Start the shop"
```

`project init` creates `issues/`, `docs/` and `workflow.yaml` (the `development` template) and writes the project into that `projects.yaml`. Use your own repo's folder instead of `~/src/shop` to run the factory on existing code; it never overwrites a file.

**3. Open the board.**

```bash
issue-viewer -config ~/.config/ai-workflow-engine/projects.yaml
```

Open <http://localhost:8080> and click the Shop lot. Leave the viewer running. While it runs, `issue-cli --project shop …` works from any folder.

**4. Write a first issue.**

```bash
issue-cli --project shop create --title "Add a dark mode toggle" --system UI
```

It appears on the board in `idea`, its first status.

**5. Start a worker on it.**

```bash
issue-cli --project shop dispatch ui/add-a-dark-mode-toggle --quote "try the dark mode toggle"
tmux attach -t agent-ui-add-a-dark-mode-toggle
```

The worker is `claude` (or `codex`) in a tmux session, given a briefing built from the workflow. Nothing opens on screen unless you set `terminal:` in `projects.yaml` ([Terminal Configuration](agent-dispatch.md#terminal-configuration)); the dispatch prints the attach command. The worker walks the issue status by status and stops at the first gate. Approve it on the issue's page or in **Needs you**: the viewer tells the worker in its session, and it carries on.

**6. Talk to the CEO.** Instead of starting workers yourself, start the project lead: **Start CEO** on the company page, or `issue-cli --project shop lead spawn`. Chat with it at `/p/shop/lead`: it grooms ideas into issues, dispatches workers and relays their questions. It records an approval only after you said yes in the chat.

**7. Add a department.** `issue-cli --project shop department list` shows the templates; `issue-cli --project shop department add marketing` (or the **New department** tile on the company page) adds Marketing, with its own workflow, role prompt, goals and a weekly job. It writes files into the project and an entry into `projects.yaml`, and never overwrites one.

That's a running factory. Commit `issues/`, `departments/` and `workflows/` with your code if you want them in git history; the tools' own state (`.agent-logs/`, `.health/`, `.ci/`) ignores itself.

### Optional tools

None of these is needed. Each one adds something:

| Tool | What it adds | Without it |
|:--|:--|:--|
| `mem-guard` | A memory check before work that can start agents unattended. Your own small script on `PATH`: `mem-guard check` exits non-zero (or prints `BLOCKED`) when memory is too full to start more, and `mem-guard used` prints the percentage used, e.g. `42%`. | Dispatch, the CEO and everything above work. [Auto-start](agent-dispatch.md#auto-start) and scheduled job runs refuse to start, `issue-cli bench run` warns and starts anyway, and [shared memory](memory.md) search uses keywords only. |
| `rtk` | Shorter command output for agents. issue-cli only notices it to warn workers that `rtk` shortens `git log`. | Nothing changes. |
| i3 + alacritty | The default window for a worker when `terminal:` is unset. | Headless: the dispatch prints the `tmux attach` command. Set `terminal:` to open a window ([examples](agent-dispatch.md#examples)). |

### When a step fails

- **The project or the config isn't found**: `issue-cli projects` prints which `projects.yaml` it read and why. Start the viewer with `-config`, or pass `--config <path>` ([How issue-cli finds the config](#how-issue-cli-finds-the-config)).
- **`workers can't dispatch`**, **`workers never add departments`**: the command ran inside an agent's session (its `ISSUE_VIEWER_ISSUE_SLUG` is set). Run it from your own shell.
- **Port 8080 in use**: `issue-viewer -config … -port 8081`.
- Anything else: open an issue on [the repo](https://github.com/michal-franc/ai-workflow-engine/issues) with the command and its output.

## Install

One line for Linux and macOS. It detects your OS and architecture, downloads the latest release and puts `issue-viewer` (the board) and `issue-cli` (the agent CLI) into `~/.local/bin`:

```bash
curl -fsSL https://raw.githubusercontent.com/michal-franc/ai-workflow-engine/main/install.sh | bash
```

Override the target with `INSTALL_DIR=...`, or pin a release with `VERSION=v0.32.0`.

Or download an archive from the [latest release](https://github.com/michal-franc/ai-workflow-engine/releases/latest). Each one holds both binaries; extract them onto your `$PATH`.

| Platform            | Archive                                       |
|:--------------------|:----------------------------------------------|
| Linux x86_64        | `issue-viewer_<version>_linux_amd64.tar.gz`   |
| Linux arm64         | `issue-viewer_<version>_linux_arm64.tar.gz`   |
| macOS Intel         | `issue-viewer_<version>_darwin_amd64.tar.gz`  |
| macOS Apple Silicon | `issue-viewer_<version>_darwin_arm64.tar.gz`  |

Releases ship Linux and macOS only. Agent dispatch also needs `tmux`, `git` and the `claude` or `codex` CLI.

### Build from source

Needs Go 1.25 or newer.

```bash
make build        # issue-viewer
make build-cli    # issue-cli
make install      # both, via go install
make validate     # go vet, tests and the issue-cli coverage gate
```

`make install` runs `go install`, which writes to `$GOBIN`, else `$(go env GOPATH)/bin`. Put that folder on your `PATH`, or install somewhere already on it: `GOBIN="$HOME/.local/bin" make install`.

### Privacy: usage stats

`issue-cli` keeps a local, names-only log of its calls (`.agent-logs/telemetry.jsonl` in each project). Once a day it also sends an **anonymous summary** of that log:
- command and flag names with counts
- error classes and durations
- the bundled workflow template and feature ids in use
- the number of projects, issues and departments
- the version and OS
- a random install id

It never sends project names, paths, issue text or flag values. This is **on by default**. The first call says so once on stderr. Until a collector URL is set (none is built in yet), nothing is sent.

- Turn it off: `issue-cli telemetry share off`, or `ISSUE_CLI_TELEMETRY=off` (which also stops the local log).
- See what was sent: `issue-cli telemetry sent`.
- Every field: [CLI Usage Telemetry](CLI/telemetry.md#sharing-usage).

## Try the demo

From a checkout of this repo:

```bash
make demo            # or make demo PORT=8081 while your own viewer has 8080
# http://localhost:8080
```

The demo is Acme Shop, the sample project in the README's pictures: an HQ with business goals, Product on the `development` workflow, Marketing on its own `content` workflow, an outputs shelf, and a few gates waiting on you. Its files live in `demo/`. `make readme-shots` stages a copy of it with a CEO and three workers on a private tmux server and retakes the README's screenshots.

## Bootstrap a project

One command makes a new project the board shows:

```bash
issue-cli project init ~/src/shop --name "Shop" --config ~/.config/ai-workflow-engine/projects.yaml
issue-viewer -config ~/.config/ai-workflow-engine/projects.yaml
```

It creates the folder with `issues/`, `docs/` and a `workflow.yaml` from the `development` template, and adds the project to the projects.yaml you name with `--config` (a new file if there is none; every other line of an existing one stays as it was). Leave `--config` out and it prints the entry for you to add instead. It never overwrites a file, so running it again only reports what is already there; `--dry-run` lists every change first. Then it prints the next steps: open `/p/shop/company`, where the New department tile adds a department from a template, start the CEO with `issue-cli --project shop lead spawn`, and create the first issue. See [CLI Overview → project init](CLI/overview.md#project-init).

To set up only the workflow in the current directory, without a projects.yaml entry:

```bash
issue-cli init --template development
```

`init` is an alias for `issue-cli workflow init`. Pick `development` for software delivery, `review` for triage queues, or `writing` for long-form content. Pass `--force` to overwrite an existing `workflow.yaml`. Run it without `--template` in a terminal for an interactive picker. See [CLI Overview → workflow init](CLI/overview.md#workflow-init) for the full reference.

## Create your first issue

```bash
issue-cli create --title "My first issue" --system UI
```

This writes `issues/UI/my-first-issue.md` in the workflow's first status. You can also write the file by hand:

```markdown
---
title: "My first issue"
status: "idea"
system: "UI"
---

Description of the issue.
```

`status` must be one of the statuses in your `workflow.yaml`. Every field is described in [Issue File Format](issue-format.md).

## Run the board

### One project, many projects

With a `projects.yaml` (recommended), one server hosts any number of projects, each with its own issues, docs and workflow, and each can set how agents are dispatched:

```yaml
projects:
  - name: "My Project"
    slug: "my-project"
    issues: "./issues"
    docs: "./docs"
    workdir: "."
    terminal: "none"
  - name: "Renderer"
    slug: "renderer"
    issues: "../renderer/issues"
    docs: "../renderer/docs"
```

```bash
issue-viewer -config projects.yaml
```

`projects.yaml.example` in this repo lists every key. The dispatch keys (`terminal`, `lead_terminal`, `tmux_session`, `agent_models`) are explained in [Agent Dispatch](agent-dispatch.md). These top-level keys set machine-wide defaults for projects that leave them unset:

- `terminal:`, e.g. `none`
- `lead_terminal:` and `lead_window_session:`, e.g. `tmux-window` and `main` to get each project lead as a tab in your own tmux session
- `tmux_session_per_project: true`, which gives each project its own tmux session for its lead and workers

`allowed_hosts:` lists Host names the viewer answers to besides loopback; see [Server flags](#server-flags).

`telemetry_data:` points the viewer's `/telemetry` page at a telemetry collector's data folder (`cmd/telemetry-collector -data <dir>`). It is only for whoever runs a collector; see [Sharing usage](CLI/telemetry.md#the-owners-view).

`viewer_url:` is the address the viewer is reached at, e.g. `viewer_url: "https://viewer.lan"`. Every link the viewer and issue-cli print uses it, and `issue-cli status` calls it. That covers approval links, `show`'s Timeline, and the issue-approve buttons in an agent's session. Without it, issue-cli uses the running viewer's own address from `viewer.json`, then `http://localhost:8080`. Its host is added to `allowed_hosts` by itself. It doesn't change where the viewer listens: that is still `-host` / `-port`. The value must be an `http://` or `https://` URL with a host; a path prefix behind a reverse proxy is kept.

`engine_project: <slug>` names the project that `issue-cli report-bug` files issue-cli bugs into. Without it, report-bug uses the project whose `repo:` is `michal-franc/ai-workflow-engine`; see [`report-bug`](CLI/overview.md#report-bug).

With one terminal and tmux, as on a Mac, see [One terminal, a session per project](agent-dispatch.md#one-terminal-a-session-per-project).

#### How issue-cli finds the config

issue-cli needs the same `projects.yaml` as the viewer, from whatever folder it runs in. Without `--config` it looks, in order:

1. `--config <path>`
2. `$ISSUE_VIEWER_CONFIG` (the viewer sets it for the agents it dispatches)
3. `./projects.yaml`, only if it exists
4. the running viewer's config: at start, `issue-viewer -config …` writes `~/.local/state/ai-workflow-engine/viewer.json` (`$XDG_STATE_HOME`) with the absolute config path, URL, pid and start time. The entry is used while that config file exists and that pid is alive. A sandbox viewer doesn't write the file: one whose config is in a temp folder (`$TMPDIR`, `/tmp`, `/var/folders`) while the state dir isn't, or one started while another live viewer holds the file for a different config. It says so on stderr. `-record-viewer` writes it anyway, `-record-viewer=false` never
5. `~/.config/ai-workflow-engine/projects.yaml` (`$XDG_CONFIG_HOME`)

So once the viewer has started with `-config` on this machine, `issue-cli --project <slug> …` and `issue-cli report-bug` work from any folder with no env var. `issue-cli projects` prints the chosen config and why on its first line, and `issue-cli --debug <command>` prints it on stderr. When nothing is found, the error lists every place checked and the fix: `export ISSUE_VIEWER_CONFIG=/path/to/projects.yaml`, or start the viewer. When `--project` names a project the config doesn't list, the error names the config file and where it came from (`Config from: …`).

For a quick look at a single folder without a config file:

```bash
issue-viewer -dir ./issues -docs ./docs
```

This mode can't set dispatch options such as `terminal`, so use a `projects.yaml` before dispatching agents.

### Server flags

| Flag       | Default     | Description                               |
|:-----------|:------------|:------------------------------------------|
| `-config`  | none        | Path to `projects.yaml` (multi-project); recorded in `viewer.json` for issue-cli |
| `-dir`     | `./issues`  | Issues directory (single-project mode)    |
| `-docs`    | `./docs`    | Docs directory (single-project mode)      |
| `-host`    | `127.0.0.1` | Interface to listen on                    |
| `-port`    | `8080`      | HTTP port                                 |
| `-record-viewer` | see above | Write `viewer.json` even for a sandbox viewer (`=false`: never write it) |
| `-version` | `false`     | Print the version and exit                |

The server listens on loopback only. Its POST endpoints start agents, tmux sessions and editors on your machine, so binding to another interface (`-host 0.0.0.0`) lets anyone who can reach the port drive them; it is an explicit opt-in and the server prints a warning at startup. Writes from another site in your browser are refused either way (see [API: Design Considerations](API/overview.md#design-considerations)).

The server also answers only to loopback names: a request whose `Host` is not `localhost`, `127.0.0.1`, `[::1]` or `*.localhost` gets `421 Misdirected Request`, which stops a DNS-rebinding page from reaching it. With a non-loopback `-host`, the bind address is accepted too (every interface address for `-host 0.0.0.0`), and the warning lists them. To reach the viewer by another name, such as a LAN or VPN hostname, list it in `projects.yaml`:

```yaml
allowed_hosts:
  - viewer.lan
```

A name other than loopback, or any request that came through a proxy or tunnel, is **public**. The viewer answers it only when `identity:` is configured in `projects.yaml` and the request carries a Cloudflare Access token for one of the owners. Without `identity:`, the viewer refuses every public request with 403, LAN and VPN names included. Before you point a tunnel at the viewer, work through the go-public checklist in [API: Security](API/security.md).

### Stopping the server

Ctrl-C (or SIGTERM) stops the server gracefully. It stops accepting connections, waits up to 10 seconds for requests and background work to finish (a GitHub close, an open edit-in-nvim, the CI watcher), and exits with status 0. If anything is still running after 10 seconds, it prints `shutdown: still running after 10s: …` and exits anyway. Press Ctrl-C a second time to exit immediately.

An edit-in-nvim session that is still open when the server stops is closed. Its changes are discarded, and the issue gets a comment saying so. Agent tmux sessions and local CI runs keep running; they don't belong to the server process.

## Find your way around

Open `http://localhost:8080`. This is the Workshop Board:

- **The map** (`/`) shows each project as a lot. The **!** badge counts what waits on you; the panel on the right lists it.
- **The company** (`/p/<project>/company`): click a lot. HQ is the CEO's room with the business goals, Product is the software factory, and each department from `projects.yaml` has a room with its goals and outputs.
- **The hall** (`/p/<project>/hall`): **Enter the hall** draws `workflow.yaml` as a line of stations (statuses) and gates (your stamps). Click a station or gate to see what is inside; **Edit** on a station changes it, with a preview before anything is written.
- **The station** (`/p/<project>/issue/<slug>`): one issue, with its belt through the statuses, your next move, and the Timeline and Evidence tabs.
- **The CEO chat** (`/p/<project>/lead`): talk to the project lead. **Start CEO** on the company page starts it.
- **Needs you** (`/p/<project>/needs-you`, `/needs-you` across projects) and **the Report** (`/p/<project>/status`).

The tabs in the hall's header lead to the Report, Health, Systems, Decisions, Retro, Docs and Updates.

## Docs pages

The Docs tab renders the markdown files in your docs directory. Frontmatter is optional:

```markdown
---
title: "Page Title"
order: 1
---

Content here.
```

`title` defaults to the file name in title case. Pages sort by `order` (lower first), then alphabetically. Subdirectories become sections in the sidebar.
