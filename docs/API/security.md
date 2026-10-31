---
title: "Security"
order: 2
---

The viewer drives a machine. It starts agents that run shell commands, opens tmux windows, approves gates and writes repositories. This page covers four things:

- what it protects, and from whom;
- every route, and who may call it;
- the barriers in the code that keep these rules, and the tests that keep the barriers;
- the checklist that must be green before anything on the internet points at the viewer.

The review behind it ran on 2026-10-31, as issue `api/security-review-and-pentest-of-the-viewer-before-it-is-exposed-to-the-web`. That issue holds the full findings and their repros. This page describes them in general terms only.

## Threat model

**Assets**

| Asset | Why it matters |
|---|---|
| The machine | Agents run as the owner's user, with the owner's shell, SSH keys and git credentials |
| Repositories and issue files | Prompts, workflow files and `CLAUDE.md` steer every agent. A written file can become a command later |
| Secrets and connectors | Connector secrets (`env:` / `keychain:`) reach agents' children. `gh` is logged in |
| Agents that run commands | A paste into a session, a dispatch prompt or a station prompt is an instruction to a shell-capable agent |
| The owner's approvals | Gates, YOLO windows, owner boxes, shelf reviews and workflow edits are the owner's decisions |
| The owner's attention | The CEO chat, chat images and tmux panes may hold anything |

**Attackers**

1. **Anonymous internet.** This applies once a tunnel or `-host` exposes the viewer. Such an attacker can send any request with any header.
2. **A signed-in user who is not the owner.** For example, someone else in the Cloudflare Access team, or a GitHub login the Access policy lets in.
3. **A malicious page in the owner's browser.** It can send cross-site requests, use DNS rebinding, frame the viewer, or open a sandboxed iframe whose origin is `null`.
4. **Poisoned content.** This means issue text, comments, outputs, docs, workflow text or sidecars written by an agent, by a GitHub import, or by a merged branch. It reaches the pages (XSS) and the shell (frontmatter in commands).
5. **A local agent.** It runs as the same user, so on loopback the viewer can't technically stop it. The owner-only routes are policy for agents, not a barrier (see Known gaps).

## Barriers

Every request passes these layers, from the outside in (`Routes` in `routes.go`):

1. **`securityHeaders`** (`headers.go`) sets the response headers and caps the request body:
   - `X-Content-Type-Options: nosniff`
   - `X-Frame-Options: SAMEORIGIN`
   - `Referrer-Policy: same-origin`
   - `Cross-Origin-Opener-Policy: same-origin`
   - a base CSP: `frame-ancestors 'self'; base-uri 'self'; object-src 'none'; form-action 'self'`
   - a body cap of 4 MiB, or 12 MiB for image uploads

   Routes that serve untrusted files set a stricter CSP of their own: `/files/`, `/attachments/`, the shelf, `/page-raw/` and CI logs.
2. **`hostAllowed`** (`origin.go`) answers `421` to any `Host` that is not loopback, not in `allowed_hosts:`, and not the bind address. This stops DNS rebinding.
3. **`identityGate`** (`identity.go`) decides whether a request is **public**. A request is public when:
   - its Host is not a loopback name, or
   - it carries any proxy header (`Cf-Ray`, `Cf-Connecting-Ip`, `X-Forwarded-*`, `Forwarded`, `X-Real-Ip`, …).

   A public request is answered only if both of these hold:
   - `identity:` is configured;
   - the request's `Cf-Access-Jwt-Assertion` token verifies. That means: RS256, signed by a key from the team's certs URL, issuer `https://<team_domain>`, audience the Access application's AUD tag, not expired, and an email in `owners:`.

   Everything else gets `403`. **Without `identity:` the viewer refuses every public request, so it fails closed.** If `identity:` is present but incomplete, the viewer refuses to start.
4. **`sameOriginOnly`** (`origin.go`) answers `403` to a non-GET request a browser sends for another site. That covers `Sec-Fetch-Site: cross-site`, a foreign `Origin`, and `Origin: null`.

Inside the handlers:

- **Issue create.** `system:` must be one folder name. Create never overwrites an existing issue. Every frontmatter value is written YAML-escaped (`tracker.CreateIssueFileOpts`).
- **Agent from a request.** The agent must be a plain command name (`agentrun.ValidAgentType`), or the request gets `400`. The launch line single-quotes the agent and the prompt path. Session names keep only `[a-z0-9_-]` (`tracker.AgentSessionName`).
- **Commands typed into shells.** Paths and names typed into a shell go through `agentrun.ShellWord`: nvim edit commands, i3 terminals and tmux `wait-for`.
- **`command_succeeds` validators.** Template values (`{{system}}`, `{{repo}}`, …) are shell-quoted when they hold anything a shell acts on.
- **Git refs from stats sidecars.** Only a hex SHA is used, and a ref is never passed as an option.
- **Approvals.** The status must be a status of the issue's workflow. `lead/answer` text goes after `--`.
- **Issue bodies.** Markdown renders without raw HTML, and `javascript:` links are blanked. Only one plain HTML comment survives as is: the `<!-- data -->` marker, or notes to agents.
- **Uploads.** An upload is saved under the extension of its sniffed image type (png, jpg, gif or webp). `/attachments/` serves only those hash-named, regular files, with `nosniff` and a sandbox CSP. It never serves a listing or a symlink.
- **`/page-raw/` CORS.** The `Origin: null` CORS grant needs a Referer on this viewer. Any site's sandboxed iframe also sends `Origin: null`.
- **Owner-only actions.** On loopback, `departments/add`, `owner-checks/tick` and owner-box ticks through the body editor require `fromOwnerBrowser`, which is a header check (see Known gaps). On a public name, every route requires the verified owner identity.

