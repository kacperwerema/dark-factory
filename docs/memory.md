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

- `--kind` takes `fact` (the default), `convention`, `pitfall`, `howto` (steps that worked) or `who` (who knows what).
- The source is your own issue when you are a worker. `--issue` names another, and `--commit` and `--comment N` add to it. The author is your session's name, or `owner` from your own shell.
- `--tag a,b`, `--system S` (default: the source issue's), `--links id,id`, and `--expires YYYY-MM-DD` (the last day it is true).
- `--suggest-pin` asks a lead to pin it.

`memory edit <id> --text "…"` corrects a memory. `memory forget <id> --reason "…"` invalidates it and keeps it in history. One fact is at most 500 characters; a longer note belongs in the docs, and the memory says where.

## Scopes

| Scope | Who reads it | Default for |
|---|---|---|
| `project` | every agent on the project | Product's and HQ's agents, the owner |
| `department:<key>` | that department's lead and workers, the CEO, the owner | a department's lead and workers (`--scope project` widens it) |
| `agent:<session>` | that session (a lead's notebook), the CEO, the owner | `--scope agent` |
| `machine` | every project on this machine | `--scope machine`, refused from a project whose `privacy:` keeps its work files in a tracker or its code private |

The project's log is `<issues>/.memory/events.jsonl`, with the work files (Settled rule "Business data sits with the work files"), so a private tracker keeps it private. The machine's log is `$XDG_DATA_HOME/ai-workflow-engine/memory/events.jsonl` (else `~/.local/share/…`), in no repo. Ids say which log a memory is in: `m-…` the project's, `s-…` the machine's. Writes take a lock, so workers can write at the same time.

## Reading and injection

- `issue-cli memory search <words> [--kind] [--scope] [--tag] [--system] [--all] [--limit]` ranks the memories you can see with BM25 over text, tags and kind. Memories about the same system or department rank higher. With a local embedder configured, search also ranks by meaning ([Semantic search](#semantic-search-local-embedder)).
- `issue-cli memory list [--pinned] [--suggested] [--all] …` lists them, and `issue-cli memory show <id>` gives one with its state, links and history.
- **At start** (`issue-cli start`), `== Memories ==` shows the pinned memories of the issue's scopes, then the top 5 for its title, system, labels and slug. Those shown are recorded as used, which keeps them strong in [decay ranking](#decay-ranking).
- **In the briefing**, a worker's dispatch prompt has `## Memory`: the pins, and how to search and add. Pins are Letta's core memory: always true and short (at most 10, about 1,500 characters). `memory pin|unpin <id>` is for leads and the owner only; a worker suggests with `--suggest-pin`, and the lead finds the suggestions with `memory list --suggested`. The CEO's and department leads' briefs carry a Memory line with their pins.
- **Reflection**: `transition … --to done` and `retrospective` print `== Memory ==`. It asks what the next agent should know, names `memory candidates <slug>`, and lists the 3 most similar memories, so the agent updates one instead of adding a near-copy (LangMem's reflection, done by the agent).

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
- **Recently changed** lists what agents learned in the last 7 days.
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

Measured on this Mac (M-series, 36 GB) with the macOS embedder, compiled, 512 numbers per vector:

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

- the issue's `progress:`, `question:`, `answer:`, `risk:` and `decision:` comments (`comment N`, for `memory add --comment N`);
- its `## Decisions` lines (`D-n`);
- the items of each section of its retros (files under `retros/` whose `Issue:` line names it).

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
| Promote | project conventions, pitfalls and howtos used 5+ times and 30+ days old | one issue for the owner listing them; CLAUDE.md is never edited by the engine or the job |

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

A hit's score is multiplied by `0.3 + 0.7 × strength`, so an old, unused memory ranks below a recent or used one with the same words but still shows when it is the only match. `memory show` prints the strength. Decay never deletes or hides; a memory past `--expires` is invalid as before. `TestOldUnusedMemoriesRankBelowRecentUsedOnes` and `TestConsolidateAt5000IsCheap` (5,000 memories in about 0.3 s) cover it.

## Footprint (mem-guard)

Nothing runs besides the issue-cli call. Measured on 2026-10-31 on this Mac (36 GB, mem-guard at 12%), a sandbox project with 5,000 memories (a 1.3 MB log):

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
