Week plan and report for {{department}}, week {{period}} (started {{date}}; previous run: {{last_run}}).

You are planning the week of a group and its departments: what each one does this week, from what is already on disk. Read the pack first:

    issue-cli department week {{department}}

It lists, per department: the goals and how far each moved in the last 7 days, outputs waiting on the owner's review, reviewed and shipped, the recurring jobs and their verdicts, handoffs in and out, and what is blocked and on whom.

Write `## Week plan` in this issue, with a `### <department>` heading each:

- What the department does this week and why: each line tied to a goal that lags, an output to finish or fix, a due job, a handoff to answer, or something blocked.
- New work: file it as an issue in the department (`issue-cli create --department <dept> --title "..."`, then `issue-cli append <slug> --body "..."`), then link it. It lands at its first status and waits for the owner's start like any other issue.
- Due jobs: name the job and the command the owner or its lead runs (`issue-cli job run <job> --quote "<the owner's words>"`). Don't run it.
- What waits on whom: outputs for the owner's review, gates, forwards a lead owes.

Rules for this run: never dispatch, approve, run a job, or move any issue but this one. Anything that publishes, sends or spends waits for the owner at its own owner-only gate; the plan only says so.

Then write the report: `issue-cli department week {{department}} --write`. It writes the week report with your plan in it, under the work files, and the group's room on the company page links it. Post its path as a `progress:` comment.

Done when the plan names each department's week, its new issues are filed and linked, and the report is written.