### Keeping them

- **Go tests:**
  - `security_test.go`: one test per finding.
  - `identity_test.go`: fail closed, token verification, the key refetch limit, headers and the body cap.
  - `origin_test.go`, plus tests in `internal/tracker/validations`, `internal/timeline` and `internal/tracker`.

  They run in `make validate`.
- **`make security-check`** runs `scripts/security-check.sh`, then `govulncheck`. The script builds the viewer, starts it on loopback with its own HOME, XDG dirs and tmux dir, attacks it as if it were public, and stops everything. Run it before a release, and after any change to `routes.go`, `origin.go`, `identity.go`, `headers.go`, the upload and attachment code, or anything that runs a command.
- **Rules for new code:**
  - A new route must fit the inventory below.
  - Never put a request value into a shell string without `ShellWord`.
  - Never put request or file data into `innerHTML` without escaping it.
  - Never serve a user-written file without `nosniff` and a CSP of its own.

## Going public: the barrier checklist

All of these must be true before a tunnel, `-host` or port forward points at the viewer. The viewer enforces the items marked **enforced**.

- [ ] **Auth in front.** Cloudflare Access, or an equivalent, protects the public name. Its policy allows only the owner's identity (for example, one GitHub login or email). The session duration is short (24h or less).
- [ ] **Identity check in the viewer** (**enforced**). Add `identity:` to `projects.yaml`, with the team domain, the application's AUD tag and the owner emails. Without it, every public request gets `403`:

  ```yaml
  allowed_hosts:
    - factory.example.com
  identity:
    cloudflare_access:
      team_domain: yourteam.cloudflareaccess.com
      audience: "<Access application AUD tag>"
    owners:
      - owner@example.com
  ```

- [ ] **Fails closed** (**enforced**, tested). These all get `403`: a public Host or proxied request without a valid owner token, a token for another application or team, an expired token, a non-owner, and a forged or unsigned token.
- [ ] **Owner-only actions need the verified identity** (**enforced** on public names). Every route on a public name runs as the verified owner. Nobody else gets in, not even read-only.
- [ ] **No command-running route without identity** (**enforced**). Dispatch, lead spawn, lead send, actions, check scripts, playtest, jobs, health review, updates install and connector checks are all behind the identity gate on a public name.
- [ ] **The tunnel connects to loopback.** cloudflared's origin is `http://127.0.0.1:<port>`, and the viewer keeps `-host 127.0.0.1`. Never bind `0.0.0.0` for a tunnel. A rewritten `Host` (`httpHostHeader: localhost`) is still treated as public because of the proxy headers, but don't rely on it.
- [ ] **Headers** (**enforced**). nosniff, frame-ancestors / X-Frame-Options, Referrer-Policy, and the base CSP are on every answer.
- [ ] **Limits.** Request bodies are capped (**enforced**). Server timeouts are set: ReadHeader 5s, Read 30s, Write 60s, Idle 120s (**enforced**). Rate limiting is Cloudflare's job: set a rate-limit rule on the public name. The viewer has none.
- [ ] **`make security-check` green** on the build that will be exposed, `govulncheck` clean.
- [ ] **The high findings' follow-up issues are done or consciously accepted by the owner** (see Known gaps).
- [ ] **The owner has read the findings** in the review issue and this checklist.

## Route inventory

Here is who may call each route class:

- **Local** means loopback with no proxy header: the owner's browser, issue-cli, or any agent on this machine.
- **Public** means through a tunnel, and only for the verified owner.

| Class | Meaning | Local | Public |
|---|---|---|---|
| R | Reads a page or JSON | anyone on the machine | verified owner |
| W | Writes issue or board files | anyone on the machine | verified owner |
| O | An owner decision | the owner (policy; see Known gaps) | verified owner |
| X | Starts a process (agent, tmux, nvim, script, `gh`, git commit) | anyone on the machine | verified owner |

Every route is under `/p/<project>/` unless it starts with `/`.

