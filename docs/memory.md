---
title: "Shared Memory"
order: 12
---

A **memory** is one fact an agent learned that the agents after it should know: a convention, a pitfall, how something is done, who knows what. It carries its source (issue, commit, comment), its author and its history. `issue-cli start` prints the pinned memories and the ones matching the issue, and the owner sees, edits, pins and deletes every memory in the viewer.

The design takes the patterns of memory libraries (mem0, Letta, Graphiti, LangMem, A-MEM) without their engines. The agents are the LLM: issue-cli retrieves similar memories and refuses near-duplicates, and the calling agent makes the decision mem0's update step makes. There is no model, no server, no API key and no telemetry; nothing leaves the machine. Source: factory/shared-memory-between-agents-mem0-or-similar-open-source (Design v2, D-6 to D-16). Phases 2 to 4 are its child issues: local embeddings, consolidation and decay ([below](#consolidation-candidates-and-decay), factory/shared-memory-phase-3-consolidation-job-extraction-candidates-and-decay-ranking), and [entities](#entities-who-knows-what) (factory/shared-memory-phase-4-entities-and-who-knows-what).

## Writing: the update step

```bash
issue-cli memory add "make shot URL= takes one screenshot; no playtest session needed" --kind howto
```

| What exists | What add does | mem0's op |
|---|---|---|
| nothing similar | adds it | ADD |
| the same fact (same words, case and punctuation aside) in the same scope | writes nothing: `Already remembered as m-…` | NONE |
| a near-duplicate (half or more of the words shared) | **refuses**, lists up to 3 similar memories and the three ways on | (the agent decides) |

The three ways on:

- `--updates <id>`: the same fact, better worded. Its text is replaced and history keeps the old one (UPDATE).
- `--supersedes <id>[,<id>…]`: the old one is no longer true, or several near-duplicates merge into yours (every id is invalidated with yours and linked). Yours is added, linked to it, and the old one becomes invalid (superseded, still in history). This follows Graphiti: facts are invalidated, not deleted.
- `--new`: a different fact after all (ADD).

Other flags:

