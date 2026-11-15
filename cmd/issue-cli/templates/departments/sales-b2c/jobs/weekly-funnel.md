# Weekly funnel review

The week's funnel from real numbers, for the owner.

1. Read last week's numbers: visits, sign-ups, active testers, retention
   (four weeks on). Each from a connector (`issue-cli connectors sales`;
   visits from `umami`, commented out in projects.yaml until the owner turns
   it on) or from numbers the owner gave on this issue. Never estimate one: a number
   you can't read is left out and named as "no value yet" with the reason.
   `umami` is read-only: it makes GETs on its stats path only, one JSON
   line per path, the week's `visits` in the body:

       issue-cli connector exec sales/umami -- '/websites/<id>/stats?startAt=<ms>&endAt=<ms>'
2. Write the metrics you read, only those:

       issue-cli goal write sales/weekly-funnel visits=512 signups=31

   It replaces the job's metrics file (<issues>/.goals/sales/weekly-funnel.json)
   atomically and names the goal reading each id. The ids are the goal ids
   (`issue-cli goals sales`). The goals read this file; nothing else writes it.
3. Write the report: each number beside last week's, where it came from,
   what moved, and one thing to try next week (an offer, a page, a launch:
   name the issue to create, don't create it). Put it on the shelf:
   `issue-cli output add --issue <slug> --kind report --title "Funnel: week <n>" --file <report>`.
4. Move to owner review and say in chat that it waits for the owner.
