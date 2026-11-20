---
title: "Stand up the CMS"
order: 11
---

# Stand up the CMS

Marketing's `piece` and `seo-pass` publish an approved post or page to the project's site through the `cms` connector ([Publishing to the site](departments.md#publishing-to-the-site)). The site is Ghost (MIT, the cms slot's default: `issue-cli department partners --slot cms`), the CMS and the site in one container. This page takes you from nothing to a Ghost on this Mac in one command, then, when you want it, to a site on your own domain. Steps marked **(owner)** are yours: only you can do them, and nothing on this page leaves the machine until you do.

## The command

```sh
issue-cli --project <project> cms up            # --dry-run first shows the plan and the compose
```

`department add marketing` prints this line under its Partner options. Options: `--email <addr>` (your sign-in email for Ghost; default the project's `git config user.email`), `--title "<t>"` (the site's title; default the project's name), `--port <n>` (default 2368, the port the `cms` connector's check reads). Run it again whenever you like: it changes nothing that is already right.

It needs macOS (the password and key go to Keychain), Docker with compose v2 (Docker Desktop, or colima with the compose plugin), and `mem-guard check` not blocked. A worker is refused: you run it, or your lead relays it to you.

## What it did

Each step prints one line (`✓ compose: written`, …; `--json` gives them as `steps`):

1. **mem-guard check.** Blocked: it stops, and nothing is written or started.
2. **compose.** It writes `compose.yaml` in `~/.local/share/ai-workflow-engine/cms/<project>/` (under `$XDG_DATA_HOME` when set), outside every repository: it refuses a folder inside a git work tree. Ghost's content (the SQLite database, images, themes) is in `content/` beside it, and `ghost.json` keeps the sign-in email, title and port, no secret. A compose already there is kept as it is, even one you edited. What the compose says:
   - `image: ghost:6-alpine`, upstream's image, unchanged; `restart: unless-stopped`.
   - `ports: ["127.0.0.1:2368:2368"]`: only this Mac reaches it; `url: http://127.0.0.1:2368`.
   - SQLite, which is Ghost's development database: fine on this Mac, not for a public site (go-live below).
   - `privacy__useUpdateCheck`, `privacy__useGravatar` and `privacy__useRpcPing` off: Ghost calls no one.
   - Mail goes to `127.0.0.1:1025`, where nothing listens: no mail transport that can deliver. (Left unset, Ghost would deliver mail itself.) So no newsletter or invite can go out.
   - `security__staffDeviceVerification: "false"`: Ghost would email a code to each new device at sign-in, and with no mail that would lock you out of `/ghost`.
3. **ghost.** `docker compose -p ghost-<project> -f <that compose> up --detach`, then it waits (up to 2 minutes) until `/ghost/api/admin/site/` answers.
4. **owner account.** A random 32-character password goes into Keychain as `ghost-owner` (account: the project) first, then Ghost's setup makes the owner account with it (`/ghost/api/admin/authentication/setup/`, no browser). The password is never printed, never on a command line (`security -i` reads it on stdin), never in a file. A Ghost already set up is not set up again: the password is read back from Keychain to sign in.
5. **admin key.** It signs in (a session held in memory only), makes the "Dark Factory publish" custom integration (or reuses it), and puts its Admin API key, `<id>:<secret>`, into Keychain as `ghost-admin-key` (account: the project). It is the key the `cms` connector reads (`GHOST_ADMIN_KEY: keychain:ghost-admin-key`). Written only when Keychain lacks it; never printed.
6. **link tagging.** It turns Ghost's `outbound_link_tagging` off, every run: otherwise Ghost adds `?ref=<site>` to every link, and what goes out differs from what you approved.

It writes no config: the `cms` connector stays commented until you turn it on (next section), as with every partner.

When a run stops, the line says why and what to do. Ghost set up, but `ghost-owner` gone from Keychain: sign in yourself and make the integration (below), or start over by removing the folder's `content/`.

## Sign in, and turn the connector on

- **(owner)** Sign in at `http://127.0.0.1:2368/ghost` with your email and the password from Keychain: `security find-generic-password -s ghost-owner -a <project> -w` prints it (run it yourself). Change the theme, the site's title or the navigation there; posts the factory publishes show up under Posts and Pages.
- **(owner)** Turn the connector on: uncomment the `cms` entry under your Marketing department's `connectors:` in projects.yaml (`departments/<key>/` has the department's files; the commented entry came with `department add marketing`). With Ghosts for several projects on this Mac, name this project's key: `GHOST_ADMIN_KEY: keychain:ghost-admin-key/<project>`; on another port, change the entry's `check:` and set `GHOST_URL`.
- `issue-cli connectors marketing` shows the key set and the check green. From then on, publish puts an approved piece on the site and records its URL; until then publish asks you for the link.
- Stop Ghost with `docker compose -p ghost-<project> -f ~/.local/share/ai-workflow-engine/cms/<project>/compose.yaml stop`; `cms up` starts it again.

Set Ghost up by hand instead (another machine, no Keychain)? The same steps work in Ghost's own screens: make the owner account, add a custom integration under Settings → Integrations, keep its Admin API key in Keychain (`security add-generic-password -s ghost-admin-key -a "$USER" -w`) and turn outbound link tagging off.

## Go live

Until here the site is on this Mac only. Going live puts it on the internet: it spends money (a server, a domain) and publishes, so every step is yours, and the factory does none of them. Do them in this order.

1. **(owner) Choose where the site runs.** A small server (a VPS with 1 GB of memory is enough for one Ghost with MySQL), or this Mac behind a tunnel. Ghost(Pro), Ghost's own hosting, is the paid alternative that does steps 2 to 4 for you.
2. **(owner) Run upstream's compose with MySQL 8.** SQLite is Ghost's development database; for a public site use upstream's Docker setup (github.com/TryGhost/ghost-docker), which pairs Ghost with MySQL 8 and Caddy. Follow its README, unchanged: never copy its files into a project repository.
3. **(owner) Set the site's `url:` to the domain** (`url: https://example.com` in its `.env` or compose). Ghost writes that URL into every link, the sitemap, RSS and canonical tags, and the publish helper records it, so the links on the shelf are the public ones from then on.
4. **(owner) TLS.** Caddy in upstream's compose gets and renews the certificate itself once the domain points at the server; behind a tunnel, the tunnel gives it.
5. **(owner) DNS.** At your registrar or DNS host, point the domain at the server (an `A`/`AAAA` record, or the tunnel's `CNAME`). Wait until `https://<domain>/ghost` loads.
6. **(owner) Real mail, then device verification back on.** Give Ghost a mail provider (its `mail__` settings) so invites, password resets and the device code reach you, then drop `security__staffDeviceVerification: "false"`. Newsletters stay off until you choose to send one: the publish helper never asks Ghost to mail.
7. **(owner) Move the content and the key.** Export the posts from this Mac's Ghost (Settings → Migration tools → Export) and import them into the new one, make the "Dark Factory publish" integration there, and put its key in Keychain as `ghost-admin-key` (`security add-generic-password -U -s ghost-admin-key -a <project> -w`, which asks for it). Then change the `cms` entry's `check:` to `https://<domain>/ghost/api/admin/site/` and set `GHOST_URL=https://<domain>`, and stop the local one.
8. **(owner) Link tagging off on the new Ghost** (Settings → Analytics, "outbound link tagging"), as `cms up` did here.

The `owner_only` gate before publish stays in place after go-live: nothing reaches the site until you approved the piece.
