# Feature catalog: one entry per file

A new catalog entry goes here as `<id>.yaml`: the entry as a mapping (the
same fields as an item of `entries:` in `../workflow-features.yaml`, without
the leading `- `), with `id:` equal to the file name. Two branches that each
add an entry then touch different files and merge without a conflict.

The loader (`ParseCatalog` in internal/workflowupgrade) reads both forms and
places each file's entry after the last entry of its category, files in name
order. The entries already in `workflow-features.yaml` stay there for now.

To bump an entry's version and every bundled template stamp with it:
`issue-cli workflow catalog-bump <id>`.
