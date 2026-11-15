# Support digest

What customers asked this week, grouped, for the owner and for Product.

1. Read the week's support conversations: the support connector
   (`issue-cli connectors sales`), or what the owner gave on this issue.
   With no source, say so under ## Research and stop: no digest from
   nothing.
2. Group them by theme: a count and one short quote per theme. No names,
   emails or other personal data.
3. File each feature request as a request to Product via HQ:
   `issue-cli create --title "<what they ask>" --requested-by sales`, and
   list the slugs under the theme.
4. Put the digest on the shelf (`issue-cli output add --issue <slug> --kind report --file <digest>`),
   move to owner review and say in chat that it waits for the owner.
