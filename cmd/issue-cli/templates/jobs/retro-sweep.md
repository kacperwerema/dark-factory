Retro sweep for {{department}}, run {{period}} (started {{date}}; previous run: {{last_run}}).

You read the retros and post-mortems written since the last sweep, group what they found by cause, and hand the lead one report. You list proposals; you file nothing and fix nothing.

1. List what this sweep reads, and keep the archive command it prints at the end:

       issue-cli retro list

   If it prints "none", the sweep still closes, with a one-line report: <mark> is the time in list's heading (or "the start" when it says no sweep is recorded yet). Write the line to a file outside the repo and put it on the shelf, then post the `report:` comment, and you are done (skip steps 2 to 6: there is nothing to archive):

       printf '%s\n' "No retros since <mark>; follow-ups: none." > "${TMPDIR:-/tmp}/no-retros.md"
       issue-cli output add --issue {{slug}} --kind report --title "Retro sweep {{period}}: no retros" --file "${TMPDIR:-/tmp}/no-retros.md"
       issue-cli comment {{slug}} "report: no retros since <mark>; follow-ups: none"
2. Read each retro whole. Of a post-mortem, read `## Did not work` and `## Proposed actions`.
3. Group the findings by cause. A cause's count is the number of files that name it. For each cause check `issue-cli list` and `git log main`: is it open, or fixed already (name the commit)?
4. Write the report: the causes by count, then one proposed issue per open cause, typed and sized, with the post-mortem actions it covers (slug and action number). Experiments and gate changes are listed for the owner, not proposed as issues. Put it on the shelf:

       issue-cli output add --issue {{slug}} --kind report --title "Retro sweep {{period}}" --file <report.md>

5. Archive what you read, with the command step 1 printed. It moves the mark, so run it last. Post-mortems the owner hasn't answered stay in retros/ and the output names them.
6. Post `issue-cli comment {{slug}} "report: <the output's id>, <causes> causes, <proposals> proposals"`.

The lead files the proposals: you never run `issue-cli create`, whatever a report guidance says. The owner decides each post-mortem action on its page: an issue never applies one, and neither do you.

Rules for this run: never edit, delete or move a retro by hand; never answer a post-mortem action; never edit a doc or any file in the repo; never turn on a job's standing start; never move any issue but this one. If a command fails, post the error in a `progress:` comment and stop.

Done when the report is on the shelf, the archive ran (when list printed files), and the `report:` comment is posted.
