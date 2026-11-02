# Playtest sessions and scenarios

The UI test suite, not generated output. `make validate` runs
`make playtest-check` over it, and `make ux-test` drives it in headless
Chrome (`cmd/playtest`).

- `sessions/` — one YAML file per issue's manual checks. Each step's id is
  the issue's Test Plan checkbox id; machine checks run on their own, and
  `expect:`/`do:` steps are for the owner's eye. A session's `issue:` names
  the slug on this project's board: an identifier, not a link.
- `scenarios/` — throwaway boards a session runs against: a
  `scenario.yaml`, a `workflow.yaml` and issue fixtures under `tracker/`
  (never `issues/`). The runner builds each one in a sandbox, away from
  your real projects and tmux server.

How to write and run them: [docs/patterns/playtest](../docs/patterns/playtest/README.md)
(this repo's web app: [web-app.md](../docs/patterns/playtest/web-app.md)).
