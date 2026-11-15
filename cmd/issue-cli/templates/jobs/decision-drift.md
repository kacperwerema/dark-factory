Decision drift sweep for {{department}}, run {{period}} (started {{date}}; previous run: {{last_run}}).

You sweep the decision ledger for drift between gates and hand the owner one issue to read. The engine finds the drift; you file it. You fix nothing: a decision changes only by the owner's words, and each cited issue is its own worker's.

1. Sweep and move the mark, so the next run reads only what reached main after this one:

       issue-cli decisions drift --record

   It prints markdown: open issues citing replaced or expired decisions, hard decisions whose `paths:` changed on main without a citation, Settled rules lines in the docs naming replaced or expired decisions (file:line), and provisional decisions older than 14 days that reach beyond their issue. Each line ends with its fix.
2. It printed "No drift.": post `issue-cli comment {{slug}} "progress: no decision drift"` and you are done.
3. Otherwise file one issue for the owner and the lead, with the report as its body:

       issue-cli create --title "Decision drift ({{period}})"
       issue-cli append <new slug> --body "<the report, as printed>"

   Then post `issue-cli comment {{slug}} "progress: decision drift filed as <new slug>: <the counts per kind>"`.

Rules for this run: run the sweep once, with `--record`; never confirm, amend, replace, renew or let go of a decision; never edit a doc, a Settled rule or any file in the repo; never comment on, move or edit the issues the report names; never turn on a job's standing start; never move any issue but this one. If the command fails, post the error in a `progress:` comment and stop.

Done when the sweep ran with `--record` and either the "no drift" comment or the filed issue's comment is posted.
