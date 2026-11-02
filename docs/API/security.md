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
5. **A local agent.** It runs as the same user and can reach every loopback route. The O routes need the owner's sign-in (see Owner sign-in), which an agent can't get by sending a request. An agent that sets out to steal it on purpose is a known gap.

## Barriers

Every request passes these layers, from the outside in (`Routes` in `routes.go`; the viewer's files are in `internal/viewer/`):

1. **`securityHeaders`** (`headers.go`) sets the response headers and caps the request body:
   - `X-Content-Type-Options: nosniff`
   - `X-Frame-Options: SAMEORIGIN`
   - `Referrer-Policy: same-origin`
   - `Cross-Origin-Opener-Policy: same-origin`
   - a CSP: `frame-ancestors 'self'; base-uri 'self'; object-src 'none'; form-action 'self'; script-src 'self' 'nonce-<per request>'`. Only `/static` scripts and the inline `<script>` blocks the templates wrote run. An injected `<script>` or `on…=` attribute does not (`csp.go`).
   - error bodies (plain text or JSON, status 400 and up) show the home folder as `~` (`csp.go`, `errorPathScrub`)
   - a body cap of 4 MiB, or 12 MiB for image uploads

   Routes that serve untrusted files set a stricter CSP of their own: `/files/`, `/attachments/`, the shelf, `/page-raw/` and CI logs. The nonce writer leaves those answers untouched.

   How the script CSP works: templates can't see the request, so an inline script is written `<script nonce="{{cspNonce}}">`. That writes a random marker fixed for the process, and `nonceWriter` swaps it for the request's nonce on the way out. Templates use no `on…=` handlers. An element carries `data-click`, `data-change`, `data-input`, `data-keydown` or `data-blur` with a JSON call written by the `onCall` template func (`data-click="{{onCall "fn" .Slug "$this"}}"`). It can also carry `data-stop` (stop propagation) or `data-autosubmit` (submit the form on change). `static/events.js`, loaded in every page head, binds them per element, and a MutationObserver binds elements added later. HTML a script builds as a string uses `dataCall('click', ['fn', arg])`.
2. **`hostAllowed`** (`origin.go`) answers `421` to any `Host` that is not loopback, not in `allowed_hosts:`, and not the bind address. This stops DNS rebinding.
3. **`identityGate`** (`identity.go`) decides whether a request is **public**. A request is public when:
   - its Host is not a loopback name, or
   - it carries any proxy header (`Cf-Ray`, `Cf-Connecting-Ip`, `X-Forwarded-*`, `Forwarded`, `X-Real-Ip`, …).

   A public request is answered only if both of these hold:
   - `identity:` is configured;
   - the request's `Cf-Access-Jwt-Assertion` token verifies. That means: RS256, signed by a key from the team's certs URL, issuer `https://<team_domain>`, audience the Access application's AUD tag, not expired, and an email in `owners:`.

   Everything else gets `403`. **Without `identity:` the viewer refuses every public request, so it fails closed.** If `identity:` is present but incomplete, the viewer refuses to start.
4. **`sameOriginOnly`** (`origin.go`) answers `403` to a non-GET request a browser sends for another site. That covers `Sec-Fetch-Site: cross-site`, a foreign `Origin`, and `Origin: null`.
5. **The owner gate** (`ownerOnlyRoute` and `fromOwner` in `owner.go`, checked in `handleProjectRoutes`) answers `403` (`owner sign-in needed`) to an O route of the inventory unless the request is the owner's: the owner's sign-in cookie on loopback, or the verified identity on a public name. Browser headers (`Sec-Fetch-Site`, `Origin`) don't count, since any client can send them.

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
- **`/page-raw/` reach.** The CSP keeps a project page's fetches, images, fonts and forms on the viewer (`default-src 'self'`, `connect-src 'self'`, `form-action 'none'`). The frame posts the status with target `*`, because the frame's origin is opaque. So it posts only while the frame shows the page the viewer loaded: a load the viewer didn't start stops the posting until the next reload. The route serves files under the page folders only. It never serves a path through a dot-folder or to a dot-file. A page at the project root serves only itself, never the root's other files.
- **Shelf files.** `shelf/file/` resolves symlinks and serves only regular files inside `.outputs/files`, as `/files/` does.
- **tmux targets.** Every tmux command addresses an agent as `=name:` (or `=session:=window` in shared mode), an exact match. A bare name is a prefix match in tmux, so `agent-foo` would reach `agent-foo-bar`.
- **Owner-only actions.** Every O route goes through the owner gate. Owner-box ticks through the body editor (`POST issue/<slug>`, a W route) check the same `fromOwner`. On a public name, every route requires the verified owner identity.

### Owner sign-in

On loopback the owner, issue-cli and every agent reach the same port as the same user. What makes a request the owner's is a session cookie that only a person can get:

- **The cookie.** `iv_owner`: `HttpOnly`, `SameSite=Strict`, `Path=/`, 32 random bytes. The viewer keeps only its SHA-256, in memory. No file holds a session, so none can be read or written to mint one. A restart of the viewer signs the owner out. Sessions last at most 30 days.
- **How the browser gets it.** At `/owner` (the YOU circle in the header), the owner presses **Show me a code**. The viewer makes an 8-digit one-time code and shows it outside HTTP: in a macOS dialog, or on the viewer's own terminal (`-owner-code dialog|terminal|auto`; `auto` is the dialog, else the terminal). The owner types it into the same page, and `POST /owner/pair` sets the cookie. **Sign out** (`POST /owner/signout`) ends the session.
- **The code.** One use, 5 minutes, 5 wrong tries burn it, at most one new code every 30 seconds (so a loop of requests can't flood the owner with dialogs). Only the browser that types it is signed in, so a code an agent asked for is useless to that agent.
- **Why an agent's `curl` can't get it.** No HTTP answer ever carries the code or a session. A cookie handed out on a top-level GET would be no barrier, because `curl` sends any `Sec-Fetch-*` header it likes. The session lives only in the viewer's memory.
- **Agents and issue-cli.** R, W and X routes don't need the cookie. An agent that needs an owner decision asks on its issue trail (`issue-cli comment <slug> "question: …"`). The issue-approve Claude Code mod runs inside an agent's session, so its approve press gets `403`; the owner approves in the browser.
- **Tests and sandboxes.** `owner_test.go` sends one request per O route without the cookie, with forged browser headers, and with a made-up cookie. The playtest runner starts its boards with `-owner-code terminal`, reads the code from its own board's stderr, and signs its browser in. `scripts/security-check.sh` does the same and attacks every O route without the cookie.

### Phone notifications

The viewer sends Web Push to the owner's devices (`push_view.go`, `internal/push`, `internal/webpush`; docs/getting-started.md#phone-notifications). What keeps it safe:

- **Owner only.** Every POST under `/push/` is an O route (`ownerOnlyGlobal`); on a public name the identity gate answers first. `/push` and `/push/key` are R: devices show only a label and a date, and the VAPID public key is public by design.
- **No relay.** A subscription must name a browser push service over https (`webpush.AllowedEndpoint`: exact host or a dot-suffix of Apple's, Google's, Mozilla's and Microsoft's), and the push client follows no redirect. So even the owner's session can't make the viewer POST to an internal address.
- **Capabilities stay put.** An endpoint is a capability URL (anyone holding it can push to the phone), and `p256dh`, `auth` and the VAPID private key are secrets. They live only in `$XDG_STATE_HOME/ai-workflow-engine/push/` (0700, files 0600, written aside and renamed), never on a page, in a log line, in `status.md` or in JSON (`TestPushSecretsStayInTheStateDir`). This is device state, not business data, so it doesn't go to the issues dir or a tracker repo (D-1 of the push issue; docs/departments.md "Business data sits with the work files").
- **Small payloads.** A push carries a title (home folder scrubbed, 120 characters), the project, the kind, a relative link (`/p/…` or `/needs-you`, `push.SafeURL`) and a tag. No row note, comment text or file content. It is end-to-end encrypted (RFC 8291) to the device; the push service sees only the size and the Topic hash.
- **Sandboxes don't send.** Only a viewer that `viewerstate.Claim` lets hold `viewer.json`, and whose config is the one `push/` is bound to, scans, sends or writes there. A sandbox that kept the shared state dir answers 409 and never reads the devices (`TestPushOnlyTheBoundViewerSends`).
- **The app shell.** `/manifest.webmanifest`, `/sw.js`, `/apple-touch-icon.png` and `/static/icons/*` are constant and data-free, but stay behind the identity gate like every path (D-3 of the push issue). The manifest link has `crossorigin="use-credentials"` so the Access cookie goes with it. iOS fetches the touch icon without cookies and falls back to a screenshot icon behind Access. The worker caches nothing, and opens only same-origin paths.

### Keeping them

- **Go tests:**
  - `security_test.go`: one test per finding.
  - `identity_test.go`: fail closed, token verification, the key refetch limit, headers and the body cap.
  - `push_view_test.go`, `internal/push`, `internal/webpush`: owner-only push routes, no secrets on pages or logs, the allow-list and no redirects, sandboxes not sending.
  - `owner_test.go`: every O route refuses a request without the owner's sign-in, the code's limits, the cookie's flags, and the O rows of this page kept in step with the gate.
  - `origin_test.go`, plus tests in `internal/tracker/validations`, `internal/timeline` and `internal/tracker`.

  They run in `make validate`.
- **`make security-check`** runs `scripts/security-check.sh`, then `govulncheck`. The script builds the viewer, starts it on loopback with its own HOME, XDG dirs and tmux dir, attacks it as if it were public, and stops everything. Run it before a release, and after any change to `routes.go`, `origin.go`, `identity.go`, `headers.go`, the upload and attachment code, or anything that runs a command.
- **Rules for new code:**
  - A new route must fit the inventory below.
  - Never put a request value into a shell string without `ShellWord`.
  - Never put request or file data into `innerHTML` without escaping it.
  - Never serve a user-written file without `nosniff` and a CSP of its own.
  - No `on…=` attribute and no inline `<script>` without `nonce="{{cspNonce}}"` in a template or in HTML built in Go. `TestTemplatesHaveNoInlineHandlers` fails on one, and `TestDataCallsNameRealFunctions` checks that every `data-*` call names a function that exists.

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
| O | An owner decision | the owner's signed-in browser (Owner sign-in) | verified owner |
| X | Starts a process (agent, tmux, nvim, script, `gh`, git commit) | anyone on the machine | verified owner |

Every route is under `/p/<project>/` unless it starts with `/`.

| Route | Method | Class | What it does |
|---|---|---|---|
| `/`, `/stats`, `/updates`, `/leads`, `/ceos`, `/needs-you`, `/crew`, `/telemetry`, `/project-for?cwd=` | GET | R | Map, cross-project pages, the telemetry collector's data, project lookup by folder. On a public name `/` answers Needs you across projects (the remote home; `/?map=1` the map): it reads the identity the gate set and changes no barrier |
| `/static/…` | GET | R | Embedded assets |
| `/owner` | GET | R | Owner sign-in page |
| `/push`, `/push/key`, `/manifest.webmanifest`, `/sw.js`, `/apple-touch-icon.png` | GET | R | Phone notifications page (devices by label and date only), the VAPID public key, and the installable app's shell. On a public name they sit behind the identity gate like every page (D-3 of the push issue) |
| `/push/subscribe`, `/push/change`, `/push/unsubscribe`, `/push/forget`, `/push/settings`, `/push/rebind`, `/push/test` | POST | O | Subscribe or drop a device, swap a rotated subscription, mute and quiet hours, bind the sender to this viewer, a test push. A subscription must name a browser push service over https (`webpush.AllowedEndpoint`); pushes follow no redirect, so the viewer can't be made to POST elsewhere |
| `/owner/code`, `/owner/pair`, `/owner/signout` | POST | W | Ask for a sign-in code (shown outside HTTP), type it in, sign out. Rate limited; the code never comes back in an answer |
| `/p/<project>/` (issues), `hall`, `hall/ci`, `hall/station/<s>`, `hall/gate/<s>`, `company`, `hq/report`, `hq/report.md`, `needs-you`, `needs-you.json`, `owner-checks`, `postmortem/<slug>`, `connectors.json`, `connections`, `connections.json`, `reports/<g>/<w>`, `shelf`, `board`, `graph`, `workboard`, `canvas`, `status`, `status.md`, `status.json`, `release.json`, `pages/<n>`, `github`, `stats`, `health`, `agents`, `agents.json`, `agents/detail`, `lead`, `ceo`, `lead.json`, `retros`, `decisions`, `updates`, `departments/new`, `metrics`, `metrics.json`, `balance`, `systems[/…]`, `hash`, `issues.json`, `docs[/…]`, `yolo.json`, `yolo/review` | GET | R | Pages and JSON |
| `issue/<slug>`, `…/comments`, `…/approvals.json`, `…/transition?to=` | GET | R | Issue page, comments, approvals, transition preview. The preview runs `command_succeeds` validators, which are config, with quoted values |
| `…/dispatch-prompt`, `…/playtest` | GET | R | Prompt text, playtest state |
| `shelf/read/<id>` | GET | R | Output reader: markdown rendered without raw HTML |
| `shelf/file/…`, `ci/log/<name>`, `page-raw/…`, `files/…`, `attachments/<hash>.<img>` | GET | R | Files: confined to their folder, with their own CSP |
| `lead/file/<hash>/<name>` | GET | O | A file the owner attached in a lead chat, back to the owner only: `application/octet-stream`, `Content-Disposition: attachment`, `nosniff`, `CSP: sandbox`; read through `os.Root` (no link leads out), never unpacked or rendered |
| `issues/create`, `issue/<slug>` | POST | W | Create or update an issue |
| `…/comments`, `…/comments/toggle`, `…/comments/delete` | POST | W | Comments |
| `…/data`, `…/data/<id>/status\|tier\|comment` | POST | W | Data sidecar |
| `…/data/<id>` | DELETE | W | Data sidecar |
| `…/checkbox`, `…/type`, `…/delete`, `upload`, `lead/image` | POST | W | Boxes, type, delete, image uploads |
| `retros/retro/<n>/status`, `retros/bug/<n>/status`, `updates/decline`, `updates/reopen` | POST | W | Retro, bug and update records |
| `…/approve`, `needs-you/approve` | POST | O | Approve gates (and nudge the agent) |
| `…/send-back`, `…/yolo-review` | POST | O | Send back for fixes, YOLO review |
| `…/clarify` | POST | O | The owner's answers to the clarify round (Station form); each a D-n decision with their words |
| `yolo/on\|extend\|off\|review`, `autostart/pause\|resume\|off` | POST | O | YOLO window, an issue's Agree / Back to on the review after YOLO, auto-start |
| `owner-checks/tick` | POST | O | Owner boxes |
| `crew/lead`, `crew/stop`, `crew/nudge`, `queue/pause\|resume` | POST | O | The Crew page across projects (`/crew`): start a lead (as `lead/spawn`), end one worker's tmux session (`=name:` target; never the server, never a paused worker, the worktree stays), paste one line into a worker (`agentrun.Nudge`), the owner's queue pause. They run tmux, but only for the owner; each is logged in `$XDG_STATE_HOME/ai-workflow-engine/crew/actions.jsonl` with who and when |
| `crew/tail` | GET | O | A worker's or lead's last 60 pane lines on the Crew page: a pane may hold anything (the owner's attention) |
| `departments/add` | POST | O | Department from a template |
| `shelf/review`, `decisions/<slug>/<D-n>/confirm` | POST | O | Output review, decision confirm (and its `hard` option: making a decision the owner's hard rule) |
| `decisions/challenge/resolve` | POST | O | Keep a challenged decision: closes its open challenges (Needs you's Keep it) |
| `decisions/<slug>/<D-n>/renew`, `decisions/<slug>/<D-n>/let-go` | POST | O | An expired hard decision's Renew (new `expires:`) and Let go (`ended:`) from its Needs you row |
| `shelf/comment`, `shelf/comment/resolve` | POST | O | The owner's comments on an output (agents answer with `issue-cli output reply`) |
| `hall/station/<s>/edit/preview`, `hall/station/<s>/edit/save` | POST | O | Edit a station of `workflow.yaml` (commits on main) |
| `memory/edit\|delete\|pin\|unpin` | POST | O | The owner's edit, delete or pin of a shared memory (agents use `issue-cli memory`) |
| `postmortem/<slug>/decide` | POST | O | The owner's one round on a post-mortem's actions: memories, one-file commits on main, experiment proposals |
| `health/recommendation/<id>/file\|dismiss` | POST | O | File or dismiss a health recommendation |
| `…/dispatch`, `…/dispatch/reprompt`, `retros/review`, `lead/spawn` | POST | X | Start an agent: tmux plus a login shell; agent name validated |
| `lead/file` | POST | O | Attach any file to a lead chat (.zip, PDF, text; 50 MB): streamed into `.agent-logs/<lead>/chat/files/<hash>/<name>` (0600, name cleaned to ASCII) through `os.Root`, never unpacked |
| `lead/send`, `lead/key`, `lead/answer` | POST | X | Paste into or press keys in the CEO's session. `lead/send` pastes `[Image: <path>]` and `[File: <path>]` lines only for files already in the chat's store |
| `agents/open` | POST | X | Open a terminal on an agent |
| `…/action/<id>`, `action/<id>` | POST | X | Custom actions from `workflow.yaml` |
| `…/check-script`, `…/playtest` | POST | X | Run guided checks or a playtest in tmux |
| `…/edit-in-nvim`, `docs/<p>/edit-in-nvim` | POST | X | nvim in tmux |
| `jobs/<dept>/<job>/run`, `health/review`, `updates/install` | POST | X | Start an agent for a job, a health review or an install |
| `connectors/check` | POST | X | Run a department's connector checks |
| `github/fetch`, `github/import` | POST | X | `gh` calls, issue import |

## Known gaps (follow-up issues)

- **The owner/agent boundary on loopback (what's left).** The O routes need the owner's sign-in now (Owner sign-in), so an agent's request alone can't make an owner decision. Agents run as the same user, though, and one that sets out to break the rule still can: read the viewer's terminal pane while a code shows (use the dialog; `-owner-code terminal` only where no agent reads that terminal), script the macOS dialog (needs Accessibility access for its terminal app), read the browser's cookie store, or run its own server on another loopback port that the owner then opens in the same browser (cookies are per host, not per port). The files issue-cli writes (approvals, YOLO state) are outside this gate: there the worker guard is the barrier. A hardware-backed or keychain-gated credential would close more of this.
- **Styles are not restricted.** `style-src` stays open: templates use `style=` attributes. A style injection can't run script, but it can restyle a page.
- **A project page can still navigate.** CSP can't stop a page from navigating itself or opening a popup to another site with data in the URL, because `navigate-to` was dropped from the spec. A project page is the project's own HTML. The viewer stops posting status once the frame leaves the page.
- **cmd/telemetry-collector** is a separate binary and was not part of this review. If it is ever exposed to the internet, it needs its own review.
- **Accepted leaks (F-17, info).**
  - `lead/image` and `lead/file` return the stored file's absolute path. The chat box sends that path back with the message, and the agent reads it as `[Image: <path>]` or `[File: <path>]`. Only a same-origin page can read the answer, and a public request needs the verified owner.
  - A chat file's path shows on the lead chat page (class R: the turn chip's title and `lead.json`), and `lead/send` (class X) pastes a `[File: <path>]` line for any stored chat file. So a caller who may paste into the lead could have it read a file the owner attached, which only the owner may download. It is not a new power: the same caller can already paste any instruction, such as "read <path>", into the lead. The path names only files already in the chat's store.
  - `/files/` serves any image under the project root, chat images included. The issue pages link images by their path from the root, so a narrower rule would break them. The same identity gate applies.
  - A path outside the home folder (a project under `/srv`) still shows in an error body. Only a same-origin page can read the body.