| Route | Method | Class | What it does |
|---|---|---|---|
| `/`, `/stats`, `/updates`, `/leads`, `/ceos`, `/needs-you`, `/telemetry`, `/project-for?cwd=` | GET | R | Map, cross-project pages, the telemetry collector's data, project lookup by folder |
| `/static/…` | GET | R | Embedded assets |
| `/p/<project>/` (issues), `hall`, `hall/ci`, `hall/station/<s>`, `hall/gate/<s>`, `company`, `hq/report`, `hq/report.md`, `needs-you`, `needs-you.json`, `owner-checks`, `connectors.json`, `connections`, `connections.json`, `reports/<g>/<w>`, `shelf`, `board`, `graph`, `workboard`, `canvas`, `status`, `status.md`, `status.json`, `release.json`, `pages/<n>`, `github`, `stats`, `health`, `agents`, `agents.json`, `agents/detail`, `lead`, `ceo`, `lead.json`, `retros`, `decisions`, `updates`, `departments/new`, `metrics`, `metrics.json`, `balance`, `systems[/…]`, `hash`, `issues.json`, `docs[/…]`, `yolo.json` | GET | R | Pages and JSON |
| `issue/<slug>`, `…/comments`, `…/approvals.json`, `…/transition?to=` | GET | R | Issue page, comments, approvals, transition preview. The preview runs `command_succeeds` validators, which are config, with quoted values |
| `…/dispatch-prompt`, `…/playtest` | GET | R | Prompt text, playtest state |
| `shelf/read/<id>` | GET | R | Output reader: markdown rendered without raw HTML |
| `shelf/file/…`, `ci/log/<name>`, `page-raw/…`, `files/…`, `attachments/<hash>.<img>` | GET | R | Files: confined to their folder, with their own CSP |
| `issues/create`, `issue/<slug>` | POST | W | Create or update an issue |
| `…/comments`, `…/comments/toggle`, `…/comments/delete` | POST | W | Comments |
| `…/data`, `…/data/<id>/status\|tier\|comment` | POST | W | Data sidecar |
| `…/data/<id>` | DELETE | W | Data sidecar |
| `…/checkbox`, `…/type`, `…/delete`, `upload`, `lead/image` | POST | W | Boxes, type, delete, image uploads |
| `shelf/comment`, `shelf/comment/resolve` | POST | W | Comments on an output |
| `retros/retro/<n>/status`, `retros/bug/<n>/status`, `updates/decline`, `updates/reopen` | POST | W | Retro, bug and update records |
| `…/approve`, `needs-you/approve` | POST | O | Approve gates (and nudge the agent) |
| `…/send-back`, `…/yolo-review` | POST | O | Send back for fixes, YOLO review |
| `yolo/on\|extend\|off`, `autostart/pause\|resume\|off` | POST | O | YOLO window, auto-start |
| `owner-checks/tick` | POST | O | Owner boxes (`fromOwnerBrowser`) |
| `departments/add` | POST | O | Department from a template (`fromOwnerBrowser`) |
| `shelf/review`, `decisions/<slug>/<D-n>/confirm` | POST | O | Output review, decision confirm |
| `hall/station/<s>/edit/preview`, `…/edit/save` | POST | O | Edit a station of `workflow.yaml` (commits on main) |
| `health/recommendation/<id>/file\|dismiss` | POST | O | File or dismiss a health recommendation |
| `…/dispatch`, `…/dispatch/reprompt`, `retros/review`, `lead/spawn` | POST | X | Start an agent: tmux plus a login shell; agent name validated |
| `lead/send`, `lead/key`, `lead/answer` | POST | X | Paste into or press keys in the CEO's session |
| `agents/open` | POST | X | Open a terminal on an agent |
| `…/action/<id>`, `action/<id>` | POST | X | Custom actions from `workflow.yaml` |
| `…/check-script`, `…/playtest` | POST | X | Run guided checks or a playtest in tmux |
| `…/edit-in-nvim`, `docs/<p>/edit-in-nvim` | POST | X | nvim in tmux |
| `jobs/<dept>/<job>/run`, `health/review`, `updates/install` | POST | X | Start an agent for a job, a health review or an install |
| `connectors/check` | POST | X | Run a department's connector checks |
| `github/fetch`, `github/import` | POST | X | `gh` calls, issue import |

## Known gaps (follow-up issues)

- **The owner/agent boundary on loopback.** On loopback, an agent with `curl` can call the O routes, and `fromOwnerBrowser` is a header any client can send. This needs an owner credential that agents can't read, such as a session cookie set only through a flow the owner runs. Until then, "only the owner approves" is a rule agents follow, not a barrier. Filed as a high bugfix issue.
- **A script CSP.** Templates use inline scripts and `on…=` handlers, so the CSP can't restrict scripts yet. The next step is moving to nonces and `script-src 'self' 'nonce-…'`. Project pages' `/page-raw/` CSP has no `connect-src`, and the frame posts `status.json` to `'*'`. Filed with the hardening follow-ups.
- **cmd/telemetry-collector** is a separate binary and was not part of this review. If it is ever exposed to the internet, it needs its own review.
- **Low findings.** These are listed in the review issue:
  - tmux targets without `=` exact match;
  - `shelf/file/` follows a symlink placed inside the shelf;
  - `page-raw` with a page folder of `.`;
  - error messages with absolute paths.
