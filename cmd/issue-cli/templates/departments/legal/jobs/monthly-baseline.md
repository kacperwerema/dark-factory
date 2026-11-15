Compliance baseline for {{period}} (started {{date}}; previous run: {{last_run}}).

<!-- Name the product this baseline covers: its repo, its live site, the countries it sells in. -->

Re-run the product's baseline: one line per check, in a table with the columns check · source (article, read on) · how to verify · status (passes, fails, missing, n/a, check). The first run writes the table from the role's areas; each later run starts from the previous run's report on the shelf (`issue-cli outputs legal`) and keeps its numbering.

Areas to cover:

- GDPR: the lawful basis per purpose, special-category data (art. 9) and how consent is recorded per person, data minimisation, retention, data subject rights, processors and their agreements (art. 28), transfers outside the EEA (art. 44–46), the art. 30 register, a DPIA where art. 35 applies, the breach procedure (art. 33).
- Consumer law: terms and pre-contract information, withdrawal and its exceptions, confirmation on a durable medium, prices with VAT and the lowest-30-day price on discounts, complaints, what replaced the ODR link.
- Third parties: the terms of each service the product automates or resells, by version.
- AI: disclosure where people talk to a model.

Verify each line against the product as it is today (the code, the texts, the site), never from memory. Cite each source by article with the date you read it.

Put the report on the shelf (`issue-cli output add --issue <this run> --kind report --title "Compliance baseline {{period}}" --file <report.md>`) with a short diff against the previous run at the top: what started failing, what was fixed, what is new. Record the goals: `issue-cli goal set legal/baseline-pass <pct passing>` and `issue-cli goal set legal/open-findings <count>`.

You flag and cite; you never sign off. Done when every line has a status and a dated source, the report is on the shelf for the owner's review, and the goals are recorded.
