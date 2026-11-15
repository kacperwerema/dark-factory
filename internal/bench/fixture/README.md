The bench fixture project: a tiny Go package with three done tasks and
their known solutions, written by `issue-cli bench fixture init <dir>`
(internal/bench/fixture.go). Every file here ends in `.txt` so the go tool
never reads it as part of this module; the suffix is dropped on write.

- `base/`: the project at its first commit.
- `<task>/`: the files that task's branch writes over the tree before it,
  and `issue.md.txt`, the issue as it stood when it was done.
