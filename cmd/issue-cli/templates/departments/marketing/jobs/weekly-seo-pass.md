Weekly SEO pass over your site(s) for {{period}} (started {{date}}; previous run: {{last_run}}).

<!-- List the sites this pass covers (your own site, or each one you run) and what a pass covers. -->

Write to the brand and the ICP in `departments/marketing/brand.md` and `icp.md`. The run can't leave its brief before they exist: the first time, open the brand foundation (`issue-cli create --department marketing --type foundation`).

For each site:

- Check the pages that changed or lost traffic since the previous run, and the local pages, FAQs and schema.
- Refresh what is out of date: facts, opening hours, prices, links, FAQ answers.
- Put each refreshed page on the shelf: `issue-cli output add --issue <this run> --title "<site>: <page>" --kind page --url <page>`.
- Draft a blog post where the site is due one, and put the draft on the shelf: `issue-cli output add --issue <this run> --title "<site>: <post title>" --kind post --file <draft>`.

This run is an `seo-pass`: after the owner approves and the changes are out, it measures. Record the week's numbers then: `issue-cli goal set marketing/pages <pages refreshed>` (and `marketing/posts` once posts are published). A site too big for this pass gets its own: `issue-cli create --department marketing --type seo-pass --system SEO --title "<site>: SEO pass"`.

Done when every site this pass covers has been checked, each change is on the shelf for the owner's review, and the goals are recorded.