- `--at <status>` says which step the fact belongs to ([At a status](#at-a-status)).
- `--kind` takes `fact` (the default), `convention`, `pitfall`, `howto` (steps that worked) or `who` (who knows what).
- The source is your own issue when you are a worker. `--issue` names another, and `--commit` and `--comment N` add to it. The author is your session's name, or `owner` from your own shell.
- `--tag a,b`, `--system S` (default: the source issue's), `--links id,id`, and `--expires YYYY-MM-DD` (the last day it is true).
- `--suggest-pin` asks a lead to pin it.

`memory edit <id> --text "…"` corrects a memory, and `memory edit <id> --at <status>` ties it to a status (`--at none` unties it). `memory forget <id> --reason "…"` invalidates it and keeps it in history. One fact is at most 500 characters; a longer note belongs in the docs, and the memory says where.

## Scopes

| Scope | Who reads it | Default for |
|---|---|---|
| `project` | every agent on the project | Product's and HQ's agents, the owner |
| `lead` | every lead (the CEO, department and group leads), the owner and the lead flush's worker (read only), no other worker | `--scope lead` |
| `department:<key>` | that department's lead and workers, the CEO, the owner | a department's lead and workers (`--scope project` widens it) |
| `agent:<session>` | that session (a lead's notebook), the CEO, the owner | `--scope agent` |
| `machine` | every project on this machine | `--scope machine`, refused from a project whose `privacy:` keeps its work files in a tracker or its code private, as are `--updates`, `--supersedes` and `edit --text` on an `s-…` memory |

The project's log is `<issues>/.memory/events.jsonl`, with the work files (Settled rule "Business data sits with the work files"), so a private tracker keeps it private. The machine's log is `$XDG_DATA_HOME/ai-workflow-engine/memory/events.jsonl` (else `~/.local/share/…`), in no repo. Ids say which log a memory is in: `m-…` the project's, `s-…` the machine's. Writes take a lock, so workers can write at the same time.

## Reading and injection

- `issue-cli memory search <words> [--kind] [--scope] [--tag] [--system] [--all] [--limit]` ranks the memories you can see with BM25 over text, tags and kind. Memories about the same system or department rank higher. With a local embedder configured, search also ranks by meaning ([Semantic search](#semantic-search-local-embedder)).
- `issue-cli memory list [--pinned] [--suggested] [--all] …` lists them, and `issue-cli memory show <id>` gives one with its state, links and history.
- **At start** (`issue-cli start`), `== Memories ==` shows the pinned memories of the issue's scopes, then the top 5 for its title, system, labels and slug. Those shown are recorded as used, which keeps them strong in [decay ranking](#decay-ranking).
- **On entering a status**, `transition` prints the memories that matter there ([At a status](#at-a-status)). `start` prints them too, under `At <status>:`.
- **In the briefing**, a worker's dispatch prompt has `## Memory`: the pins, and how to search and add. Pins are Letta's core memory: always true and short (at most 10, about 1,500 characters), listed as `Pinned on every project:` (machine memories) and `Pinned on this project:`, the same two layers as [Rules](workflow.md#rules). `memory pin|unpin <id>` is for leads and the owner only; a worker suggests with `--suggest-pin`, and the lead finds the suggestions with `memory list --suggested`. The CEO's and department leads' briefs carry a Memory line with their pins ([Lead rules](#lead-rules)).
- **Reflection**: `transition … --to done` and `retrospective` print `== Memory ==`. It asks what the next agent should know, names `memory candidates <slug>`, and lists the 3 most similar memories, so the agent updates one instead of adding a near-copy (LangMem's reflection, done by the agent).

## Lead rules

A lead's standing rule (when to hold, how to hand over) belongs to every lead, not to one Claude profile and not to every worker. Add it to the lead scope:

```bash
issue-cli memory add "an issue can reach done off main: check merge-base before calling it shipped" --kind pitfall --scope lead
issue-cli memory pin m-3392dc          # a lead or the owner: the whole text in every lead's brief
issue-cli memory list --scope lead     # a lead, the owner or the lead flush; any other worker gets nothing
```

- **In the brief:** the prompt `lead spawn` starts a CEO or department lead with carries the pinned ones in full under "Standing rules (lead scope)", at most 12 and 6,000 characters, apart from the project's pins, so neither crowds out the other. Then the unpinned ones, one line of 100 characters each, at most 8, and `+N more: issue-cli memory list --scope lead`.
- **`lead spawn`** prints how many there are and the list command; `lead flush pack` names it among the state to read.
- **Workers** don't read them in `start`, `search`, `list`, `show`, the status memories or `## Memory`. The one exception is the worker on a [lead flush](patterns/lead-session.md#the-nightly-flush) run: it hands the day to the next lead, so it reads them in `list`, `search`, `show` and `consolidate`, and names the stale ones in its progress comment. Anyone may write a new one, as with every scope, but only a lead or the owner edits, forgets, updates, supersedes or pins one, so a worker's memory reaches a brief as one clipped unpinned line. Use `--suggest-pin`.
- **Private text:** the lead scope stays in the project's log, with the work files. Secrets are refused as anywhere, and no check can tell deal, client or price context from a rule, so that stays out by the writer's care: put a pointer to the private file instead.
- **Claude memory** keeps pointers only (the lead's personal rules aside), so a Codex lead or a department lead gets the same rules.

## At a status

Start picks memories by what the issue is about. Some facts belong to a step instead: "main moves during validate, so re-check with vet and the touched tests and fast-forward at once" matters when shipping, on any issue, and an issue's title won't bring it up. Tie such a fact to its status:

```bash
issue-cli memory add "after a green validate, merge main and ff at once" --kind howto --at shipping
issue-cli memory edit m-7da61e --at shipping     # an existing memory
issue-cli memory edit m-7da61e --at none         # untie it
issue-cli memory list --at shipping              # every one tied there (search takes --at too)
```

`transition` ends with the memories of the status it entered:

```
== Memories for shipping ==
  m-7da61e (pitfall) Shipping on a busy night: main can move every ~6 min, …
  +2 more: issue-cli memory list --at "shipping"
```

- **The cap:** at most 3 memories and 600 characters per transition, strongest first ([decay ranking](#decay-ranking)). A memory is at most 500 characters, so that is one long one or two short ones. No setting changes it.
- **No memory there, no lines.** Most transitions print nothing extra.
- **One status per memory.** It is checked when written: a status none of the project's workflow files has is refused with the names, and stored as the workflow spells it. If a status is renamed later, `issue-cli health lint --check memory` names each memory left behind.
- **What it doesn't print:** a pinned memory (the briefing carries it), one no longer valid, and one the agent's scope can't read. A department's memory stays in its department whatever its status.
- **`--json`** transition output has no block; it is for the agent reading the transition.
- **Use:** shown memories count as used, so they stay strong.
- **Start** prints them under `At <status>:` for an agent resuming there, or moved there by start itself, and not again under *Matching this issue*.
- `--supersedes` keeps the old memory's status unless `--at` names another; adding the same fact again with `--at` ties the existing memory.

A process rule that always holds belongs in the status's `prompt:` in workflow.yaml. A memory is the lighter place: it has a source, it can expire, and it fades when nobody uses it. On the Memory page the row shows `at <status>`, read-only. Source: factory/a-memory-can-say-which-status-it-matters-at.

## Entities: who knows what

An **entity** is a `#topic` or an `@name` (an agent's session name, or a person) that a memory carries. It comes from the memory's text, where a `#` or `@` after a space, an opening bracket or the start is followed by a letter, or from its tags: `--tag playtest` is `#playtest` and `--tag @lead-x` is `@lead-x`. Names are lowercased, and trailing `.`, `-` and `/` are dropped. `issue #42`, `a@b.com`, `page#top` and a markdown `# heading` are not entities.

```bash
issue-cli memory add "@lead-dark-factory knows the #playtest runner" --kind who
issue-cli memory search @lead-dark-factory          # every memory about that agent
issue-cli memory search '#playtest' chrome          # memories on #playtest, ranked by the words
issue-cli memory entities --only topic              # who knows what
```

- **Search:** each `#topic` or `@name` in the query is a filter, and a memory must carry all of them. The other words rank as usual. With entities alone, the matches come newest first. Quote a `#` in a shell, or it is read as a comment.
- **`memory entities [--only topic|agent] [--kind] [--scope] [--all]`** lists the entities in the memories you can see, most memories first. Each line has the entity's count and kinds, and for a topic, `known by` the `@names` that share a `who` memory with it. `--json` gives the index.
- **Writing:** `memory add` prints `Entities: …` for what it found. A `who` memory without both an `@name` and a `#topic` gets a hint, not a refusal (D-3).
- **The index is folded, never stored.** It is computed from the current memories on each read, so an edit changes it and the owner's Delete removes the memory's entities with its text (D-1). Authors are not entities: a `who` memory is how an agent is linked to a topic (D-2).
- **Visibility:** the index counts only the memories the caller can read. A department's entities stay inside the department, like its memories.
- **No graph database.** Design v2 P17 takes the idea of entity nodes from mem0's graph memory and Graphiti, but not their servers (Neo4j, FalkorDB): there are no relation edges and no traversal, only tags and one pass over the memories (5,000 index well under a second).

On the [Memory page](#in-the-viewer), the **Entities** strip lists every entity with its count and who knows it, and a click filters the page to that entity (`?entity=%23playtest`). **Group: by entity** (`?group=entity`) lists the memories under each entity, a memory under every entity it carries, with *No entity* last. `memory.json` serves the index as `entities`. Source: factory/shared-memory-phase-4-entities-and-who-knows-what.

## Secrets and privacy

Every write path refuses text that looks like a secret: add, edit, update and the viewer's Save. That covers private key blocks, `sk-…`, GitHub, Slack, AWS and Google keys, JWTs, bearer tokens, a password in a URL, `password=` / `token:` / `api_key=` with a value, and long random-looking strings. The refusal names the kind and never prints the text, and nothing is stored. References such as `env:GSC_TOKEN` and `keychain:acme-gsc` pass (Settled rule "Secrets are references"). The call's clilog record hides the memory text like any free text (`clilog.RedactCall`). A private project can't write to the machine scope (Settled rule "Private projects stay private").

## In the viewer

`/p/<project>/memory` is linked from the HQ room's foot as *🧠 N memories*. It shows every memory of the project and of the machine, filtered by scope, kind, state (valid / superseded or forgotten / all), pins and entity, and grouped by entity on request.

- **Each row:** kind, scope, state, pin or a worker's pin suggestion, date, author, source issue (linked), commit, comment, the supersedes / superseded-by links, its entities (each a filter link, [Entities](#entities-who-knows-what)), and its history (every event, and how often it was used).
- **Actions:** **Save** (an owner edit, checked for secrets), **Pin** / **Unpin** and **Delete**.
- **Delete is the owner's hard delete:** the memory leaves the page, every earlier event about it loses its text, and history keeps only "deleted by the owner" (D-15).
- **Recently changed** lists what agents learned in the last 7 calendar days (from midnight of the date 7 days back).
- **Who can write:** writes pass only from the owner's browser (`fromOwnerBrowser`). A script or an agent gets 403. `memory.json` serves both logs.
- **Needs you:** nothing here waits on the owner, so it adds no rows (Settled rule "One waiting-on-you count").

## Semantic search (local embedder)

Off by default. `memory: embedder:` in the project's workflow.yaml names a local embedder; then `memory search`, start's `== Memories ==` and the reflection hint rank by words and meaning:

```yaml
memory:
  embedder:
    url: "http://127.0.0.1:11434/api/embed"   # Ollama's /api/embed, or:
    # command: ["memory-embed-macos"]          # the same JSON on stdin and stdout
    model: "nomic-embed-text"                  # sent with each request; keys the cache
    timeout: "5s"                              # per request (default 5s)
    min_similarity: 0.5                        # a cosine below this adds nothing (default 0.5)
```

- **One wire format.** Request `{"model": m, "input": [texts]}`, answer `{"embeddings": [[numbers]...]}`, as Ollama's `/api/embed`. A `command:` runs from the project root with the request on stdin and prints the answer.
- **Only this machine.** A `url:` must be loopback (127.0.0.1, ::1, localhost). A URL that leaves the machine is refused and search ranks by words: a hosted embedder sends memory text out, which is the owner's call (Settled rule "Nothing goes out without the owner's stamp"; the parent issue's AC6). There is no hosted setting.
- **mem-guard first.** Before each embed request issue-cli runs `mem-guard used`. At 70% or more, when mem-guard isn't on PATH, or when the embedder fails or times out, that call ranks by BM25 alone.
- **Scoring.** Each memory scores `0.5 × BM25 / best BM25 of the call + 0.5 × cosine` (cosine counted at `min_similarity` or more), then the system and department boosts. A memory with neither scores 0 and is left out (mem0's `score_and_rank`, without its LLM).
- **Cache.** Vectors are cached per memory text and model in `<issues>/.memory/vectors/<model>/<hash>.json`, with the work files, so a private tracker keeps them private. A call embeds at most 64 memories without a vector, in the same request as the query; the rest rank by words until later calls fill them in. Changing the model starts a new folder.
- **What it says.** With an embedder configured, `memory search` ends with `Ranked by words + meaning (<model>).` or `Ranked by words only: <why>.` (memory at 78%, embedder: connection refused, a refused URL). `issue-cli health lint --check memory` reports a config search can't use.
- **No Ollama?** `scripts/memory-embed-macos.swift` is a command embedder over the sentence embedding macOS ships (NaturalLanguage, on-device, nothing downloaded). Compile it once: `swiftc -O scripts/memory-embed-macos.swift -o ~/.local/bin/memory-embed-macos`, then `command: ["memory-embed-macos"]`, `model: "nl-sentence-en"`. It is light but its sense of meaning is weak (below); a model such as `nomic-embed-text` under Ollama ranks better.

Measured on a 36 GB M-series Mac with the macOS embedder, compiled, 512 numbers per vector:

| Call | Peak RSS | Time |
|---|---|---|
| embedder alone, 1 text / 65 texts | 30 MB / 41 MB | 0.04 s / 0.37 s |
| `memory search`, 50 memories, first call (embeds all 50) | 47 MB | 0.52 s |
| `memory search`, 50 memories, cached | 31 MB | 0.07 s |
| `memory search`, 5,000 memories, a call that embeds 64 | 48 MB | 0.37 s |
| `memory search`, 5,000 memories, all cached | 44 MB | 0.38 s |
| `memory search`, 5,000 memories, no embedder (BM25) | 37 MB | 0.02 s |

Filling the cache for 5,000 memories took 80 calls (43 s in all); the cache is 39 MB on disk. Reading 5,000 vector files is most of a cached call's time. For comparison, mem0 with fastembed peaked at 360 MB per call. The macOS model puts most English sentences between 0.25 and 0.55 cosine: "flaky when loaded" vs "Timing tests fail on a busy machine" is 0.53 and is found where BM25 finds nothing, but "how do I record a release note" vs "Changelog entries go under the Unreleased heading" is only 0.25 and is missed.

## Consolidation, candidates and decay

Phase 3 forms and tidies memories in the background, still without a model in the engine: issue-cli gathers and ranks, an agent decides through the update step, and nothing in this phase deletes. Sources: the epic's Design v2 P11 to P14; factory/shared-memory-phase-3-consolidation-job-extraction-candidates-and-decay-ranking#D-1 to #D-4.

### Candidates from an issue's trail

```bash
issue-cli memory candidates <slug> [--all] [--json]
```

It lists what the issue's trail could teach, each as `c<N> · <source>` with its text and up to 3 close memories (`~ m-…`):

- the issue's `progress:`, `question:`, `answer:`, `risk:`, `decision:` and `report:` comments (`comment N`, for `memory add --comment N`);
- its `## Decisions` lines (`D-n`);
- the items of each section of its retros (files under `retros/` whose `Issue:` line names it);
- the list items under a `Known` or `Corrections` heading of each markdown output on the shelf linked to it (`output <id> · known`), so a report-only run's facts can be remembered.

Machine-written comments (approvals, dispatch, `tests:`, `yolo-owner-check:`) are left out, and so is any transcript, pane or log: only what is already on the issue or in the repo. A candidate that looks like a secret shows its kind and no text. One already remembered (a memory from that issue and comment, or with the same words) drops out; `--all` shows it with its id. The command writes nothing: the agent writes the fact in its own words with `memory add … --issue <slug> --comment N`, and the update step decides the rest.

### The consolidation pack and job

```bash
issue-cli memory consolidate [--scope S] [--json]
```

It lists, for the memories you can see:

| Group | What | The proposal |
|---|---|---|
| Near-duplicates | clusters of valid memories in one scope sharing half or more of their words (the update step's line), at most 20 | merge: `memory add "<one fact>" --scope <scope> --supersedes id,id`, or keep the best (`--updates`) and forget the rest with `--reason "merged into <id>"` |
| Stale | valid, unpinned, strength under 0.25 (about 180 days untouched and unused) | still true: leave it; no longer true: `forget --reason` or `--supersedes` |
| Expired | past `--expires` (already invalid) | true again: a fresh one with `--supersedes` |
| Promote | project conventions, pitfalls and howtos used 5+ times and 30+ days old, not measured unhelpful | one issue for the owner listing them; CLAUDE.md is never edited by the engine or the job |
| Unhelpful | valid, unpinned, measured unhelpful over 15+ traced stages ([Memory as a cache](#memory-as-a-cache)) | proposed to the owner for forgetting, in the same owner issue; the run never forgets one |

It writes nothing. The **`memory-consolidate` job** runs it on a calendar: its brief is bundled with the engine, so a department's `jobs:` names it without a file in the project ([Recurring jobs](departments.md#recurring-jobs)):

```yaml
departments:
  - name: Marketing
    jobs:
      - id: memory-consolidate
        name: Memory consolidation
        every: week
        on: sun
        brief: builtin:memory-consolidate
```

The run's agent reads the pack, decides each cluster, looks at the stale and expired ones, files the promotion issue and posts a `progress:` line with the counts. It never deletes, pins, edits CLAUDE.md or moves another issue. Its standing start is off until the owner turns it on with their words (`issue-cli job auto on <dept>/memory-consolidate --quote "…"`; Settled rule "Nothing goes out without the owner's stamp"). Jobs belong to departments, so the job goes on one; it consolidates what that department's agents can see (the project's memories and the department's).

### Decay ranking

Every ranking (search, start's `== Memories ==`, the reflection hint, candidates' close memories) weighs a memory by its **strength**:

- recency halves every 90 days since it was last added, changed or used (`0.5^(days/90)`);
- uses raise it: × `1 + 0.15 ln(1 + uses)`, at most × 1.5, and strength never passes 1;
- a pinned memory has strength 1.

A hit's score is multiplied by `0.3 + 0.7 × strength`, so an old, unused memory ranks below a recent or used one with the same words but still shows when it is the only match. A memory measured unhelpful has its strength halved first ([Memory as a cache](#memory-as-a-cache)). `memory show` prints the strength. Decay never deletes or hides; a memory past `--expires` is invalid as before. `TestOldUnusedMemoriesRankBelowRecentUsedOnes` and `TestConsolidateAt5000IsCheap` (5,000 memories in about 0.3 s) cover it.

## Memory as a cache

The owner's policy (factory/fresh-agent-per-stage-measured-…#D-2, 2026-11-07): the issue file and HANDOFF.md are the short-term memory of the current work, and `issue-cli memory` keeps only facts that have proved themselves across issues. Each stage gets only the memories that help it, scored by usefulness from the traces. A memory that does not help decays and is proposed to the owner for forgetting. Agents write only durable facts during work, and everything else goes through distillation with the owner's round. Source: factory/memory-as-a-cache-usefulness-from-stage-traces-unhelpful-memories-decay-durable-facts-only-during-work-and-the-rest-through-distillation.

### What lives where

| Layer | What it holds | Who writes it | Lives |
|---|---|---|---|
| Working context | the session's chat, the files it read, its test runs | the agent, as it works | the agent's session; gone when the session ends |
| The issue file and HANDOFF.md | the current work: brief, design, decisions (`## Decisions`), comments (`progress:`, `question:`), checklists; HANDOFF.md says what is done, the next step and the open questions | the worker and its lead, through issue-cli; HANDOFF.md by hand, never committed | `<issues>/…/<slug>.md`; `<worktree>/HANDOFF.md` |
| Long-term memory | durable project facts, true across issues: conventions, pitfalls, howtos, who knows what | agents (`memory add`), the consolidate run, the owner on the memory page | `<issues>/.memory/events.jsonl`, the machine store |
| Rules and decisions | what the owner decided: standing rules and D-n decisions with scope, hard or soft, expiry | the owner (rules.md, `rules:`), workers and leads record decisions (`decide`) | `~/.config/ai-workflow-engine/rules.md`, workflow.yaml `rules:`, the issues' `## Decisions` and `<issues>/.decisions/` |
| Procedures | how work is done: statuses, gates and prompts, skills, the docs | the owner and engine issues | workflow.yaml, `docs/`, CLAUDE.md, skills |

A note on one issue (what you tried, where you stopped, what this branch needs) belongs in the issue or HANDOFF.md. A lesson for later issues goes through the post-mortem at close or `memory consolidate`, where the owner agrees or refuses it. A rule or a decision is a D-n line or a rule, never a memory.

### Writes during work: durable facts only

`memory add` from a worker refuses text that reads as a note on its own issue: "this issue", "my issue", "this ticket", "my branch", "my PR", a worker's branch name (`work/<area>/<slug>`), or its own issue's slug. The refusal names where the note belongs. A lead or the owner gets the same words as a warning, and anyone naming another issue's slug gets a warning (a memory should stay true after that issue closes, and `--issue` keeps the source). A decision citation (`<slug>#D-n`) is not a note, and neither is a path, a package or a status name. `issue-cli health lint --check memory` flags every valid project memory whose text reads that way, with `memory edit` / `memory forget` as the fix. `start`'s add line and the briefing's `## Memory` say the same.

### Usefulness from the traces

Every memory a stage is shown (start's `== Memories ==`, transition's memories `--at` the status, a stage briefing) is recorded as a `use` event with the **issue and status** of that stage. The issue's trace (`<issues>/.traces/<slug>.jsonl`, one span per status visit, [Traces](workflow-stats.md)) says whether the gate out of that visit passed **first try**. The join puts each use on the visit of its status that holds its time, once per memory per visit.

- The **baseline** is the first-try rate of every traced visit at that status.
- A memory's **usefulness** is the mean of (first try − baseline) over its last **20** injections with a known gate result, overall and per status. Above 0, the stages it reached did better than usual.
- No score until **8** known injections ("not measured"). An open visit, or one with no gate result, is unknown, not a failure. Use events from before this change name no issue and are not scored.
- **Helps**: measured with a lift above 0.05. **No help**: measured with a lift of 0 or below.

`memory show <id>` prints `Usefulness: +0.12 over 14 (helps) · in design +0.30 (6) · testing — (3)`, `memory list --json` and the page's `memory.json` carry `usefulness`, the memory page shows a chip per memory and filters by it (`?use=helps|nohelp|unmeasured`), and the consolidate pack prints it on each line. It is a correlation, not proof: a memory that reaches hard stages looks unhelpful, so the threshold asks for many stages before anything moves.

### Decay and forgetting

- A memory measured **no help** ranks lower: its strength is halved before the decay weighting ([Decay ranking](#decay-ranking)). It still shows when it is the best match.
- No help over **15** or more known injections puts it on the consolidate pack's **Unhelpful** list. The consolidate run lists it for the owner in its owner issue and never forgets it. The owner forgets or keeps it.
- A pinned memory is never ranked down, hidden or listed. A score never deletes, forgets or invalidates anything.

### What a stage gets

`memory.ForStage` picks a stage's memories at a status: memories `--at` the status come first, most useful there first, then strongest, within the caller's cap (start and transition: 3 memories and 600 characters; a stage briefing: its budget). The rest that start ranks for the issue leaves out memories `--at` another status and those measured unhelpful at this status. A memory with no data at a status still reaches it, so a new memory can prove itself. In practice a memory with no `--at` reaches only the statuses where its usefulness does not show it as unhelpful.

## Footprint (mem-guard)

Nothing runs besides the issue-cli call. Measured on 2026-10-31 on a 36 GB M-series Mac (mem-guard at 12%), a sandbox project with 5,000 memories (a 1.3 MB log):

| Call | Peak RSS | Time |
|---|---|---|
| `issue-cli show <slug>` (no memory read) | 19 MB | 0.05 s |
| `issue-cli memory search …` | 38–40 MB | 0.03 s |
| `issue-cli start <slug>` (fold, rank, record use) | 41 MB | 0.08 s |

The same job in mem0, run fully local (fastembed, embedded Qdrant, no LLM), peaked at 360 MB per call. `TestSearchAt5000IsCheap` bounds the load and rank.

## How it differs from what exists

| Existing | Why it isn't a memory |
|---|---|
| Decisions (`issue-cli decide`) | answers for one issue, read when cited |
| Retros (`retros/`) | workflow feedback, read by the experiment loop |
| CLAUDE.md, docs | settled rules, through a commit; a memory that keeps proving useful is proposed to move there by the consolidation job, as an issue for the owner |
| Claude Code's file memory (`~/.claude/projects/<folder>/memory`) | one user's notes per checkout folder (each worktree is a new folder), Claude only, not in the viewer. The engine never reads or writes it |
| Issue comments | one issue's trail |

**Not yet:** a hosted embedder (the owner's gate), clusters by meaning (the pack uses word overlap only), entities and who-knows-what search (phase 4).
