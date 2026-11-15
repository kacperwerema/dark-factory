Weekly SEO pass over your site(s) for {{period}} (started {{date}}; previous run: {{last_run}}).

<!-- List the sites this pass covers (your own site, or each one you run) and what a pass covers. -->

Write to the brand and the ICP in `departments/marketing/brand.md` and `icp.md`. The run can't leave its brief before they exist: the first time, open the brand foundation (`issue-cli create --department marketing --type foundation`).

For each site:

- Check the pages that changed or lost traffic since the previous run, and the local pages, FAQs and schema.
- Refresh what is out of date: facts, opening hours, prices, links, FAQ answers.
- Put each refreshed page on the shelf: `issue-cli output add --issue <this run> --title "<site>: <page>" --kind page --url <page>`.
- Draft a blog post where the site is due one, and put the draft on the shelf: `issue-cli output add --issue <this run> --title "<site>: <post title>" --kind post --file <draft>`.

This run is an `seo-pass`: after the owner approves and the changes are out, it measures. Read ranks, Search Console and GA4 from the `openseo` connector when the department has it on (its example is commented out in projects.yaml); without it, a number you can't read is "no value yet". Cost cap for this pass: $1 of paid data (give maxCostUsd on each paid call). Read the week's visits from the `umami` connector (its example is commented out in projects.yaml too; read-only, it makes GETs on its stats path only: `issue-cli connector exec marketing/umami -- '/websites/<id>/stats?startAt=<ms>&endAt=<ms>'`, the week's `visits` in the body) and write them: `issue-cli goal write marketing/weekly-seo-pass visits=<visits>`; a number you can't read is left out and named as "no value yet". Record the rest then: `issue-cli goal set marketing/pages <pages refreshed>` (and `marketing/posts` once posts are published). A site too big for this pass gets its own: `issue-cli create --department marketing --type seo-pass --system SEO --title "<site>: SEO pass"`.

Done when every site this pass covers has been checked, each change is on the shelf for the owner's review, and the goals are recorded.
