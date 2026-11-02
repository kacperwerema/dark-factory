# Feature catalog: one entry per file

Every workflow.yaml capability the engine supports, one `<id>.yaml` per
entry: the entry as a mapping, with `id:` equal to the file name. Two
branches that each add or change an entry touch different files and merge
without a conflict.

Read by:

- `issue-cli workflow scan` detects which entries a workflow.yaml has and
  writes its `features:` record
- `issue-cli workflow upgrade` proposes entries a project lacks (`propose:
  true` only, unless `--all`), item by item, read-only
- the viewer's Updates tab

Catalog order, which `workflow scan` records `features:` in, is by category
(lifecycle, gates, automation, actions, board, workbench, dispatch,
conventions), then by id (`ParseCatalog` in internal/workflowupgrade).

Kept separate from CHANGELOG.md (engine release notes). Installing a feature
means an agent copies its snippet, adapted, into workflow.yaml; nothing here
is merged at load time. When the engine gains a capability, or a change
alters what a project adopts:

1. add a new entry file, or change one, with `version:` = the next release
   (the one after the newest `## vX.Y.Z` in CHANGELOG.md);
   `issue-cli workflow catalog-bump <id>` sets it and carries the template
   stamps and record lines along;
2. bump a template's `template: <name>@<version>` stamp if its workflow
   changed, and run `issue-cli workflow scan --template <name>` in the
   checkout so its `features:` record matches.

`make validate` enforces both: TestEveryWorkflowFeatureHasACatalogEntry fails
on a workflow.yaml key, validator rule or action type no entry covers, and
`issue-cli workflow catalog-check` fails on a branch that changes an entry's
sources: or a template without the bump (or a `Catalog: n/a <id> <why>`
commit trailer), and on the catalog's own problems. Each failure names its
fix. A projects.yaml key gets docs and a `health lint` check, not an entry.

## Fields

- `id`: stable kebab-case name, the file name; recorded as `<id>@<version>`
- `version`: issue-cli release that last changed the entry
- `category`: grouping and order: lifecycle, gates, automation, actions,
  board, workbench, dispatch, conventions
- `propose`: true: `upgrade` proposes it to projects that lack it; false:
  catalog only (scanned and recorded; listed by `upgrade --all`)
- `templates`: bundled templates `upgrade` proposes it for (empty = all)
- `requires`: entry ids the project must have first; upgrade proposes the
  entry only then (`--all` lists it with `needs`)
- `sources`: repo globs of the code behind the feature (`**` allowed,
  anchored at the repo root; a folder is `internal/x/**`, a bare
  `internal/x/` matches nothing), or `file.go#Symbol` for one Go
  declaration; `["@prompt"]` alone for a prompt-only convention. Changing
  them on a branch needs a version bump (catalog-check)
- `title`, `what`, `why`, `where`, `question`: shown to the agent
- `setup`: ordered `{who: owner | agent, do: …}` steps beyond the snippet
- `unlocks`: what the hall's Updates page shows locked or empty without the
  feature, each a `text` and a project-relative `url` (`/status`). Display
  only: changing it needs no version bump
- `snippet`: YAML example (must parse); adapt status names to the project
- `detect`: structural probes over the parsed workflow.yaml; the feature is
  present when ANY probe matches. Each probe has a `path` and at most one
  condition:
  - (none): the value is set: not empty, false, 0 or null
  - `equals`: the value equals this scalar
  - `one_of`: the value is one of these strings
  - `rule_in`: the rule name (text before `:`) is one of these; a new
    validator rule goes in the `rule_in:` of the entry that detects it
  - `contains`: the string value contains this text (case-insensitive)

  Path syntax: `key`, `key[]` (each list item), `*` (each map value), or a
  map key by name (`types.docs`). A path starting with `statuses[]` or
  `transitions[]` also matches inside every `types.<t>` and `systems.<s>`
  overlay.
