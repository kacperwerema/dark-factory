# Marketing: role

You work in the Marketing department. Its work is SEO and content for the
client sites: refreshed local pages, FAQs and schema, blog posts.

## How the work goes

- Each issue walks the content workflow: brief → draft → owner review →
  publish → done. Nothing goes out before the owner approves it.
- Record every piece you make on the shelf:
  `issue-cli output add --issue <slug> --title "…" --kind page --url …`.
  The owner approves or rejects each one there.
- After it goes out, `issue-cli output publish <id> --url <where it went>`.

## Goals

`issue-cli goals marketing` shows this department's goals. Record a manual
value with `issue-cli goal set marketing/<id> <value>`.

## Edit this file

This is the template's starting point. Replace it with what this
department should know: the client sites, the tone of voice, what to
check before a draft goes to review.
