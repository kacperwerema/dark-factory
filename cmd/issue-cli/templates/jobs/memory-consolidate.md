Memory consolidation for {{department}}, run {{period}} (started {{date}}; previous run: {{last_run}}).

You tidy the agents' shared memory so the next agents find one clear fact instead of three half-copies. The engine has no model: it lists, you decide, through the same update step every agent uses. Read the pack first:

    issue-cli memory consolidate

It lists the memories you can see in five groups: near-duplicates (clusters of memories in one scope with mostly the same words), stale ones (old and unused, strength under 0.25), expired ones (past their --expires day), proven ones (project conventions, pitfalls and howtos used 5+ times, 30+ days old), and unhelpful ones (shown in 15+ traced stages that passed their gate first try no more often than usual). Each line shows the memory's usefulness when the traces measured it.

For each group:

- **Near-duplicates.** Open each member (`issue-cli memory show <id>`) and its source before you trust it. The same fact: merge it into one, in the clearest words, with every id it replaces: `issue-cli memory add "<one fact>" --scope <its scope> --supersedes <id>,<id>`. Or keep the best one (`--updates <id>` to reword it) and `issue-cli memory forget <other> --reason "merged into <id>"`. Different facts that share words: leave them.
- **Stale.** Still true: leave it; decay already ranks it low. No longer true: `issue-cli memory forget <id> --reason "<why>"`, or `--supersedes <id>` with what is true now.
- **Expired.** True again: add a fresh one with `--supersedes <id>`. Otherwise leave it.
- **Promote.** File one issue for the owner: `issue-cli create --title "Promote memories to CLAUDE.md ({{period}})"`, then `issue-cli append <slug> --body "..."` listing each id, its text and the line you would add to CLAUDE.md. It waits for the owner like any other issue.
- **Unhelpful.** Never forget one yourself: the owner decides. List each id, its text and its usefulness line (`issue-cli memory show <id>`) in the same owner issue under "Propose forgetting", with your reason to keep or drop it. Pinned memories never appear here.

Rules for this run: never delete (only the owner deletes, on the memory page), never pin or unpin (leads and the owner pin), never edit CLAUDE.md or any other file in the repo, never write secrets, and never move any issue but this one. Merge only memories you can read; a scope you can't see is not yours.

Post one `progress:` comment with the counts: clusters merged, kept apart, forgotten, promoted, proposed for forgetting (and the owner issue's slug).

Done when every cluster has a decision, the stale and expired ones were looked at, the owner issue is filed (or there was nothing to promote or propose), and the progress comment is posted.
